package logstore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	defaultTableName    = "track_logs"
	defaultTargetsTable = "track_targets"
	defaultDialTimeout  = 5 * time.Second
	defaultQueryTimeout = 5 * time.Second
)

type clickHouseBackend struct {
	conn         clickhouse.Conn
	tableName    string
	targetsTable string
}

func newClickHouseBackend(options ClickHouseOptions) (*clickHouseBackend, error) {
	addr := strings.TrimSpace(options.Addr)
	database := strings.TrimSpace(options.Database)
	username := strings.TrimSpace(options.Username)
	tableName := strings.TrimSpace(options.Table)

	if addr == "" {
		return nil, errors.New("clickhouse addr is required")
	}
	if database == "" {
		return nil, errors.New("clickhouse database is required")
	}
	if username == "" {
		username = "default"
	}
	if tableName == "" {
		tableName = defaultTableName
	}

	dialTimeout := options.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}

	maxOpen := options.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := options.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}

	var tlsConfig *tls.Config
	if options.Secure {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: username,
			Password: options.Password,
		},
		DialTimeout:      dialTimeout,
		MaxOpenConns:     maxOpen,
		MaxIdleConns:     maxIdle,
		ConnOpenStrategy: clickhouse.ConnOpenInOrder,
		TLS:              tlsConfig,
	})
	if err != nil {
		return nil, err
	}

	dbName := sanitizeIdentifier(database)
	if dbName == "" {
		return nil, errors.New("clickhouse database contains unsupported characters")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	if err := conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+dbName); err != nil {
		cancel()
		return nil, fmt.Errorf("create clickhouse database: %w", err)
	}
	cancel()

	conn, err = clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: database,
			Username: username,
			Password: options.Password,
		},
		DialTimeout:      dialTimeout,
		MaxOpenConns:     maxOpen,
		MaxIdleConns:     maxIdle,
		ConnOpenStrategy: clickhouse.ConnOpenInOrder,
		TLS:              tlsConfig,
		Settings: clickhouse.Settings{
			"async_insert":          1,
			"wait_for_async_insert": 1,
		},
	})
	if err != nil {
		return nil, err
	}

	table := sanitizeIdentifier(tableName)
	if table == "" {
		return nil, errors.New("clickhouse table contains unsupported characters")
	}

	backend := &clickHouseBackend{
		conn:         conn,
		tableName:    table,
		targetsTable: defaultTargetsTable,
	}

	if err := backend.initSchema(); err != nil {
		return nil, err
	}
	return backend, nil
}

func (c *clickHouseBackend) initSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	logsQuery := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	ts DateTime64(3, 'UTC'),
	target String,
	address String,
	port UInt16,
	status LowCardinality(String),
	reason LowCardinality(String)
) ENGINE = MergeTree()
ORDER BY (target, ts)
`, c.tableName)
	if err := c.conn.Exec(ctx, logsQuery); err != nil {
		return fmt.Errorf("create clickhouse table: %w", err)
	}

	targetsQuery := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	name String,
	address String,
	port UInt16,
	enabled UInt8,
	updated_at DateTime64(3, 'UTC')
) ENGINE = MergeTree()
ORDER BY (name, updated_at)
`, c.targetsTable)
	if err := c.conn.Exec(ctx, targetsQuery); err != nil {
		return fmt.Errorf("create clickhouse targets table: %w", err)
	}

	return nil
}

func (c *clickHouseBackend) append(targetName, address string, port int, status bool, reason string, at time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	query := fmt.Sprintf(
		"INSERT INTO %s (ts, target, address, port, status, reason) VALUES (?, ?, ?, ?, ?, ?)",
		c.tableName,
	)
	return c.conn.Exec(
		ctx,
		query,
		at.UTC(),
		targetName,
		address,
		uint16(port),
		statusText(status),
		strings.ToUpper(reason),
	)
}

func (c *clickHouseBackend) readSince(targetName string, since time.Time, limit int) []Row {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := fmt.Sprintf(
		"SELECT ts, status, address, port, reason FROM %s WHERE target = ? AND ts >= ? ORDER BY ts DESC LIMIT ?",
		c.tableName,
	)

	rows, err := c.conn.Query(ctx, query, targetName, since.UTC(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	result := make([]Row, 0, limit)
	for rows.Next() {
		var (
			ts      time.Time
			status  string
			address string
			port    uint16
			reason  string
		)
		if err := rows.Scan(&ts, &status, &address, &port, &reason); err != nil {
			continue
		}
		result = append(result, Row{
			Timestamp: ts.UTC().Format(time.RFC3339),
			Status:    strings.ToUpper(status),
			Endpoint:  fmt.Sprintf("%s:%d", address, port),
			Reason:    strings.ToUpper(reason),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp < result[j].Timestamp })
	return result
}

func (c *clickHouseBackend) listTargets() ([]Target, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT
	name,
	argMax(address, updated_at) AS address,
	argMax(port, updated_at) AS port,
	argMax(enabled, updated_at) AS enabled
FROM %s
GROUP BY name
ORDER BY name`,
		c.targetsTable,
	)

	rows, err := c.conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Target, 0, 32)
	for rows.Next() {
		var (
			name    string
			address string
			port    uint16
			enabled uint8
		)
		if err := rows.Scan(&name, &address, &port, &enabled); err != nil {
			return nil, err
		}
		if enabled != 1 {
			continue
		}
		result = append(result, Target{
			Name:      name,
			Address:   address,
			Port:      int(port),
			Enabled:   enabled == 1,
			UpdatedAt: time.Time{},
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *clickHouseBackend) upsertTarget(target Target) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	query := fmt.Sprintf(
		"INSERT INTO %s (name, address, port, enabled, updated_at) VALUES (?, ?, ?, ?, ?)",
		c.targetsTable,
	)
	return c.conn.Exec(
		ctx,
		query,
		target.Name,
		target.Address,
		uint16(target.Port),
		uint8(1),
		target.UpdatedAt.UTC(),
	)
}

func (c *clickHouseBackend) deleteTarget(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	query := fmt.Sprintf(
		"INSERT INTO %s (name, address, port, enabled, updated_at) VALUES (?, ?, ?, ?, ?)",
		c.targetsTable,
	)
	return c.conn.Exec(
		ctx,
		query,
		name,
		"",
		uint16(0),
		uint8(0),
		time.Now().UTC(),
	)
}

func sanitizeIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return ""
	}
	return trimmed
}
