package tracker

import (
	"context"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"trackway/internal/config"
	"trackway/internal/logstore"
)

type MonitorEngine struct {
	logs   *logstore.Store
	logger *slog.Logger

	interval     time.Duration
	timeout      time.Duration
	checkWorkers int

	mu           sync.RWMutex
	targets      []*TargetState
	targetByName map[string]*TargetState
}

func NewMonitorEngine(cfg config.Config, logs *logstore.Store) *MonitorEngine {
	targets := buildTargets(cfg.Targets)
	byName := make(map[string]*TargetState, len(targets))
	for _, target := range targets {
		byName[target.Name] = target
	}

	return &MonitorEngine{
		logs:         logs,
		logger:       slog.Default(),
		interval:     defaultSeconds(cfg.Monitoring.IntervalSeconds, 5),
		timeout:      defaultSeconds(cfg.Monitoring.ConnectTimeoutSeconds, 2),
		checkWorkers: defaultWorkers(cfg.Monitoring.MaxParallelChecks, len(targets)),
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
	if len(e.targets) == 0 {
		return
	}

	workers := e.checkWorkers
	if workers > len(e.targets) {
		workers = len(e.targets)
	}

	sem := make(chan struct{}, workers)
	eventsCh := make(chan alertEvent, len(e.targets))
	var wg sync.WaitGroup

	for _, target := range e.targets {
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
	if limit > 5000 {
		limit = 5000
	}

	e.mu.RLock()
	target := e.targetByName[trackName]
	e.mu.RUnlock()
	if target == nil {
		return nil, false
	}

	return e.logs.ReadLastDays(target.Name, days, limit), true
}

func buildTargets(items []config.Target) []*TargetState {
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
	return value
}
