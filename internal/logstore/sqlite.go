package logstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultSQLiteRetentionDays = 5
	defaultSQLiteBusyTimeoutMS = 5000
	defaultSQLiteMaxOpenConns  = 1
	defaultSQLiteMaxIdleConns  = 1
	sqliteCleanupEveryWrites   = 100
)

type sqliteBackend struct {
	db            *sql.DB
	retentionDays int
	writeCount    atomic.Uint64
}

func newSQLiteBackend(options SQLiteOptions) (*sqliteBackend, error) {
	path := strings.TrimSpace(options.Path)
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	maxOpen := options.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = defaultSQLiteMaxOpenConns
	}
	maxIdle := options.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = defaultSQLiteMaxIdleConns
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(0)

	busyTimeout := options.BusyTimeoutMS
	if busyTimeout <= 0 {
		busyTimeout = defaultSQLiteBusyTimeoutMS
	}

	retentionDays := options.RetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultSQLiteRetentionDays
	}

	if err := applySQLitePragmas(db, busyTimeout); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := initSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	backend := &sqliteBackend{
		db:            db,
		retentionDays: retentionDays,
	}
	if err := backend.cleanupOldLogs(time.Now().UTC()); err != nil {
		// cleanup is best effort; keep startup resilient
	}
	return backend, nil
}

func applySQLitePragmas(db *sql.DB, busyTimeoutMS int) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA temp_store = FILE",
		"PRAGMA mmap_size = 0",
		"PRAGMA cache_size = -2048",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = " + strconv.Itoa(busyTimeoutMS),
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", pragma, err)
		}
	}
	return nil
}

func initSQLiteSchema(db *sql.DB) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			target TEXT NOT NULL,
			address TEXT NOT NULL,
			port INTEGER NOT NULL,
			status TEXT NOT NULL,
			reason TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_target_ts ON logs(target, ts)`,
		`CREATE TABLE IF NOT EXISTS targets (
			name TEXT PRIMARY KEY,
			address TEXT NOT NULL,
			port INTEGER NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, query := range schema {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("init sqlite schema: %w", err)
		}
	}
	return nil
}

func (s *sqliteBackend) append(targetName, address string, port int, status bool, reason string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO logs (ts, target, address, port, status, reason) VALUES (?, ?, ?, ?, ?, ?)`,
		at.UTC().Format(time.RFC3339Nano),
		targetName,
		address,
		port,
		statusText(status),
		strings.ToUpper(reason),
	)
	if err != nil {
		return err
	}

	if s.writeCount.Add(1)%sqliteCleanupEveryWrites == 0 {
		_ = s.cleanupOldLogs(time.Now().UTC())
	}
	return nil
}

func (s *sqliteBackend) readSince(targetName string, since time.Time, limit int) []Row {
	rows, err := s.db.Query(
		`SELECT ts, status, address, port, reason
		FROM logs
		WHERE target = ? AND ts >= ?
		ORDER BY ts ASC
		LIMIT ?`,
		targetName,
		since.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make([]Row, 0, limit)
	for rows.Next() {
		var (
			ts      string
			status  string
			address string
			port    int
			reason  string
		)
		if err := rows.Scan(&ts, &status, &address, &port, &reason); err != nil {
			continue
		}
		result = append(result, Row{
			Timestamp: ts,
			Status:    strings.ToUpper(status),
			Endpoint:  fmt.Sprintf("%s:%d", address, port),
			Reason:    strings.ToUpper(reason),
		})
	}
	return result
}

func (s *sqliteBackend) listTargets() ([]Target, error) {
	rows, err := s.db.Query(
		`SELECT name, address, port, enabled, updated_at
		FROM targets
		WHERE enabled = 1
		ORDER BY name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Target, 0, 64)
	for rows.Next() {
		var (
			target    Target
			enabled   int
			updatedAt string
		)
		if err := rows.Scan(&target.Name, &target.Address, &target.Port, &enabled, &updatedAt); err != nil {
			return nil, err
		}
		target.Enabled = enabled == 1
		parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
		if err == nil {
			target.UpdatedAt = parsed.UTC()
		}
		result = append(result, target)
	}
	return result, nil
}

func (s *sqliteBackend) upsertTarget(target Target) error {
	updatedAt := target.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT INTO targets (name, address, port, enabled, updated_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(name) DO UPDATE SET
			address = excluded.address,
			port = excluded.port,
			enabled = 1,
			updated_at = excluded.updated_at`,
		target.Name,
		target.Address,
		target.Port,
		updatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *sqliteBackend) deleteTarget(name string) error {
	_, err := s.db.Exec(
		`UPDATE targets SET enabled = 0, updated_at = ? WHERE name = ?`,
		time.Now().UTC().Format(time.RFC3339Nano),
		name,
	)
	return err
}

func (s *sqliteBackend) cleanupOldLogs(now time.Time) error {
	if s.retentionDays <= 0 {
		return nil
	}
	cutoff := now.UTC().Add(-time.Duration(s.retentionDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	_, err := s.db.Exec(`DELETE FROM logs WHERE ts < ?`, cutoff)
	return err
}
