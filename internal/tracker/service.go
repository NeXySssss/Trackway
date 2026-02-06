package tracker

import (
	"context"
	"fmt"
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
	SendHTML(ctx context.Context, chatID int64, text string) error
}

type Service struct {
	notifier Notifier
	logs     *logstore.Store

	interval time.Duration
	timeout  time.Duration

	mu           sync.RWMutex
	targets      []*TargetState
	targetByName map[string]*TargetState
}

func (s *Service) SetNotifier(notifier Notifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifier = notifier
}

type TargetState struct {
	Name        string
	Address     string
	Port        int
	LastStatus  *bool
	LastChanged time.Time
	LastChecked time.Time
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
		interval:     defaultSeconds(cfg.Monitoring.IntervalSeconds, 5),
		timeout:      defaultSeconds(cfg.Monitoring.ConnectTimeoutSeconds, 2),
		targets:      targets,
		targetByName: byName,
	}
}

func (s *Service) RunMonitor(ctx context.Context) {
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
				_ = s.notifier.SendHTML(ctx, msg.Chat.ID, message)
			}
			return
		}
	default:
		return
	}

	if s.notifier == nil {
		return
	}
	_ = s.notifier.SendHTML(ctx, msg.Chat.ID, response)
}

func (s *Service) runChecks(ctx context.Context) {
	for _, target := range s.targets {
		status := checkTCP(target.Address, target.Port, s.timeout)
		s.applyStatus(ctx, target, status)
	}
}

func (s *Service) applyStatus(ctx context.Context, target *TargetState, status bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	reason := ""
	shouldLog := false
	alertKind := ""
	alertReason := ""
	target.LastChecked = now
	if target.LastStatus == nil {
		target.LastStatus = boolPtr(status)
		target.LastChanged = now
		reason = "INIT"
		shouldLog = true
		if !status {
			alertKind = "DOWN"
			alertReason = "initial-check"
		}
	} else if *target.LastStatus != status {
		prev := *target.LastStatus
		*target.LastStatus = status
		target.LastChanged = now
		reason = "CHANGE"
		shouldLog = true
		if prev && !status {
			alertKind = "DOWN"
			alertReason = "state-change"
		} else if !prev && status {
			alertKind = "RECOVERED"
			alertReason = "state-change"
		}
	}
	s.mu.Unlock()

	if shouldLog {
		_ = s.logs.Append(target.Name, target.Address, target.Port, status, reason)
	}
	if alertKind != "" {
		_ = s.sendAlert(ctx, alertKind, target, alertReason)
	}
}

func (s *Service) sendAlert(ctx context.Context, kind string, target *TargetState, reason string) error {
	if s.notifier == nil {
		return nil
	}
	text := fmt.Sprintf(
		"<b>%s</b>\ntrack: <code>%s</code>\nendpoint: <code>%s:%d</code>\nreason: <code>%s</code>\ntime_utc: <code>%s</code>",
		util.HTMLEscape(kind),
		util.HTMLEscape(target.Name),
		util.HTMLEscape(target.Address),
		target.Port,
		util.HTMLEscape(reason),
		time.Now().UTC().Format(time.RFC3339),
	)
	return s.notifier.SendDefaultHTML(ctx, text)
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

func checkTCP(address string, port int, timeout time.Duration) bool {
	endpoint := net.JoinHostPort(address, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", endpoint, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func formatRows(rows []logstore.Row) string {
	var sb strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&sb, "%s  %-4s  %-21s  %s\n", row.Timestamp, row.Status, row.Endpoint, row.Reason)
	}
	return strings.TrimSuffix(sb.String(), "\n")
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

func boolPtr(value bool) *bool {
	return &value
}

func helpText() string {
	return "<b>Port Tracker Bot</b>\n/list - tracks\n/status - current states\n/logs &lt;track&gt; - last 7 days"
}
