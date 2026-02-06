package logstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trackway/internal/util"
)

type Store struct {
	dir string
}

type Row struct {
	Timestamp string
	Status    string
	Endpoint  string
	Reason    string
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Append(targetName, address string, port int, status bool, reason string) error {
	path := s.pathFor(targetName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	line := fmt.Sprintf(
		"%s\t%s\t%s:%d\t%s\n",
		time.Now().UTC().Format(time.RFC3339),
		statusText(status),
		address,
		port,
		reason,
	)
	_, err = file.WriteString(line)
	return err
}

func (s *Store) ReadLastDays(targetName string, days int, limit int) []Row {
	path := s.pathFor(targetName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	rows := make([]Row, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, parts[0])
		if err != nil || ts.Before(cutoff) {
			continue
		}
		rows = append(rows, Row{
			Timestamp: parts[0],
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

func (s *Store) pathFor(targetName string) string {
	return filepath.Join(s.dir, util.SafeName(targetName)+".log")
}

func statusText(value bool) string {
	if value {
		return "UP"
	}
	return "DOWN"
}
