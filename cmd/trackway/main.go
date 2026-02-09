package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

	cfgPath := envOrDefault("CONFIG_PATH", "config.json")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}

	store, err := initStore(cfg)
	if err != nil {
		fmt.Println("storage init error:", err)
		os.Exit(1)
	}
	if err := seedTargets(store, cfg.Targets); err != nil {
		fmt.Println("targets init error:", err)
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
		allowedMiniAppUserID := int64(0)
		if cfg.Bot.ChatID > 0 {
			allowedMiniAppUserID = cfg.Bot.ChatID
		}
		dash, err = dashboard.New(cfg.Dashboard, cfg.Bot.Token, svc, allowedMiniAppUserID)
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

func initStore(cfg config.Config) (*logstore.Store, error) {
	if cfg.Storage.Driver != "sqlite" {
		return nil, fmt.Errorf("unsupported storage driver: %s", cfg.Storage.Driver)
	}
	return logstore.NewSQLite(logstore.SQLiteOptions{
		Path:          cfg.Storage.SQLite.Path,
		RetentionDays: cfg.Storage.SQLite.RetentionDays,
		BusyTimeoutMS: cfg.Storage.SQLite.BusyTimeoutMS,
		MaxOpenConns:  cfg.Storage.SQLite.MaxOpenConns,
		MaxIdleConns:  cfg.Storage.SQLite.MaxIdleConns,
	})
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

func seedTargets(store *logstore.Store, targets []config.Target) error {
	if len(targets) == 0 {
		return nil
	}
	existing, err := store.ListTargets()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	for _, target := range targets {
		if err := store.UpsertTarget(target.Name, target.Address, target.Port); err != nil {
			return err
		}
	}
	slog.Info("seeded targets from config", "count", len(targets))
	return nil
}
