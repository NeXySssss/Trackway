package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"trackway/internal/util"
)

type AlertManager struct {
	notifier Notifier
	logger   *slog.Logger
	mu       sync.Mutex

	pendingDown  map[string]pendingDownAlert
	pendingGroup map[string][]pendingDownGroup
}

func NewAlertManager(notifier Notifier) *AlertManager {
	return &AlertManager{
		notifier:     notifier,
		logger:       slog.Default(),
		pendingDown:  make(map[string]pendingDownAlert),
		pendingGroup: make(map[string][]pendingDownGroup),
	}
}

func (a *AlertManager) SendBatch(ctx context.Context, events []alertEvent) {
	if a.notifier == nil || len(events) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	events = a.applyFastRecoveryEdits(ctx, events, 30*time.Second)
	if len(events) == 0 {
		return
	}

	groups := make(map[string][]alertEvent)
	order := make([]string, 0, len(events))
	for _, event := range events {
		key := event.Kind + "|" + event.Reason
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], event)
	}

	sort.SliceStable(order, func(i, j int) bool {
		leftKind, _, _ := strings.Cut(order[i], "|")
		rightKind, _, _ := strings.Cut(order[j], "|")
		if leftKind != rightKind {
			return alertOrder(leftKind) < alertOrder(rightKind)
		}
		return order[i] < order[j]
	})

	for _, key := range order {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return group[i].Target < group[j].Target })
		message := formatAlertGroup(group)
		kind, reason, _ := strings.Cut(key, "|")

		a.handleGroupSend(ctx, kind, reason, group, message, key)
	}
}

func (a *AlertManager) handleGroupSend(ctx context.Context, kind, reason string, group []alertEvent, message, key string) {
	if kind == "DOWN" && reason == "state-change" && len(group) == 1 {
		messageID, err := a.notifier.SendDefaultHTMLWithID(ctx, message)
		if err != nil {
			a.logger.Warn("failed to send grouped alert", "key", key, "count", len(group), "error", err)
			return
		}
		if messageID > 0 {
			ev := group[0]
			a.pendingDown[ev.Target] = pendingDownAlert{
				MessageID: messageID,
				DownAt:    ev.Occurred,
				Reason:    ev.Reason,
				Address:   ev.Address,
				Port:      ev.Port,
			}
		}
		return
	}

	if kind == "DOWN" && reason == "state-change" && len(group) > 1 {
		messageID, err := a.notifier.SendDefaultHTMLWithID(ctx, message)
		if err != nil {
			a.logger.Warn("failed to send grouped alert", "key", key, "count", len(group), "error", err)
			return
		}
		if messageID > 0 {
			pending := pendingDownGroup{
				MessageID: messageID,
				Reason:    reason,
				DownAt:    group[0].Occurred,
				Targets:   make(map[string]alertEvent, len(group)),
			}
			for _, ev := range group {
				pending.Targets[ev.Target] = ev
			}
			a.pendingGroup[reason] = append(a.pendingGroup[reason], pending)
		}
		return
	}

	if err := a.notifier.SendDefaultHTML(ctx, message); err != nil {
		a.logger.Warn("failed to send grouped alert", "key", key, "count", len(group), "error", err)
	}
}

func (a *AlertManager) applyFastRecoveryEdits(ctx context.Context, events []alertEvent, window time.Duration) []alertEvent {
	remaining := make([]alertEvent, 0, len(events))
	groupedRecoveries := make(map[string][]alertEvent)

	for _, ev := range events {
		if ev.Kind != "RECOVERED" || ev.Reason != "state-change" {
			remaining = append(remaining, ev)
			continue
		}

		pending, ok := a.pendingDown[ev.Target]
		if !ok {
			groupedRecoveries[ev.Reason] = append(groupedRecoveries[ev.Reason], ev)
			continue
		}
		delete(a.pendingDown, ev.Target)

		if ev.Occurred.Sub(pending.DownAt) > window {
			groupedRecoveries[ev.Reason] = append(groupedRecoveries[ev.Reason], ev)
			continue
		}

		editText := formatRecoveredEdit(ev, pending)
		if err := a.notifier.EditDefaultHTML(ctx, pending.MessageID, editText); err != nil {
			a.logger.Warn("failed to edit down alert message", "track", ev.Target, "error", err)
			groupedRecoveries[ev.Reason] = append(groupedRecoveries[ev.Reason], ev)
		}
	}

	// handle grouped DOWN -> RECOVERED edits
	for reason, recovs := range groupedRecoveries {
		pendingList := a.pendingGroup[reason]
		if len(pendingList) == 0 {
			remaining = append(remaining, recovs...)
			continue
		}
		consumedIdx := -1
		for idx, pending := range pendingList {
			if len(pending.Targets) != len(recovs) {
				continue
			}
			match := true
			for _, ev := range recovs {
				if _, ok := pending.Targets[ev.Target]; !ok {
					match = false
					break
				}
				if ev.Occurred.Sub(pending.DownAt) > window {
					match = false
					break
				}
			}
			if match {
				consumedIdx = idx
				if err := a.notifier.EditDefaultHTML(ctx, pending.MessageID, formatGroupedRecoveryEdit(pending, recovs)); err != nil {
					a.logger.Warn("failed to edit grouped alert", "reason", reason, "error", err)
					remaining = append(remaining, recovs...)
				}
				break
			}
		}
		if consumedIdx >= 0 {
			pendingList = append(pendingList[:consumedIdx], pendingList[consumedIdx+1:]...)
			a.pendingGroup[reason] = pendingList
		} else {
			remaining = append(remaining, recovs...)
		}
	}
	return remaining
}

func formatRecoveredEdit(recovered alertEvent, pending pendingDownAlert) string {
	downtime := recovered.Occurred.Sub(pending.DownAt)
	if downtime < 0 {
		downtime = 0
	}
	var sb strings.Builder
	sb.WriteString("<b>DOWN -> RECOVERED</b>\n")
	fmt.Fprintf(&sb, "reason: <code>%s</code>\n", util.HTMLEscape(recovered.Reason))
	fmt.Fprintf(&sb, "down_at_utc: <code>%s</code>\n", pending.DownAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "recovered_at_utc: <code>%s</code>\n", recovered.Occurred.Format(time.RFC3339))
	fmt.Fprintf(&sb, "downtime: <code>%s</code>\n", formatDurationShort(downtime))
	sb.WriteString("target:\n")
	fmt.Fprintf(
		&sb,
		"- <code>%s</code> (<code>%s:%d</code>)",
		util.HTMLEscape(recovered.Target),
		util.HTMLEscape(recovered.Address),
		recovered.Port,
	)
	return sb.String()
}

func formatDurationShort(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
}

func formatGroupedRecoveryEdit(pending pendingDownGroup, recovs []alertEvent) string {
	if len(recovs) == 0 {
		return ""
	}
	// use latest recover time for header
	latest := recovs[0].Occurred
	for _, ev := range recovs[1:] {
		if ev.Occurred.After(latest) {
			latest = ev.Occurred
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>DOWN -> RECOVERED x%d</b>\n", len(recovs))
	fmt.Fprintf(&sb, "reason: <code>%s</code>\n", util.HTMLEscape(recovs[0].Reason))
	fmt.Fprintf(&sb, "time_utc: <code>%s</code>\n", latest.Format(time.RFC3339))
	sb.WriteString("targets:\n")
	sort.Slice(recovs, func(i, j int) bool { return recovs[i].Target < recovs[j].Target })
	for _, ev := range recovs {
		downtime := ev.Occurred.Sub(pending.DownAt)
		if downEvent, ok := pending.Targets[ev.Target]; ok {
			downtime = ev.Occurred.Sub(downEvent.Occurred)
		}
		fmt.Fprintf(
			&sb,
			"- <code>%s</code> (<code>%s:%d</code>)\nrecovered_at_utc: <code>%s</code>\ndowntime: <code>%s</code>\n",
			util.HTMLEscape(ev.Target),
			util.HTMLEscape(ev.Address),
			ev.Port,
			ev.Occurred.Format(time.RFC3339),
			formatDurationShort(downtime),
		)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func formatAlertGroup(events []alertEvent) string {
	if len(events) == 0 {
		return ""
	}
	first := events[0]
	var sb strings.Builder
	if len(events) == 1 {
		fmt.Fprintf(&sb, "<b>%s</b>\n", util.HTMLEscape(first.Kind))
	} else {
		fmt.Fprintf(&sb, "<b>%s x%d</b>\n", util.HTMLEscape(first.Kind), len(events))
	}
	fmt.Fprintf(&sb, "reason: <code>%s</code>\n", util.HTMLEscape(first.Reason))
	fmt.Fprintf(&sb, "time_utc: <code>%s</code>\n", first.Occurred.Format(time.RFC3339))
	sb.WriteString("targets:\n")
	for _, event := range events {
		fmt.Fprintf(
			&sb,
			"- <code>%s</code> (<code>%s:%d</code>)\n",
			util.HTMLEscape(event.Target),
			util.HTMLEscape(event.Address),
			event.Port,
		)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func alertOrder(kind string) int {
	switch kind {
	case "DOWN":
		return 0
	case "RECOVERED":
		return 1
	default:
		return 2
	}
}
