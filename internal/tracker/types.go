package tracker

import (
	"context"
	"time"
)

type Notifier interface {
	SendDefaultHTML(ctx context.Context, text string) error
	SendDefaultHTMLWithID(ctx context.Context, text string) (int, error)
	EditDefaultHTML(ctx context.Context, messageID int, text string) error
	SendHTML(ctx context.Context, chatID int64, text string) error
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

type pendingDownGroup struct {
	MessageID int
	Reason    string
	DownAt    time.Time
	Targets   map[string]alertEvent
}

type Snapshot struct {
	GeneratedAt time.Time
	Total       int
	Up          int
	Down        int
	Unknown     int
	Targets     []TargetSnapshot
}

type TargetSnapshot struct {
	Name        string
	Address     string
	Port        int
	Status      string
	LastChanged time.Time
	LastChecked time.Time
}

func boolPtr(value bool) *bool {
	return &value
}
