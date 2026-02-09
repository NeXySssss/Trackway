package logstore

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SQLiteOptions struct {
	Path          string
	RetentionDays int
	BusyTimeoutMS int
	MaxOpenConns  int
	MaxIdleConns  int
}

type Store struct {
	backend backend
}

type Target struct {
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Row struct {
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	Endpoint  string `json:"endpoint"`
	Reason    string `json:"reason"`
}

type backend interface {
	append(targetName, address string, port int, status bool, reason string, at time.Time) error
	readSince(targetName string, since time.Time, limit int) []Row
	listTargets() ([]Target, error)
	upsertTarget(target Target) error
	deleteTarget(name string) error
}

func New(_ string) (*Store, error) {
	// Backward-compatible constructor used in tests.
	return NewMemory()
}

func NewMemory() (*Store, error) {
	return &Store{
		backend: &memoryBackend{
			rowsByTrack: make(map[string][]Row),
			targets:     make(map[string]Target),
		},
	}, nil
}

func NewSQLite(options SQLiteOptions) (*Store, error) {
	sqliteBackend, err := newSQLiteBackend(options)
	if err != nil {
		return nil, err
	}
	return &Store{backend: sqliteBackend}, nil
}

func (s *Store) Append(targetName, address string, port int, status bool, reason string) error {
	return s.backend.append(targetName, address, port, status, reason, time.Now().UTC())
}

func (s *Store) ReadLastDays(targetName string, days int, limit int) []Row {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 1000
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	return s.backend.readSince(targetName, cutoff, limit)
}

func (s *Store) ReadLastHours(targetName string, hours int, limit int) []Row {
	if hours <= 0 {
		hours = 24
	}
	if limit <= 0 {
		limit = 1000
	}
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	return s.backend.readSince(targetName, cutoff, limit)
}

func (s *Store) ListTargets() ([]Target, error) {
	return s.backend.listTargets()
}

func (s *Store) UpsertTarget(name, address string, port int) error {
	return s.backend.upsertTarget(Target{
		Name:      strings.TrimSpace(name),
		Address:   strings.TrimSpace(address),
		Port:      port,
		Enabled:   true,
		UpdatedAt: time.Now().UTC(),
	})
}

func (s *Store) DeleteTarget(name string) error {
	return s.backend.deleteTarget(strings.TrimSpace(name))
}

type memoryBackend struct {
	mu          sync.RWMutex
	rowsByTrack map[string][]Row
	targets     map[string]Target
}

func (m *memoryBackend) append(targetName, address string, port int, status bool, reason string, at time.Time) error {
	row := Row{
		Timestamp: at.UTC().Format(time.RFC3339),
		Status:    statusText(status),
		Endpoint:  address + ":" + strconv.Itoa(port),
		Reason:    strings.ToUpper(reason),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.rowsByTrack[targetName] = append(m.rowsByTrack[targetName], row)
	return nil
}

func (m *memoryBackend) readSince(targetName string, since time.Time, limit int) []Row {
	m.mu.RLock()
	rows := append([]Row(nil), m.rowsByTrack[targetName]...)
	m.mu.RUnlock()

	filtered := make([]Row, 0, len(rows))
	for _, row := range rows {
		ts, err := time.Parse(time.RFC3339, row.Timestamp)
		if err != nil {
			continue
		}
		if ts.Before(since) {
			continue
		}
		filtered = append(filtered, row)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp < filtered[j].Timestamp
	})

	if len(filtered) > limit {
		return filtered[len(filtered)-limit:]
	}
	return filtered
}

func (m *memoryBackend) listTargets() ([]Target, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Target, 0, len(m.targets))
	for _, target := range m.targets {
		if !target.Enabled {
			continue
		}
		out = append(out, target)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memoryBackend) upsertTarget(target Target) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	target.Name = strings.TrimSpace(target.Name)
	target.Address = strings.TrimSpace(target.Address)
	target.Enabled = true
	target.UpdatedAt = target.UpdatedAt.UTC()

	m.targets[target.Name] = target
	return nil
}

func (m *memoryBackend) deleteTarget(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, strings.TrimSpace(name))
	return nil
}

func statusText(value bool) string {
	if value {
		return "UP"
	}
	return "DOWN"
}
