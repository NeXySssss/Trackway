package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/go-telegram/bot/models"

	"trackway/internal/logstore"
	"trackway/internal/util"
)

type QueryProvider interface {
	Snapshot() Snapshot
	Logs(trackName string, days int, limit int) ([]logstore.Row, bool)
}

type CommandHandler struct {
	notifier Notifier
	source   QueryProvider
	logger   *slog.Logger

	allowedChat int64

	mu         sync.RWMutex
	authLinkFn func() (string, error)
}

func NewCommandHandler(allowedChat int64, source QueryProvider, notifier Notifier) *CommandHandler {
	return &CommandHandler{
		notifier:    notifier,
		source:      source,
		logger:      slog.Default(),
		allowedChat: allowedChat,
	}
}

func (h *CommandHandler) SetAuthLinkGenerator(fn func() (string, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authLinkFn = fn
}

func (h *CommandHandler) HandleUpdate(ctx context.Context, update *models.Update) {
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
		response = h.listText()
	case "status":
		response = h.statusText()
	case "authme":
		response = h.authLinkText(msg.Chat.ID)
	case "logs":
		if arg == "" {
			response = "Usage: /logs &lt;track_name&gt;"
		} else {
			if h.notifier == nil {
				return
			}
			for _, message := range h.logsMessages(arg) {
				if err := h.notifier.SendHTML(ctx, msg.Chat.ID, message); err != nil {
					h.logger.Warn("failed to send logs message", "track", arg, "error", err)
				}
			}
			return
		}
	default:
		return
	}

	if h.notifier == nil {
		return
	}
	if err := h.notifier.SendHTML(ctx, msg.Chat.ID, response); err != nil {
		h.logger.Warn("failed to send command response", "command", command, "chat_id", msg.Chat.ID, "error", err)
	}
}

func (h *CommandHandler) listText() string {
	snapshot := h.source.Snapshot()
	if len(snapshot.Targets) == 0 {
		return "No tracks configured."
	}

	targets := append([]TargetSnapshot(nil), snapshot.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	var sb strings.Builder
	sb.WriteString("<b>Configured tracks</b>\n")
	for i, target := range targets {
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

func (h *CommandHandler) statusText() string {
	snapshot := h.source.Snapshot()
	if len(snapshot.Targets) == 0 {
		return "No tracks configured."
	}

	targets := append([]TargetSnapshot(nil), snapshot.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	var sb strings.Builder
	fmt.Fprintf(
		&sb,
		"<b>Status snapshot (UTC)</b>\ntracks: %d | up: %d | down: %d | unknown: %d\n\n",
		snapshot.Total,
		snapshot.Up,
		snapshot.Down,
		snapshot.Unknown,
	)
	for i, target := range targets {
		fmt.Fprintf(
			&sb,
			"%d. <b>%s</b>\nendpoint: <code>%s:%d</code>\nstate: <b>%s</b>\nchanged: <code>%s</code>\nchecked: <code>%s</code>\n\n",
			i+1,
			util.HTMLEscape(target.Name),
			util.HTMLEscape(target.Address),
			target.Port,
			target.Status,
			util.FormatTime(target.LastChanged),
			util.FormatTime(target.LastChecked),
		)
	}
	return sb.String()
}

func (h *CommandHandler) logsMessages(trackName string) []string {
	rows, ok := h.source.Logs(trackName, 7, 120)
	if !ok {
		return []string{"Track not found. Use /list."}
	}
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

	header := fmt.Sprintf(
		"Track: <b>%s</b> | rows: %d | up: %d | down: %d",
		util.HTMLEscape(trackName),
		len(rows),
		upCount,
		downCount,
	)
	return renderLogChunks(header, rows)
}

func (h *CommandHandler) authLinkText(chatID int64) string {
	if h.allowedChat != 0 && chatID != h.allowedChat {
		return "This command is not available in this chat."
	}

	h.mu.RLock()
	generate := h.authLinkFn
	h.mu.RUnlock()
	if generate == nil {
		return "Dashboard auth is disabled. Set dashboard.enabled and dashboard.public_url in config."
	}
	link, err := generate()
	if err != nil {
		h.logger.Warn("failed to generate auth link", "error", err)
		return "Failed to create auth link. Try again in a few seconds."
	}
	escaped := util.HTMLEscape(link)
	return fmt.Sprintf("<b>Dashboard auth</b>\n<a href=\"%s\">Authorize dashboard</a>\n<code>%s</code>", escaped, escaped)
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

func helpText() string {
	return "<b>Port Tracker Bot</b>\n/list - tracks\n/status - current states\n/logs &lt;track&gt; - last 7 days\n/authme - dashboard login link"
}
