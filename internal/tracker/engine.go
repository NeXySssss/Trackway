package tracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trackway/internal/config"
	"trackway/internal/logstore"
)

const maxParallelChecksHardLimit = 256

type MonitorEngine struct {
	logs   *logstore.Store
	logger *slog.Logger

	interval    time.Duration
	timeout     time.Duration
	maxParallel int

	mu           sync.RWMutex
	targets      []*TargetState
	targetByName map[string]*TargetState
}

func NewMonitorEngine(cfg config.Config, logs *logstore.Store) *MonitorEngine {
	targets := buildTargetsFromConfig(cfg.Targets)
	byName := make(map[string]*TargetState, len(targets))
	for _, target := range targets {
		byName[target.Name] = target
	}

	return &MonitorEngine{
		logs:         logs,
		logger:       slog.Default(),
		interval:     defaultSeconds(cfg.Monitoring.IntervalSeconds, 5),
		timeout:      defaultSeconds(cfg.Monitoring.ConnectTimeoutSeconds, 2),
		maxParallel:  cfg.Monitoring.MaxParallelChecks,
		targets:      targets,
		targetByName: byName,
	}
}

func (e *MonitorEngine) Run(ctx context.Context, onEvents func([]alertEvent)) {
	if onEvents == nil {
		onEvents = func([]alertEvent) {}
	}
	e.runChecks(ctx, onEvents)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runChecks(ctx, onEvents)
		}
	}
}

func (e *MonitorEngine) runChecks(ctx context.Context, onEvents func([]alertEvent)) {
	e.syncTargets()

	e.mu.RLock()
	targets := append([]*TargetState(nil), e.targets...)
	e.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	workers := defaultWorkers(e.maxParallel, len(targets))

	sem := make(chan struct{}, workers)
	eventsCh := make(chan alertEvent, len(targets))
	var wg sync.WaitGroup

	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t *TargetState) {
			defer wg.Done()
			defer func() { <-sem }()
			status := checkTCP(ctx, t.Address, t.Port, e.timeout)
			if event := e.applyStatus(t, status); event != nil {
				eventsCh <- *event
			}
		}(target)
	}

	wg.Wait()
	close(eventsCh)

	events := make([]alertEvent, 0, len(eventsCh))
	for event := range eventsCh {
		events = append(events, event)
	}
	onEvents(events)
}

func (e *MonitorEngine) applyStatus(target *TargetState, status bool) *alertEvent {
	now := time.Now().UTC()
	e.mu.Lock()
	reason := "POLL"
	var event *alertEvent
	target.LastChecked = now
	if target.LastStatus == nil {
		target.LastStatus = boolPtr(status)
		target.LastChanged = now
		reason = "INIT"
		if !status {
			event = &alertEvent{
				Kind:     "DOWN",
				Target:   target.Name,
				Address:  target.Address,
				Port:     target.Port,
				Reason:   "initial-check",
				Occurred: now,
			}
		}
	} else if *target.LastStatus != status {
		prev := *target.LastStatus
		*target.LastStatus = status
		target.LastChanged = now
		reason = "CHANGE"
		if prev && !status {
			event = &alertEvent{
				Kind:     "DOWN",
				Target:   target.Name,
				Address:  target.Address,
				Port:     target.Port,
				Reason:   "state-change",
				Occurred: now,
			}
		} else if !prev && status {
			event = &alertEvent{
				Kind:     "RECOVERED",
				Target:   target.Name,
				Address:  target.Address,
				Port:     target.Port,
				Reason:   "state-change",
				Occurred: now,
			}
		}
	}
	e.mu.Unlock()

	if err := e.logs.Append(target.Name, target.Address, target.Port, status, reason); err != nil {
		e.logger.Warn("failed to append log row", "track", target.Name, "error", err)
	}
	return event
}

func (e *MonitorEngine) Snapshot() Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := Snapshot{
		GeneratedAt: time.Now().UTC(),
		Total:       len(e.targets),
		Targets:     make([]TargetSnapshot, 0, len(e.targets)),
	}

	for _, target := range e.targets {
		state := "UNKNOWN"
		switch {
		case target.LastStatus == nil:
			result.Unknown++
		case *target.LastStatus:
			state = "UP"
			result.Up++
		default:
			state = "DOWN"
			result.Down++
		}
		result.Targets = append(result.Targets, TargetSnapshot{
			Name:        target.Name,
			Address:     target.Address,
			Port:        target.Port,
			Status:      state,
			LastChanged: target.LastChanged,
			LastChecked: target.LastChecked,
		})
	}

	return result
}

func (e *MonitorEngine) TargetNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	names := make([]string, 0, len(e.targets))
	for _, target := range e.targets {
		names = append(names, target.Name)
	}
	return names
}

func (e *MonitorEngine) Logs(trackName string, days int, limit int) ([]logstore.Row, bool) {
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 50000 {
		limit = 50000
	}

	e.mu.RLock()
	target := e.targetByName[trackName]
	e.mu.RUnlock()
	if target == nil {
		return nil, false
	}

	return e.logs.ReadLastDays(target.Name, days, limit), true
}

func (e *MonitorEngine) UpsertTarget(name, address string, port int) error {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name == "" {
		return errors.New("target name is required")
	}
	if address == "" {
		return errors.New("target address is required")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("target port must be between 1 and 65535, got %d", port)
	}
	if err := e.logs.UpsertTarget(name, address, port); err != nil {
		return err
	}
	e.syncTargets()
	return nil
}

func (e *MonitorEngine) DeleteTarget(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("target name is required")
	}
	if err := e.logs.DeleteTarget(name); err != nil {
		return err
	}
	e.syncTargets()
	return nil
}

func (e *MonitorEngine) syncTargets() {
	targetRows, err := e.logs.ListTargets()
	if err != nil {
		e.logger.Warn("failed to load targets from store", "error", err)
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	nextTargets := make([]*TargetState, 0, len(targetRows))
	nextByName := make(map[string]*TargetState, len(targetRows))
	for _, row := range targetRows {
		if !row.Enabled || row.Name == "" || row.Address == "" || row.Port <= 0 {
			continue
		}

		target := &TargetState{
			Name:    row.Name,
			Address: row.Address,
			Port:    row.Port,
		}
		if previous := e.targetByName[row.Name]; previous != nil {
			if previous.Address == row.Address && previous.Port == row.Port {
				target.LastStatus = previous.LastStatus
				target.LastChanged = previous.LastChanged
				target.LastChecked = previous.LastChecked
			}
		}

		nextTargets = append(nextTargets, target)
		nextByName[target.Name] = target
	}

	sort.Slice(nextTargets, func(i, j int) bool { return nextTargets[i].Name < nextTargets[j].Name })
	e.targets = nextTargets
	e.targetByName = nextByName
}

func buildTargetsFromConfig(items []config.Target) []*TargetState {
	out := make([]*TargetState, 0, len(items))
	for _, item := range items {
		out = append(out, &TargetState{
			Name:    item.Name,
			Address: item.Address,
			Port:    item.Port,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func checkTCP(ctx context.Context, address string, port int, timeout time.Duration) bool {
	endpoint := net.JoinHostPort(address, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func defaultSeconds(value int, fallback int) time.Duration {
	if value <= 0 {
		value = fallback
	}
	return time.Duration(value) * time.Second
}

func defaultWorkers(value int, targetCount int) int {
	if value <= 0 {
		value = targetCount
	}
	if value < 1 {
		value = 1
	}
	if value > maxParallelChecksHardLimit {
		value = maxParallelChecksHardLimit
	}
	return value
}
