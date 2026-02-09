package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot/models"

	"trackway/internal/config"
	"trackway/internal/dashboard"
	"trackway/internal/logstore"
	"trackway/internal/telegram"
	"trackway/internal/tracker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

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

	updates := make(chan *models.Update, 128)
	client, err := telegram.New(cfg.Bot.Token, cfg.Bot.ChatID, func(ctx context.Context, update *models.Update) {
		select {
		case updates <- update:
		case <-ctx.Done():
		default:
			slog.Warn("dropping update due to full queue")
		}
	})
	if err != nil {
		fmt.Println("bot init error:", err)
		os.Exit(1)
	}
	svc := tracker.New(cfg, store, client)
	var dash *dashboard.Server
	if cfg.Dashboard.Enabled {
		dash, err = dashboard.New(cfg.Dashboard, svc)
		if err != nil {
			fmt.Println("dashboard init error:", err)
			os.Exit(1)
		}
		svc.SetAuthLinkGenerator(dash.NewAuthLink)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.RunMonitor(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updates:
				svc.HandleUpdate(ctx, update)
			}
		}
	}()
	if dash != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := dash.ListenAndServe(ctx); err != nil {
				slog.Error("dashboard server failed", "error", err)
				cancel()
			}
		}()
	}

	sendStatus(client, "<b>INFO</b>\nport tracker started (Go)")
	client.Start(ctx)
	wg.Wait()
	sendStatus(client, "<b>INFO</b>\nport tracker stopped")
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func sendStatus(client *telegram.Client, message string) {
	sendCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.SendDefaultHTML(sendCtx, message); err != nil {
		fmt.Println("status message error:", err)
	}
}
