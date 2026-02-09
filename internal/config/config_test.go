package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromJSONB64AndEnvOverride(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", base64.StdEncoding.EncodeToString([]byte(`{
		"bot": {"token":"x","chat_id":1},
		"monitoring": {"interval_seconds":5, "connect_timeout_seconds":2},
		"storage": {"clickhouse":{"addr":"clickhouse:9000","database":"trackway","username":"default","password":"from-json","table":"track_logs"}},
		"dashboard": {"enabled":false}
	}`)))
	t.Setenv("CLICKHOUSE_PASSWORD", "from-env")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Storage.ClickHouse.Password != "from-env" {
		t.Fatalf("expected env override password, got %q", cfg.Storage.ClickHouse.Password)
	}
	if cfg.Storage.ClickHouse.Addr != "clickhouse:9000" {
		t.Fatalf("unexpected clickhouse addr: %q", cfg.Storage.ClickHouse.Addr)
	}
}

func TestLoadYAMLWithoutTargets(t *testing.T) {
	t.Setenv("TRACKWAY_CONFIG_JSON", "")
	t.Setenv("TRACKWAY_CONFIG_JSON_B64", "")

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yaml")
	content := `
bot:
  token: "token"
  chat_id: 1
monitoring:
  interval_seconds: 5
  connect_timeout_seconds: 2
storage:
  clickhouse:
    addr: "clickhouse:9000"
    database: "trackway"
dashboard:
  enabled: false
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Targets) != 0 {
		t.Fatalf("expected zero targets, got %d", len(cfg.Targets))
	}
}
