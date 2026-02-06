package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gopkg.in/yaml.v3"
)

const telegramMaxText = 4000

type Config struct {
	Bot struct {
		Token  string `yaml:"token"`
		ChatID int64  `yaml:"chat_id"`
	} `yaml:"bot"`
	Monitoring struct {
		IntervalSeconds       int `yaml:"interval_seconds"`
		ConnectTimeoutSeconds int `yaml:"connect_timeout_seconds"`
	} `yaml:"monitoring"`
	Storage struct {
		LogDir string `yaml:"log_dir"`
	} `yaml:"storage"`
	Targets []TargetConfig `yaml:"targets"`
}

type TargetConfig struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type Target struct {
	Name        string
	Address     string
	Port        int
	LastStatus  *bool
	LastChanged time.Time
	LastChecked time.Time
}

type Monitor struct {
	bot      *tgbot.Bot
	chatID   int64
	logDir   string
	interval time.Duration
	timeout  time.Duration

	targets      []*Target
	targetByName map[string]*Target

	mu sync.RWMutex
}

type LogRow struct {
	Timestamp string
	Status    string
	Endpoint  string
	Reason    string
}

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}

	logDir := strings.TrimSpace(os.Getenv("LOG_DIR"))
	if logDir == "" {
		logDir = strings.TrimSpace(cfg.Storage.LogDir)
	}
	if logDir == "" {
		logDir = "logs"
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Println("log dir error:", err)
		os.Exit(1)
	}

	monitor := &Monitor{
		chatID:       cfg.Bot.ChatID,
		logDir:       logDir,
		interval:     defaultSeconds(cfg.Monitoring.IntervalSeconds, 5),
		timeout:      defaultSeconds(cfg.Monitoring.ConnectTimeoutSeconds, 2),
		targets:      buildTargets(cfg.Targets),
		targetByName: map[string]*Target{},
	}
	for _, t := range monitor.targets {
		monitor.targetByName[t.Name] = t
	}

	b, err := tgbot.New(
		cfg.Bot.Token,
		tgbot.WithDefaultHandler(monitor.handleUpdate),
		tgbot.WithNotAsyncHandlers(),
	)
	if err != nil {
		fmt.Println("bot init error:", err)
		os.Exit(1)
	}
	monitor.bot = b

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go monitor.monitorLoop(ctx)

	_ = monitor.sendHTML(ctx, "<b>INFO</b>\nport tracker started (Go)")
	b.Start(ctx)
	_ = monitor.sendHTML(context.Background(), "<b>INFO</b>\nport tracker stopped")
}

func defaultSeconds(value int, fallback int) time.Duration {
	if value <= 0 {
		value = fallback
	}
	return time.Duration(value) * time.Second
}

func buildTargets(items []TargetConfig) []*Target {
	out := make([]*Target, 0, len(items))
	for _, item := range items {
		out = append(out, &Target{
			Name:    strings.TrimSpace(item.Name),
			Address: strings.TrimSpace(item.Address),
			Port:    item.Port,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Bot.Token == "" || cfg.Bot.ChatID == 0 {
		return cfg, errors.New("bot.token and bot.chat_id are required")
	}
	if len(cfg.Targets) == 0 {
		return cfg, errors.New("targets list is empty")
	}
	return cfg, nil
}

func (m *Monitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runChecks(ctx)
		}
	}
}

func (m *Monitor) runChecks(ctx context.Context) {
	for _, target := range m.targets {
		status := checkTCP(target.Address, target.Port, m.timeout)
		m.applyStatus(ctx, target, status)
	}
}

func (m *Monitor) applyStatus(ctx context.Context, target *Target, status bool) {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	target.LastChecked = now
	if target.LastStatus == nil {
		target.LastStatus = boolPtr(status)
		target.LastChanged = now
		m.appendLog(target, status, "INIT")
		if !status {
			_ = m.sendAlert(ctx, "DOWN", target, "initial-check")
		}
		return
	}

	if *target.LastStatus != status {
		prev := *target.LastStatus
		*target.LastStatus = status
		target.LastChanged = now
		m.appendLog(target, status, "CHANGE")
		if prev && !status {
			_ = m.sendAlert(ctx, "DOWN", target, "state-change")
		} else if !prev && status {
			_ = m.sendAlert(ctx, "RECOVERED", target, "state-change")
		}
	}
}

func (m *Monitor) sendAlert(ctx context.Context, kind string, t *Target, reason string) error {
	text := fmt.Sprintf(
		"<b>%s</b>\ntrack: <code>%s</code>\nendpoint: <code>%s:%d</code>\nreason: <code>%s</code>\ntime_utc: <code>%s</code>",
		htmlEscape(kind),
		htmlEscape(t.Name),
		htmlEscape(t.Address), t.Port,
		htmlEscape(reason),
		time.Now().UTC().Format(time.RFC3339),
	)
	return m.sendHTML(ctx, text)
}

func (m *Monitor) sendHTML(ctx context.Context, text string) error {
	chunks := splitByLimit(text, telegramMaxText)
	for _, chunk := range chunks {
		_, err := m.bot.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:    m.chatID,
			Text:      chunk,
			ParseMode: models.ParseModeHTML,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Monitor) appendLog(t *Target, status bool, reason string) {
	line := fmt.Sprintf(
		"%s\t%s\t%s:%d\t%s\n",
		time.Now().UTC().Format(time.RFC3339),
		statusText(status),
		t.Address,
		t.Port,
		reason,
	)
	path := filepath.Join(m.logDir, safeName(t.Name)+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}

func (m *Monitor) handleUpdate(ctx context.Context, b *tgbot.Bot, upd *models.Update) {
	msg := upd.Message
	if msg == nil || msg.Text == "" {
		return
	}
	cmd, arg, ok := parseCommand(msg.Text)
	if !ok {
		return
	}

	var response string
	switch cmd {
	case "start", "help":
		response = helpText()
	case "list":
		response = m.listText()
	case "status":
		response = m.statusText()
	case "logs":
		if arg == "" {
			response = "Usage: /logs &lt;track_name&gt;"
		} else {
			response = m.logsText(arg)
		}
	default:
		return
	}

	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:    msg.Chat.ID,
		Text:      response,
		ParseMode: models.ParseModeHTML,
	})
}

func parseCommand(text string) (string, string, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" || raw[0] != '/' {
		return "", "", false
	}
	parts := strings.Fields(raw)
	cmd := strings.TrimPrefix(parts[0], "/")
	if idx := strings.Index(cmd, "@"); idx > 0 {
		cmd = cmd[:idx]
	}
	if cmd == "" {
		return "", "", false
	}
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}
	return strings.ToLower(cmd), arg, true
}

func (m *Monitor) listText() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.targets) == 0 {
		return "No tracks configured."
	}
	var sb strings.Builder
	sb.WriteString("<b>Configured tracks</b>\n")
	for i, t := range m.targets {
		fmt.Fprintf(&sb, "%d. <b>%s</b> - <code>%s:%d</code>\n", i+1, htmlEscape(t.Name), htmlEscape(t.Address), t.Port)
	}
	return sb.String()
}

func (m *Monitor) statusText() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.targets) == 0 {
		return "No tracks configured."
	}

	up, down, unknown := 0, 0, 0
	for _, t := range m.targets {
		if t.LastStatus == nil {
			unknown++
		} else if *t.LastStatus {
			up++
		} else {
			down++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>Status snapshot (UTC)</b>\ntracks: %d | up: %d | down: %d | unknown: %d\n\n", len(m.targets), up, down, unknown)
	for i, t := range m.targets {
		state := "UNKNOWN"
		if t.LastStatus != nil {
			if *t.LastStatus {
				state = "UP"
			} else {
				state = "DOWN"
			}
		}
		fmt.Fprintf(
			&sb,
			"%d. <b>%s</b>\nendpoint: <code>%s:%d</code>\nstate: <b>%s</b>\nchanged: <code>%s</code>\nchecked: <code>%s</code>\n\n",
			i+1,
			htmlEscape(t.Name),
			htmlEscape(t.Address),
			t.Port,
			state,
			formatTime(t.LastChanged),
			formatTime(t.LastChecked),
		)
	}
	return sb.String()
}

func (m *Monitor) logsText(trackName string) string {
	m.mu.RLock()
	target := m.targetByName[trackName]
	m.mu.RUnlock()
	if target == nil {
		return "Track not found. Use /list."
	}

	rows := readLogsLast7Days(filepath.Join(m.logDir, safeName(target.Name)+".log"), 120)
	if len(rows) == 0 {
		return "No log rows for last 7 days."
	}

	upCnt, downCnt := 0, 0
	for _, row := range rows {
		switch row.Status {
		case "UP":
			upCnt++
		case "DOWN":
			downCnt++
		}
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Track: <b>%s</b> | rows: %d | up: %d | down: %d\n", htmlEscape(target.Name), len(rows), upCnt, downCnt)
	body.WriteString("<pre>")
	body.WriteString(htmlEscape(formatLogRows(rows)))
	body.WriteString("</pre>")
	return body.String()
}

func readLogsLast7Days(path string, limit int) []LogRow {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)

	rows := make([]LogRow, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		timestamp := parts[0]
		ts, err := time.Parse(time.RFC3339, timestamp)
		if err != nil || ts.Before(cutoff) {
			continue
		}
		rows = append(rows, LogRow{
			Timestamp: timestamp,
			Status:    strings.ToUpper(parts[1]),
			Endpoint:  parts[2],
			Reason:    strings.ToUpper(parts[3]),
		})
	}

	if len(rows) > limit {
		return rows[len(rows)-limit:]
	}
	return rows
}

func formatLogRows(rows []LogRow) string {
	var out strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&out, "%s  %-4s  %-21s  %s\n", row.Timestamp, row.Status, row.Endpoint, row.Reason)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func checkTCP(address string, port int, timeout time.Duration) bool {
	target := net.JoinHostPort(address, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func splitByLimit(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	chunks := make([]string, 0, len(text)/maxLen+1)
	for len(text) > maxLen {
		chunks = append(chunks, text[:maxLen])
		text = text[maxLen:]
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func statusText(value bool) string {
	if value {
		return "UP"
	}
	return "DOWN"
}

func safeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func htmlEscape(input string) string {
	result := strings.ReplaceAll(input, "&", "&amp;")
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")
	return result
}

func boolPtr(value bool) *bool {
	return &value
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func helpText() string {
	return "<b>Port Tracker Bot</b>\n/list - tracks\n/status - current states\n/logs &lt;track&gt; - last 7 days"
}
