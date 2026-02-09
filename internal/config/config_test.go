package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromJSONB64DefaultsToSQLite(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", base64.StdEncoding.EncodeToString([]byte(`{
		"bot": {"token":"x","chat_id":1},
		"monitoring": {"interval_seconds":5, "connect_timeout_seconds":2},
		"storage": {
			"sqlite":{"path":"/tmp/trackway.db"}
		},
		"dashboard": {"enabled":false}
	}`)))

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Fatalf("expected sqlite driver, got %q", cfg.Storage.Driver)
	}
	if cfg.Storage.SQLite.Path != "/tmp/trackway.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Storage.SQLite.Path)
	}
}

func TestLoadRejectsUnsupportedStorageDriver(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON", `{
		"bot":{"token":"x","chat_id":1},
		"monitoring":{"interval_seconds":5,"connect_timeout_seconds":2},
		"storage":{"driver":"legacy"},
		"dashboard":{"enabled":false}
	}`)
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", "")

	_, err := Load(filepath.Join(t.TempDir(), "unused.json"))
	if err == nil {
		t.Fatal("expected unsupported storage driver error")
	}
	if !strings.Contains(err.Error(), "only sqlite is supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadJSONFileWithoutTargetsDefaultsToSQLite(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON", "")
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", "")

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")
	content := `{
		"bot":{"token":"token","chat_id":1},
		"monitoring":{"interval_seconds":5,"connect_timeout_seconds":2},
		"dashboard":{"enabled":false}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Fatalf("expected sqlite driver, got %q", cfg.Storage.Driver)
	}
	if cfg.Storage.SQLite.Path != "trackway.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Storage.SQLite.Path)
	}
	if cfg.Storage.SQLite.RetentionDays != 5 {
		t.Fatalf("unexpected sqlite retention days: %d", cfg.Storage.SQLite.RetentionDays)
	}
	if len(cfg.Targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(cfg.Targets))
	}
}

func TestSQLiteEnvOverrides(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON", `{
		"bot":{"token":"x","chat_id":1},
		"monitoring":{"interval_seconds":5,"connect_timeout_seconds":2},
		"storage":{"driver":"sqlite","sqlite":{"path":"/tmp/from-json.db"}},
		"dashboard":{"enabled":false}
	}`)
	t.Setenv("SQLITE_PATH", "/data/trackway.db")
	t.Setenv("SQLITE_RETENTION_DAYS", "7")
	t.Setenv("SQLITE_BUSY_TIMEOUT_MS", "9000")
	t.Setenv("SQLITE_MAX_OPEN_CONNS", "3")
	t.Setenv("SQLITE_MAX_IDLE_CONNS", "2")

	cfg, err := Load(filepath.Join(t.TempDir(), "unused.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Storage.SQLite.Path != "/data/trackway.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Storage.SQLite.Path)
	}
	if cfg.Storage.SQLite.RetentionDays != 7 {
		t.Fatalf("unexpected sqlite retention: %d", cfg.Storage.SQLite.RetentionDays)
	}
	if cfg.Storage.SQLite.BusyTimeoutMS != 9000 {
		t.Fatalf("unexpected sqlite busy timeout: %d", cfg.Storage.SQLite.BusyTimeoutMS)
	}
	if cfg.Storage.SQLite.MaxOpenConns != 3 {
		t.Fatalf("unexpected sqlite max open conns: %d", cfg.Storage.SQLite.MaxOpenConns)
	}
	if cfg.Storage.SQLite.MaxIdleConns != 2 {
		t.Fatalf("unexpected sqlite max idle conns: %d", cfg.Storage.SQLite.MaxIdleConns)
	}
}

func TestLoadRejectsYAMLFile(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON", "")
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", "")

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "legacy-config.txt")
	content := `
bot:
  token: "token"
  chat_id: 1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected yaml rejection error")
	}
	if !strings.Contains(err.Error(), "YAML is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
