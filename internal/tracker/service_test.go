package tracker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"trackway/internal/config"
	"trackway/internal/logstore"
)

type fakeNotifier struct {
	mu       sync.Mutex
	defaults []string
	replies  []string
	edits    []string
}

func (f *fakeNotifier) SendDefaultHTML(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaults = append(f.defaults, text)
	return nil
}

func (f *fakeNotifier) SendDefaultHTMLWithID(_ context.Context, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaults = append(f.defaults, text)
	return 100 + len(f.defaults), nil
}

func (f *fakeNotifier) EditDefaultHTML(_ context.Context, _ int, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, text)
	return nil
}

func (f *fakeNotifier) SendHTML(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, text)
	return nil
}

func TestParseCommand(t *testing.T) {
	cmd, arg, ok := parseCommand("/logs@mybot mini-srv")
	if !ok {
		t.Fatal("expected command to be parsed")
	}
	if cmd != "logs" || arg != "mini-srv" {
		t.Fatalf("unexpected command parse result: cmd=%q arg=%q", cmd, arg)
	}
}

func TestApplyStatusTransitions(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	notifier := &fakeNotifier{}
	svc := New(testConfig(), store, notifier)
	target := svc.targets[0]

	ctx := context.Background()
	var events []alertEvent
	if ev := svc.applyStatus(target, false); ev != nil {
		events = append(events, *ev)
	}
	if ev := svc.applyStatus(target, false); ev != nil {
		events = append(events, *ev)
	}
	if ev := svc.applyStatus(target, true); ev != nil {
		events = append(events, *ev)
	}
	svc.sendAlertBatch(ctx, events)

	if len(notifier.defaults) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(notifier.defaults))
	}
	if !strings.Contains(notifier.defaults[0], "DOWN") {
		t.Fatalf("expected first alert to contain DOWN: %q", notifier.defaults[0])
	}
	if !strings.Contains(notifier.defaults[1], "RECOVERED") {
		t.Fatalf("expected second alert to contain RECOVERED: %q", notifier.defaults[1])
	}

	rows := store.ReadLastDays(target.Name, 7, 100)
	if len(rows) != 3 {
		t.Fatalf("expected 3 log rows (INIT+POLL+CHANGE), got %d", len(rows))
	}
	if rows[0].Reason != "INIT" || rows[1].Reason != "POLL" || rows[2].Reason != "CHANGE" {
		t.Fatalf("unexpected reasons: %+v", rows)
	}
}

func TestSendAlertBatchCombinesSameKind(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	notifier := &fakeNotifier{}
	svc := New(testConfig(), store, notifier)

	now := time.Now().UTC()
	events := []alertEvent{
		{Kind: "DOWN", Target: "a", Address: "10.0.0.1", Port: 80, Reason: "state-change", Occurred: now},
		{Kind: "DOWN", Target: "b", Address: "10.0.0.2", Port: 443, Reason: "state-change", Occurred: now},
		{Kind: "DOWN", Target: "c", Address: "10.0.0.3", Port: 22, Reason: "state-change", Occurred: now},
	}

	svc.sendAlertBatch(context.Background(), events)

	if len(notifier.defaults) != 1 {
		t.Fatalf("expected one grouped alert, got %d", len(notifier.defaults))
	}
	got := notifier.defaults[0]
	if !strings.Contains(got, "DOWN x3") {
		t.Fatalf("expected grouped counter in message: %q", got)
	}
	if !strings.Contains(got, "targets:") || !strings.Contains(got, "<code>a</code>") {
		t.Fatalf("expected grouped target list in message: %q", got)
	}
}

func TestFastRecoveryEditsDownMessage(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	notifier := &fakeNotifier{}
	svc := New(testConfig(), store, notifier)

	downTime := time.Now().UTC()
	recoveredTime := downTime.Add(5 * time.Second)
	events := []alertEvent{
		{
			Kind:     "DOWN",
			Target:   "test-track",
			Address:  "127.0.0.1",
			Port:     1,
			Reason:   "state-change",
			Occurred: downTime,
		},
	}
	svc.sendAlertBatch(context.Background(), events)
	if len(notifier.defaults) != 1 {
		t.Fatalf("expected one DOWN message, got %d", len(notifier.defaults))
	}

	events = []alertEvent{
		{
			Kind:     "RECOVERED",
			Target:   "test-track",
			Address:  "127.0.0.1",
			Port:     1,
			Reason:   "state-change",
			Occurred: recoveredTime,
		},
	}
	svc.sendAlertBatch(context.Background(), events)

	if len(notifier.edits) != 1 {
		t.Fatalf("expected edit message, got %d", len(notifier.edits))
	}
	if strings.Contains(notifier.edits[0], "downtime: <code>5s</code>") == false {
		t.Fatalf("expected downtime in edit message, got: %q", notifier.edits[0])
	}
	if len(notifier.defaults) != 1 {
		t.Fatalf("expected no extra RECOVERED message, defaults=%d", len(notifier.defaults))
	}
}

func TestFastRecoveryGroupEditsDownMessage(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	notifier := &fakeNotifier{}
	svc := New(testConfig(), store, notifier)

	downTime := time.Now().UTC()
	recoveredTime := downTime.Add(4 * time.Second)
	group := []alertEvent{
		{Kind: "DOWN", Target: "a", Address: "10.0.0.1", Port: 80, Reason: "state-change", Occurred: downTime},
		{Kind: "DOWN", Target: "b", Address: "10.0.0.2", Port: 443, Reason: "state-change", Occurred: downTime},
		{Kind: "DOWN", Target: "c", Address: "10.0.0.3", Port: 22, Reason: "state-change", Occurred: downTime},
	}
	svc.sendAlertBatch(context.Background(), group)
	if len(notifier.defaults) != 1 {
		t.Fatalf("expected one grouped DOWN, got %d", len(notifier.defaults))
	}

	recovered := []alertEvent{
		{Kind: "RECOVERED", Target: "a", Address: "10.0.0.1", Port: 80, Reason: "state-change", Occurred: recoveredTime},
		{Kind: "RECOVERED", Target: "b", Address: "10.0.0.2", Port: 443, Reason: "state-change", Occurred: recoveredTime},
		{Kind: "RECOVERED", Target: "c", Address: "10.0.0.3", Port: 22, Reason: "state-change", Occurred: recoveredTime},
	}
	svc.sendAlertBatch(context.Background(), recovered)

	if len(notifier.edits) != 1 {
		t.Fatalf("expected one grouped edit, got %d", len(notifier.edits))
	}
	got := notifier.edits[0]
	if !strings.Contains(got, "DOWN -> RECOVERED x3") {
		t.Fatalf("expected grouped edit header, got %q", got)
	}
	if strings.Contains(got, "downtime: <code>4s</code>") == false {
		t.Fatalf("expected downtime 4s in edit, got %q", got)
	}
	if len(notifier.defaults) != 1 {
		t.Fatalf("expected no extra RECOVERED messages, defaults=%d", len(notifier.defaults))
	}
}

func TestLogsMessagesChunking(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	svc := New(testConfig(), store, &fakeNotifier{})
	target := svc.targets[0]

	for i := 0; i < 260; i++ {
		status := (i%2 == 0)
		reason := "CHANGE"
		if i == 0 {
			reason = "INIT"
		}
		if err := store.Append(target.Name, target.Address, target.Port, status, reason); err != nil {
			t.Fatalf("append error: %v", err)
		}
	}

	messages := svc.logsMessages(target.Name)
	if len(messages) < 2 {
		t.Fatalf("expected chunked log response, got %d message(s)", len(messages))
	}
	for i, msg := range messages {
		if len(msg) > 4000 {
			t.Fatalf("message %d is too long: %d chars", i, len(msg))
		}
		if !strings.Contains(msg, "<pre>") {
			t.Fatalf("message %d must contain <pre> block", i)
		}
	}
}

func TestAuthLinkText(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	svc := New(testConfig(), store, &fakeNotifier{})
	svc.SetAuthLinkGenerator(func() (string, error) {
		return "https://example.com/auth/verify?token=abc", nil
	})

	text := svc.authLinkText(1)
	if !strings.Contains(text, "https://example.com/auth/verify?token=abc") {
		t.Fatalf("expected auth link in response, got %q", text)
	}
}

func TestAuthLinkTextChatRestricted(t *testing.T) {
	t.Parallel()

	store, err := logstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("logstore init error: %v", err)
	}
	cfg := testConfig()
	cfg.Bot.ChatID = 100
	svc := New(cfg, store, &fakeNotifier{})
	svc.SetAuthLinkGenerator(func() (string, error) {
		return "https://example.com/auth/verify?token=abc", nil
	})

	text := svc.authLinkText(200)
	if !strings.Contains(strings.ToLower(text), "not available") {
		t.Fatalf("expected restricted chat response, got %q", text)
	}
}

func testConfig() config.Config {
	var cfg config.Config
	cfg.Bot.Token = "token"
	cfg.Bot.ChatID = 1
	cfg.Monitoring.IntervalSeconds = 1
	cfg.Monitoring.ConnectTimeoutSeconds = 1
	cfg.Targets = []config.Target{
		{
			Name:    "test-track",
			Address: "127.0.0.1",
			Port:    1,
		},
	}
	return cfg
}
