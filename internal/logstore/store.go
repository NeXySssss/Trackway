package logstore

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ClickHouseOptions struct {
	Addr         string
	Database     string
	Username     string
	Password     string
	Table        string
	Secure       bool
	DialTimeout  time.Duration
	MaxOpenConns int
	MaxIdleConns int
}

type Store struct {
	backend backend
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
}

func New(_ string) (*Store, error) {
	// Backward-compatible constructor used in tests.
	return NewMemory()
}

func NewMemory() (*Store, error) {
	return &Store{
		backend: &memoryBackend{
			rowsByTrack: make(map[string][]Row),
		},
	}, nil
}

func NewClickHouse(options ClickHouseOptions) (*Store, error) {
	ch, err := newClickHouseBackend(options)
	if err != nil {
		return nil, err
	}
	return &Store{backend: ch}, nil
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

type memoryBackend struct {
	mu          sync.RWMutex
	rowsByTrack map[string][]Row
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

func statusText(value bool) string {
	if value {
		return "UP"
	}
	return "DOWN"
}
