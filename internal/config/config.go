package config

import (
	"errors"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Bot struct {
		Token  string `yaml:"token"`
		ChatID int64  `yaml:"chat_id"`
	} `yaml:"bot"`
	Monitoring struct {
		IntervalSeconds       int `yaml:"interval_seconds"`
		ConnectTimeoutSeconds int `yaml:"connect_timeout_seconds"`
		MaxParallelChecks     int `yaml:"max_parallel_checks"`
	} `yaml:"monitoring"`
	Storage   Storage   `yaml:"storage"`
	Dashboard Dashboard `yaml:"dashboard"`
	Targets   []Target  `yaml:"targets"`
}

type Storage struct {
	ClickHouse ClickHouse `yaml:"clickhouse"`
}

type ClickHouse struct {
	Addr               string `yaml:"addr"`
	Database           string `yaml:"database"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	Table              string `yaml:"table"`
	Secure             bool   `yaml:"secure"`
	DialTimeoutSeconds int    `yaml:"dial_timeout_seconds"`
	MaxOpenConns       int    `yaml:"max_open_conns"`
	MaxIdleConns       int    `yaml:"max_idle_conns"`
}

type Target struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
}

type Dashboard struct {
	Enabled             bool   `yaml:"enabled"`
	ListenAddress       string `yaml:"listen_address"`
	PublicURL           string `yaml:"public_url"`
	AuthTokenTTLSeconds int    `yaml:"auth_token_ttl_seconds"`
	SecureCookie        bool   `yaml:"secure_cookie"`
	MiniAppEnabled      bool   `yaml:"mini_app_enabled"`
	MiniAppMaxAgeSec    int    `yaml:"mini_app_max_age_seconds"`
}

func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Bot.Token == "" || cfg.Bot.ChatID == 0 {
		return cfg, errors.New("bot.token and bot.chat_id are required")
	}
	if len(cfg.Targets) == 0 {
		return cfg, errors.New("targets list is empty")
	}
	for i := range cfg.Targets {
		cfg.Targets[i].Name = strings.TrimSpace(cfg.Targets[i].Name)
		cfg.Targets[i].Address = strings.TrimSpace(cfg.Targets[i].Address)
		if cfg.Targets[i].Name == "" || cfg.Targets[i].Address == "" || cfg.Targets[i].Port <= 0 {
			return cfg, errors.New("each target requires non-empty name/address and port > 0")
		}
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
