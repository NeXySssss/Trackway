package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"trackway/internal/config"
	"trackway/internal/logstore"
	"trackway/internal/telegram"
	"trackway/internal/tracker"
)

func main() {
	cfgPath := envOrDefault("CONFIG_PATH", "config.yaml")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}

	logDir := strings.TrimSpace(envOrDefault("LOG_DIR", cfg.Storage.LogDir))
	if logDir == "" {
		logDir = "logs"
	}

	store, err := logstore.New(logDir)
	if err != nil {
		fmt.Println("log dir error:", err)
		os.Exit(1)
	}

	svc := tracker.New(cfg, store, nil)
	client, err := telegram.New(cfg.Bot.Token, cfg.Bot.ChatID, svc.HandleUpdate)
	if err != nil {
		fmt.Println("bot init error:", err)
		os.Exit(1)
	}
	svc.SetNotifier(client)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go svc.RunMonitor(ctx)

	_ = client.SendDefaultHTML(ctx, "<b>INFO</b>\nport tracker started (Go)")
	client.Start(ctx)
	_ = client.SendDefaultHTML(context.Background(), "<b>INFO</b>\nport tracker stopped")
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
