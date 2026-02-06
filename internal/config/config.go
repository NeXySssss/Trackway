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
	Storage struct {
		LogDir string `yaml:"log_dir"`
	} `yaml:"storage"`
	Targets []Target `yaml:"targets"`
}

type Target struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Port    int    `yaml:"port"`
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
	return cfg, nil
}
