package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Bot struct {
		Token  string `yaml:"token" json:"token"`
		ChatID int64  `yaml:"chat_id" json:"chat_id"`
	} `yaml:"bot" json:"bot"`
	Monitoring struct {
		IntervalSeconds       int `yaml:"interval_seconds" json:"interval_seconds"`
		ConnectTimeoutSeconds int `yaml:"connect_timeout_seconds" json:"connect_timeout_seconds"`
		MaxParallelChecks     int `yaml:"max_parallel_checks" json:"max_parallel_checks"`
	} `yaml:"monitoring" json:"monitoring"`
	Storage   Storage   `yaml:"storage" json:"storage"`
	Dashboard Dashboard `yaml:"dashboard" json:"dashboard"`
	Targets   []Target  `yaml:"targets" json:"targets"`
}

type Storage struct {
	ClickHouse ClickHouse `yaml:"clickhouse" json:"clickhouse"`
}

type ClickHouse struct {
	Addr               string `yaml:"addr" json:"addr"`
	Database           string `yaml:"database" json:"database"`
	Username           string `yaml:"username" json:"username"`
	Password           string `yaml:"password" json:"password"`
	Table              string `yaml:"table" json:"table"`
	Secure             bool   `yaml:"secure" json:"secure"`
	DialTimeoutSeconds int    `yaml:"dial_timeout_seconds" json:"dial_timeout_seconds"`
	MaxOpenConns       int    `yaml:"max_open_conns" json:"max_open_conns"`
	MaxIdleConns       int    `yaml:"max_idle_conns" json:"max_idle_conns"`
}

type Target struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
	Port    int    `yaml:"port" json:"port"`
}

type Dashboard struct {
	Enabled             bool   `yaml:"enabled" json:"enabled"`
	ListenAddress       string `yaml:"listen_address" json:"listen_address"`
	PublicURL           string `yaml:"public_url" json:"public_url"`
	AuthTokenTTLSeconds int    `yaml:"auth_token_ttl_seconds" json:"auth_token_ttl_seconds"`
	SecureCookie        bool   `yaml:"secure_cookie" json:"secure_cookie"`
	MiniAppEnabled      bool   `yaml:"mini_app_enabled" json:"mini_app_enabled"`
	MiniAppMaxAgeSec    int    `yaml:"mini_app_max_age_seconds" json:"mini_app_max_age_seconds"`
}

func Load(path string) (Config, error) {
	var cfg Config

	if err := loadInto(&cfg, path); err != nil {
		return cfg, err
	}
	applyClickHouseEnvOverrides(&cfg)

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

	cfg.Storage.ClickHouse.Addr = strings.TrimSpace(cfg.Storage.ClickHouse.Addr)
	cfg.Storage.ClickHouse.Database = strings.TrimSpace(cfg.Storage.ClickHouse.Database)
	cfg.Storage.ClickHouse.Username = strings.TrimSpace(cfg.Storage.ClickHouse.Username)
	cfg.Storage.ClickHouse.Table = strings.TrimSpace(cfg.Storage.ClickHouse.Table)
	if cfg.Storage.ClickHouse.Addr == "" || cfg.Storage.ClickHouse.Database == "" {
		return cfg, errors.New("storage.clickhouse.addr and storage.clickhouse.database are required")
	}
	if cfg.Storage.ClickHouse.Username == "" {
		cfg.Storage.ClickHouse.Username = "default"
	}
	if cfg.Storage.ClickHouse.Table == "" {
		cfg.Storage.ClickHouse.Table = "track_logs"
	}
	if cfg.Storage.ClickHouse.DialTimeoutSeconds <= 0 {
		cfg.Storage.ClickHouse.DialTimeoutSeconds = 5
	}
	if cfg.Storage.ClickHouse.MaxOpenConns <= 0 {
		cfg.Storage.ClickHouse.MaxOpenConns = 10
	}
	if cfg.Storage.ClickHouse.MaxIdleConns <= 0 {
		cfg.Storage.ClickHouse.MaxIdleConns = 5
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
		if err := json.Unmarshal(rawJSON, cfg); err != nil {
			return fmt.Errorf("unmarshal TRACKWAY_CONFIG_JSON_B64: %w", err)
		}
		return nil
	}

	configJSON := strings.TrimSpace(os.Getenv("TRACKWAY_CONFIG_JSON"))
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), cfg); err != nil {
			return fmt.Errorf("unmarshal TRACKWAY_CONFIG_JSON: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
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

func applyClickHouseEnvOverrides(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_ADDR")); v != "" {
		cfg.Storage.ClickHouse.Addr = v
	}
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_DB")); v != "" {
		cfg.Storage.ClickHouse.Database = v
	}
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_DATABASE")); v != "" {
		cfg.Storage.ClickHouse.Database = v
	}
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_USER")); v != "" {
		cfg.Storage.ClickHouse.Username = v
	}
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_USERNAME")); v != "" {
		cfg.Storage.ClickHouse.Username = v
	}
	if v, ok := os.LookupEnv("CLICKHOUSE_PASSWORD"); ok {
		cfg.Storage.ClickHouse.Password = strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(os.Getenv("CLICKHOUSE_TABLE")); v != "" {
		cfg.Storage.ClickHouse.Table = v
	}
}
