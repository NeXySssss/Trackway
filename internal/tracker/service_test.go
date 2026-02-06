package tracker

import (
	"context"
	"strings"
	"sync"
	"testing"

	"trackway/internal/config"
	"trackway/internal/logstore"
)

type fakeNotifier struct {
	mu       sync.Mutex
	defaults []string
	replies  []string
}

func (f *fakeNotifier) SendDefaultHTML(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaults = append(f.defaults, text)
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
	svc.applyStatus(ctx, target, false) // init down => alert
	svc.applyStatus(ctx, target, false) // unchanged => no alert
	svc.applyStatus(ctx, target, true)  // recovered => alert

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
	if len(rows) != 2 {
		t.Fatalf("expected 2 log rows (INIT+CHANGE), got %d", len(rows))
	}
	if rows[0].Reason != "INIT" || rows[1].Reason != "CHANGE" {
		t.Fatalf("unexpected reasons: %+v", rows)
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
