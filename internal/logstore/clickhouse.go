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
	defaultDialTimeout  = 5 * time.Second
	defaultQueryTimeout = 5 * time.Second
)

type clickHouseBackend struct {
	conn      clickhouse.Conn
	tableName string
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
		conn:      conn,
		tableName: table,
	}

	if err := backend.initSchema(); err != nil {
		return nil, err
	}
	return backend, nil
}

func (c *clickHouseBackend) initSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultQueryTimeout)
	defer cancel()

	query := fmt.Sprintf(`
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
	if err := c.conn.Exec(ctx, query); err != nil {
		return fmt.Errorf("create clickhouse table: %w", err)
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
