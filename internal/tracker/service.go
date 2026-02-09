package tracker

import (
	"context"

	"github.com/go-telegram/bot/models"

	"trackway/internal/config"
	"trackway/internal/logstore"
)

type Service struct {
	engine   *MonitorEngine
	alerts   *AlertManager
	commands *CommandHandler

	// compatibility layer for package tests and internal callers
	targets      []*TargetState
	targetByName map[string]*TargetState
}

func New(cfg config.Config, logs *logstore.Store, notifier Notifier) *Service {
	engine := NewMonitorEngine(cfg, logs)
	alerts := NewAlertManager(notifier)
	commands := NewCommandHandler(cfg.Bot.ChatID, engine, notifier)

	return &Service{
		engine:       engine,
		alerts:       alerts,
		commands:     commands,
		targets:      engine.targets,
		targetByName: engine.targetByName,
	}
}

func (s *Service) SetAuthLinkGenerator(fn func() (string, error)) {
	s.commands.SetAuthLinkGenerator(fn)
}

func (s *Service) RunMonitor(ctx context.Context) {
	s.engine.Run(ctx, func(events []alertEvent) {
		s.alerts.SendBatch(ctx, events)
	})
}

func (s *Service) HandleUpdate(ctx context.Context, update *models.Update) {
	s.commands.HandleUpdate(ctx, update)
}

func (s *Service) Snapshot() Snapshot {
	return s.engine.Snapshot()
}

func (s *Service) TargetNames() []string {
	return s.engine.TargetNames()
}

func (s *Service) Logs(trackName string, days int, limit int) ([]logstore.Row, bool) {
	return s.engine.Logs(trackName, days, limit)
}

func (s *Service) UpsertTarget(name, address string, port int) error {
	return s.engine.UpsertTarget(name, address, port)
}

func (s *Service) DeleteTarget(name string) error {
	return s.engine.DeleteTarget(name)
}

func (s *Service) applyStatus(target *TargetState, status bool) *alertEvent {
	return s.engine.applyStatus(target, status)
}

func (s *Service) sendAlertBatch(ctx context.Context, events []alertEvent) {
	s.alerts.SendBatch(ctx, events)
}

func (s *Service) listText() string {
	return s.commands.listText()
}

func (s *Service) statusText() string {
	return s.commands.statusText()
}

func (s *Service) logsMessages(trackName string) []string {
	return s.commands.logsMessages(trackName)
}

func (s *Service) authLinkText(chatID int64) string {
	return s.commands.authLinkText(chatID)
}
