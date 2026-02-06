package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"trackway/internal/config"
	"trackway/internal/logstore"
	"trackway/internal/util"
)

type Notifier interface {
	SendDefaultHTML(ctx context.Context, text string) error
	SendDefaultHTMLWithID(ctx context.Context, text string) (int, error)
	EditDefaultHTML(ctx context.Context, messageID int, text string) error
	SendHTML(ctx context.Context, chatID int64, text string) error
}

type Service struct {
	notifier Notifier
	logs     *logstore.Store
	logger   *slog.Logger

	interval     time.Duration
	timeout      time.Duration
	checkWorkers int

	mu           sync.RWMutex
	targets      []*TargetState
	targetByName map[string]*TargetState
	pendingDown  map[string]pendingDownAlert
}

type TargetState struct {
	Name        string
	Address     string
	Port        int
	LastStatus  *bool
	LastChanged time.Time
	LastChecked time.Time
}

type alertEvent struct {
	Kind     string
	Target   string
	Address  string
	Port     int
	Reason   string
	Occurred time.Time
}

type pendingDownAlert struct {
	MessageID int
	DownAt    time.Time
	Reason    string
	Address   string
	Port      int
}

func New(cfg config.Config, logs *logstore.Store, notifier Notifier) *Service {
	targets := buildTargets(cfg.Targets)
	byName := make(map[string]*TargetState, len(targets))
	for _, target := range targets {
		byName[target.Name] = target
	}
	return &Service{
		notifier:     notifier,
		logs:         logs,
		logger:       slog.Default(),
		interval:     defaultSeconds(cfg.Monitoring.IntervalSeconds, 5),
		timeout:      defaultSeconds(cfg.Monitoring.ConnectTimeoutSeconds, 2),
		checkWorkers: defaultWorkers(cfg.Monitoring.MaxParallelChecks, len(targets)),
		targets:      targets,
		targetByName: byName,
		pendingDown:  make(map[string]pendingDownAlert, len(targets)),
	}
}

func (s *Service) RunMonitor(ctx context.Context) {
	s.runChecks(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runChecks(ctx)
		}
	}
}

func (s *Service) HandleUpdate(ctx context.Context, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Text == "" {
		return
	}
	command, arg, ok := parseCommand(msg.Text)
	if !ok {
		return
	}

	var response string
	switch command {
	case "start", "help":
		response = helpText()
	case "list":
		response = s.listText()
	case "status":
		response = s.statusText()
	case "logs":
		if arg == "" {
			response = "Usage: /logs &lt;track_name&gt;"
		} else {
			if s.notifier == nil {
				return
			}
			for _, message := range s.logsMessages(arg) {
				if err := s.notifier.SendHTML(ctx, msg.Chat.ID, message); err != nil {
					s.logger.Warn("failed to send logs message", "track", arg, "error", err)
				}
			}
			return
		}
	default:
		return
	}

	if s.notifier == nil {
		return
	}
	if err := s.notifier.SendHTML(ctx, msg.Chat.ID, response); err != nil {
		s.logger.Warn("failed to send command response", "command", command, "chat_id", msg.Chat.ID, "error", err)
	}
}

func (s *Service) runChecks(ctx context.Context) {
	if len(s.targets) == 0 {
		return
	}

	workers := s.checkWorkers
	if workers > len(s.targets) {
		workers = len(s.targets)
	}

	sem := make(chan struct{}, workers)
	eventsCh := make(chan alertEvent, len(s.targets))
	var wg sync.WaitGroup

	for _, target := range s.targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t *TargetState) {
			defer wg.Done()
			defer func() { <-sem }()
			status := checkTCP(ctx, t.Address, t.Port, s.timeout)
			if event := s.applyStatus(t, status); event != nil {
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
	s.sendAlertBatch(ctx, events)
}

func (s *Service) applyStatus(target *TargetState, status bool) *alertEvent {
	now := time.Now().UTC()
	s.mu.Lock()
	reason := ""
	shouldLog := false
	var event *alertEvent
	target.LastChecked = now
	if target.LastStatus == nil {
		target.LastStatus = boolPtr(status)
		target.LastChanged = now
		reason = "INIT"
		shouldLog = true
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
		shouldLog = true
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
	s.mu.Unlock()

	if shouldLog {
		if err := s.logs.Append(target.Name, target.Address, target.Port, status, reason); err != nil {
			s.logger.Warn("failed to append log row", "track", target.Name, "error", err)
		}
	}
	return event
}

func (s *Service) sendAlertBatch(ctx context.Context, events []alertEvent) {
	if s.notifier == nil || len(events) == 0 {
		return
	}

	events = s.applyFastRecoveryEdits(ctx, events, 30*time.Second)
	if len(events) == 0 {
		return
	}

	groups := make(map[string][]alertEvent)
	order := make([]string, 0, len(events))
	for _, event := range events {
		key := event.Kind + "|" + event.Reason
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], event)
	}

	sort.SliceStable(order, func(i, j int) bool {
		leftKind, _, _ := strings.Cut(order[i], "|")
		rightKind, _, _ := strings.Cut(order[j], "|")
		if leftKind != rightKind {
			return alertOrder(leftKind) < alertOrder(rightKind)
		}
		return order[i] < order[j]
	})

	for _, key := range order {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return group[i].Target < group[j].Target })
		message := formatAlertGroup(group)
		kind, reason, _ := strings.Cut(key, "|")

		if kind == "DOWN" && reason == "state-change" && len(group) == 1 {
			messageID, err := s.notifier.SendDefaultHTMLWithID(ctx, message)
			if err != nil {
				s.logger.Warn("failed to send grouped alert", "key", key, "count", len(group), "error", err)
				continue
			}
			if messageID > 0 {
				ev := group[0]
				s.pendingDown[ev.Target] = pendingDownAlert{
					MessageID: messageID,
					DownAt:    ev.Occurred,
					Reason:    ev.Reason,
					Address:   ev.Address,
					Port:      ev.Port,
				}
			}
			continue
		}

		if err := s.notifier.SendDefaultHTML(ctx, message); err != nil {
			s.logger.Warn("failed to send grouped alert", "key", key, "count", len(group), "error", err)
		}
	}
}

func (s *Service) applyFastRecoveryEdits(ctx context.Context, events []alertEvent, window time.Duration) []alertEvent {
	remaining := make([]alertEvent, 0, len(events))
	for _, ev := range events {
		if ev.Kind != "RECOVERED" || ev.Reason != "state-change" {
			remaining = append(remaining, ev)
			continue
		}

		pending, ok := s.pendingDown[ev.Target]
		if !ok {
			remaining = append(remaining, ev)
			continue
		}
		delete(s.pendingDown, ev.Target)

		if ev.Occurred.Sub(pending.DownAt) > window {
			remaining = append(remaining, ev)
			continue
		}

		editText := formatRecoveredEdit(ev, pending)
		if err := s.notifier.EditDefaultHTML(ctx, pending.MessageID, editText); err != nil {
			s.logger.Warn("failed to edit down alert message", "track", ev.Target, "error", err)
			remaining = append(remaining, ev)
		}
	}
	return remaining
}

func formatRecoveredEdit(recovered alertEvent, pending pendingDownAlert) string {
	downtime := recovered.Occurred.Sub(pending.DownAt)
	if downtime < 0 {
		downtime = 0
	}
	var sb strings.Builder
	sb.WriteString("<b>DOWN -> RECOVERED</b>\n")
	fmt.Fprintf(&sb, "reason: <code>%s</code>\n", util.HTMLEscape(recovered.Reason))
	fmt.Fprintf(&sb, "down_at_utc: <code>%s</code>\n", pending.DownAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "recovered_at_utc: <code>%s</code>\n", recovered.Occurred.Format(time.RFC3339))
	fmt.Fprintf(&sb, "downtime: <code>%s</code>\n", formatDurationShort(downtime))
	sb.WriteString("target:\n")
	fmt.Fprintf(
		&sb,
		"- <code>%s</code> (<code>%s:%d</code>)",
		util.HTMLEscape(recovered.Target),
		util.HTMLEscape(recovered.Address),
		recovered.Port,
	)
	return sb.String()
}

func formatDurationShort(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
}

func formatAlertGroup(events []alertEvent) string {
	if len(events) == 0 {
		return ""
	}
	first := events[0]
	var sb strings.Builder
	if len(events) == 1 {
		fmt.Fprintf(&sb, "<b>%s</b>\n", util.HTMLEscape(first.Kind))
	} else {
		fmt.Fprintf(&sb, "<b>%s x%d</b>\n", util.HTMLEscape(first.Kind), len(events))
	}
	fmt.Fprintf(&sb, "reason: <code>%s</code>\n", util.HTMLEscape(first.Reason))
	fmt.Fprintf(&sb, "time_utc: <code>%s</code>\n", first.Occurred.Format(time.RFC3339))
	sb.WriteString("targets:\n")
	for _, event := range events {
		fmt.Fprintf(
			&sb,
			"- <code>%s</code> (<code>%s:%d</code>)\n",
			util.HTMLEscape(event.Target),
			util.HTMLEscape(event.Address),
			event.Port,
		)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func alertOrder(kind string) int {
	switch kind {
	case "DOWN":
		return 0
	case "RECOVERED":
		return 1
	default:
		return 2
	}
}

func (s *Service) listText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.targets) == 0 {
		return "No tracks configured."
	}
	var sb strings.Builder
	sb.WriteString("<b>Configured tracks</b>\n")
	for i, target := range s.targets {
		fmt.Fprintf(
			&sb,
			"%d. <b>%s</b> - <code>%s:%d</code>\n",
			i+1,
			util.HTMLEscape(target.Name),
			util.HTMLEscape(target.Address),
			target.Port,
		)
	}
	return sb.String()
}

func (s *Service) statusText() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.targets) == 0 {
		return "No tracks configured."
	}

	up, down, unknown := 0, 0, 0
	for _, target := range s.targets {
		if target.LastStatus == nil {
			unknown++
		} else if *target.LastStatus {
			up++
		} else {
			down++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>Status snapshot (UTC)</b>\ntracks: %d | up: %d | down: %d | unknown: %d\n\n", len(s.targets), up, down, unknown)
	for i, target := range s.targets {
		state := "UNKNOWN"
		if target.LastStatus != nil {
			if *target.LastStatus {
				state = "UP"
			} else {
				state = "DOWN"
			}
		}
		fmt.Fprintf(
			&sb,
			"%d. <b>%s</b>\nendpoint: <code>%s:%d</code>\nstate: <b>%s</b>\nchanged: <code>%s</code>\nchecked: <code>%s</code>\n\n",
			i+1,
			util.HTMLEscape(target.Name),
			util.HTMLEscape(target.Address),
			target.Port,
			state,
			util.FormatTime(target.LastChanged),
			util.FormatTime(target.LastChecked),
		)
	}
	return sb.String()
}

func (s *Service) logsMessages(trackName string) []string {
	s.mu.RLock()
	target := s.targetByName[trackName]
	s.mu.RUnlock()
	if target == nil {
		return []string{"Track not found. Use /list."}
	}

	rows := s.logs.ReadLastDays(target.Name, 7, 120)
	if len(rows) == 0 {
		return []string{"No log rows for last 7 days."}
	}

	upCount, downCount := 0, 0
	for _, row := range rows {
		switch row.Status {
		case "UP":
			upCount++
		case "DOWN":
			downCount++
		}
	}

	header := fmt.Sprintf("Track: <b>%s</b> | rows: %d | up: %d | down: %d", util.HTMLEscape(target.Name), len(rows), upCount, downCount)
	return renderLogChunks(header, rows)
}

func parseCommand(text string) (string, string, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" || raw[0] != '/' {
		return "", "", false
	}
	parts := strings.Fields(raw)
	command := strings.TrimPrefix(parts[0], "/")
	if idx := strings.Index(command, "@"); idx > 0 {
		command = command[:idx]
	}
	if command == "" {
		return "", "", false
	}
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}
	return strings.ToLower(command), arg, true
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

func renderLogChunks(header string, rows []logstore.Row) []string {
	if len(rows) == 0 {
		return []string{header + "\n<pre>(empty)</pre>"}
	}

	base := header + "\n<pre>"
	suffix := "</pre>"
	maxBody := 3800 - len(base) - len(suffix)
	if maxBody < 256 {
		maxBody = 256
	}

	chunks := make([]string, 0, 2)
	current := strings.Builder{}
	for _, row := range rows {
		line := fmt.Sprintf("%s  %-4s  %-21s  %s\n", row.Timestamp, row.Status, row.Endpoint, row.Reason)
		if current.Len() > 0 && current.Len()+len(line) > maxBody {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	if len(chunks) == 1 {
		return []string{base + util.HTMLEscape(strings.TrimSuffix(chunks[0], "\n")) + suffix}
	}

	out := make([]string, 0, len(chunks))
	for idx, chunk := range chunks {
		title := fmt.Sprintf("%s (%d/%d)", header, idx+1, len(chunks))
		body := util.HTMLEscape(strings.TrimSuffix(chunk, "\n"))
		out = append(out, title+"\n<pre>"+body+"</pre>")
	}
	return out
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

func boolPtr(value bool) *bool {
	return &value
}

func helpText() string {
	return "<b>Port Tracker Bot</b>\n/list - tracks\n/status - current states\n/logs &lt;track&gt; - last 7 days"
}
