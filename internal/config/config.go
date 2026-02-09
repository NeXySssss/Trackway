package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultStorageDriver      = "sqlite"
	defaultSQLitePath         = "trackway.db"
	defaultSQLiteRetentionDay = 5
	defaultSQLiteBusyTimeout  = 5000
	defaultSQLiteMaxOpenConns = 1
	defaultSQLiteMaxIdleConns = 1
)

type Config struct {
	Bot struct {
		Token  string `json:"token"`
		ChatID int64  `json:"chat_id"`
	} `json:"bot"`
	Monitoring struct {
		IntervalSeconds       int `json:"interval_seconds"`
		ConnectTimeoutSeconds int `json:"connect_timeout_seconds"`
		MaxParallelChecks     int `json:"max_parallel_checks"`
	} `json:"monitoring"`
	Storage   Storage   `json:"storage"`
	Dashboard Dashboard `json:"dashboard"`
	Targets   []Target  `json:"targets"`
}

type Storage struct {
	Driver string `json:"driver"`
	SQLite SQLite `json:"sqlite"`
}

type SQLite struct {
	Path          string `json:"path"`
	RetentionDays int    `json:"retention_days"`
	BusyTimeoutMS int    `json:"busy_timeout_ms"`
	MaxOpenConns  int    `json:"max_open_conns"`
	MaxIdleConns  int    `json:"max_idle_conns"`
}

type Target struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type Dashboard struct {
	Enabled             bool   `json:"enabled"`
	ListenAddress       string `json:"listen_address"`
	PublicURL           string `json:"public_url"`
	AuthTokenTTLSeconds int    `json:"auth_token_ttl_seconds"`
	SecureCookie        bool   `json:"secure_cookie"`
	MiniAppEnabled      bool   `json:"mini_app_enabled"`
	MiniAppMaxAgeSec    int    `json:"mini_app_max_age_seconds"`
}

func Load(path string) (Config, error) {
	var cfg Config

	if err := loadInto(&cfg, path); err != nil {
		return cfg, err
	}
	if err := applyStorageEnvOverrides(&cfg); err != nil {
		return cfg, err
	}

	if cfg.Bot.Token == "" || cfg.Bot.ChatID == 0 {
		return cfg, errors.New("bot.token and bot.chat_id are required")
	}
	seenTargets := make(map[string]struct{}, len(cfg.Targets))
	for i := range cfg.Targets {
		cfg.Targets[i].Name = strings.TrimSpace(cfg.Targets[i].Name)
		cfg.Targets[i].Address = strings.TrimSpace(cfg.Targets[i].Address)
		if cfg.Targets[i].Name == "" || cfg.Targets[i].Address == "" || cfg.Targets[i].Port <= 0 {
			return cfg, errors.New("each target requires non-empty name/address and port > 0")
		}
		key := strings.ToLower(cfg.Targets[i].Name)
		if _, exists := seenTargets[key]; exists {
			return cfg, fmt.Errorf("duplicate target name: %s", cfg.Targets[i].Name)
		}
		seenTargets[key] = struct{}{}
	}

	if err := normalizeStorageConfig(&cfg); err != nil {
		return cfg, err
	}

	cfg.Dashboard.ListenAddress = strings.TrimSpace(cfg.Dashboard.ListenAddress)
	cfg.Dashboard.PublicURL = strings.TrimSpace(cfg.Dashboard.PublicURL)
	if !cfg.Dashboard.Enabled && (cfg.Dashboard.ListenAddress != "" || cfg.Dashboard.PublicURL != "") {
		cfg.Dashboard.Enabled = true
	}
	if cfg.Dashboard.ListenAddress == "" {
		cfg.Dashboard.ListenAddress = ":8080"
	}
	if cfg.Dashboard.AuthTokenTTLSeconds <= 0 {
		cfg.Dashboard.AuthTokenTTLSeconds = 300
	}
	if cfg.Dashboard.MiniAppMaxAgeSec <= 0 {
		cfg.Dashboard.MiniAppMaxAgeSec = 86400
	}
	if cfg.Dashboard.Enabled && cfg.Dashboard.PublicURL == "" {
		return cfg, errors.New("dashboard.public_url is required when dashboard.enabled is true")
	}

	return cfg, nil
}

func loadInto(cfg *Config, path string) error {
	configJSONB64 := strings.TrimSpace(os.Getenv("TRACKWAY_CONFIG_JSON_B64"))
	if configJSONB64 != "" {
		rawJSON, err := decodeBase64Config(configJSONB64)
		if err != nil {
			return fmt.Errorf("decode TRACKWAY_CONFIG_JSON_B64: %w", err)
		}
		return unmarshalJSONConfig(rawJSON, "TRACKWAY_CONFIG_JSON_B64", cfg)
	}

	configJSON := strings.TrimSpace(os.Getenv("TRACKWAY_CONFIG_JSON"))
	if configJSON != "" {
		return unmarshalJSONConfig([]byte(configJSON), "TRACKWAY_CONFIG_JSON", cfg)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return unmarshalJSONConfig(data, path, cfg)
}

func decodeBase64Config(value string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return nil, errors.New("invalid base64 payload")
}

func unmarshalJSONConfig(data []byte, source string, cfg *Config) error {
	payload := strings.TrimSpace(string(data))
	if payload == "" {
		return fmt.Errorf("%s is empty", source)
	}
	if !strings.HasPrefix(payload, "{") {
		return fmt.Errorf("%s must be JSON object (YAML is not supported)", source)
	}
	if err := json.Unmarshal([]byte(payload), cfg); err != nil {
		return fmt.Errorf("unmarshal %s: %w", source, err)
	}
	return nil
}

func applyStorageEnvOverrides(cfg *Config) error {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_DRIVER"))); v != "" {
		cfg.Storage.Driver = v
	}

	if v := strings.TrimSpace(os.Getenv("SQLITE_PATH")); v != "" {
		cfg.Storage.SQLite.Path = v
	}
	if err := parseIntEnv("SQLITE_RETENTION_DAYS", &cfg.Storage.SQLite.RetentionDays); err != nil {
		return err
	}
	if err := parseIntEnv("SQLITE_BUSY_TIMEOUT_MS", &cfg.Storage.SQLite.BusyTimeoutMS); err != nil {
		return err
	}
	if err := parseIntEnv("SQLITE_MAX_OPEN_CONNS", &cfg.Storage.SQLite.MaxOpenConns); err != nil {
		return err
	}
	if err := parseIntEnv("SQLITE_MAX_IDLE_CONNS", &cfg.Storage.SQLite.MaxIdleConns); err != nil {
		return err
	}

	return nil
}

func parseIntEnv(name string, dst *int) error {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", name, err)
	}
	*dst = value
	return nil
}

func normalizeStorageConfig(cfg *Config) error {
	driver := strings.ToLower(strings.TrimSpace(cfg.Storage.Driver))
	if driver == "" {
		driver = defaultStorageDriver
	}
	cfg.Storage.Driver = driver

	switch driver {
	case "sqlite":
		normalizeSQLiteConfig(&cfg.Storage.SQLite)
	default:
		return fmt.Errorf("unsupported storage.driver: %s (only sqlite is supported)", driver)
	}

	return nil
}

func normalizeSQLiteConfig(sqlite *SQLite) {
	sqlite.Path = strings.TrimSpace(sqlite.Path)
	if sqlite.Path == "" {
		sqlite.Path = defaultSQLitePath
	}
	if sqlite.RetentionDays <= 0 {
		sqlite.RetentionDays = defaultSQLiteRetentionDay
	}
	if sqlite.BusyTimeoutMS <= 0 {
		sqlite.BusyTimeoutMS = defaultSQLiteBusyTimeout
	}
	if sqlite.MaxOpenConns <= 0 {
		sqlite.MaxOpenConns = defaultSQLiteMaxOpenConns
	}
	if sqlite.MaxIdleConns <= 0 {
		sqlite.MaxIdleConns = defaultSQLiteMaxIdleConns
	}
}
