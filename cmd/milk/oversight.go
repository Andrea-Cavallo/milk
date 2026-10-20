package main

import (
	"context"
	"fmt"
	"os"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/oversight"
	"github.com/scoutme/milk/internal/oversight/telegram"
)

// newNotifier constructs the appropriate Notifier from config.
// Returns oversight.Noop when remote oversight is disabled or misconfigured.
func newNotifier(cfg config.Config) oversight.Notifier {
	ro := cfg.RemoteOversight
	if ro == nil || ro.Backend == "" {
		return oversight.Noop{}
	}
	switch ro.Backend {
	case "telegram":
		if ro.Telegram == nil || ro.Telegram.Token == "" || ro.Telegram.ChatID == 0 {
			fmt.Fprintln(os.Stderr, milkTag()+" warning: remote_oversight.telegram missing token or chat_id — oversight disabled")
			return oversight.Noop{}
		}
		n := telegram.New(telegram.Config{
			Token:         ro.Telegram.Token,
			ChatID:        ro.Telegram.ChatID,
			PermTimeout:   ro.PermTimeoutDuration(),
			TimeoutAction: ro.TimeoutActionValue(),
			BusyIndicator: ro.BusyIndicatorEnabled(),
		})
		if err := n.Ping(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "%s warning: telegram oversight ping failed: %v — oversight disabled\n", milkTag(), err)
			return oversight.Noop{}
		}
		notifyAnnouncement(n, "milk", "startup", "oversight active")
		return n
	default:
		fmt.Fprintf(os.Stderr, "%s warning: unknown remote_oversight backend %q — oversight disabled\n", milkTag(), ro.Backend)
		return oversight.Noop{}
	}
}

// notifyAnnouncement routes one-off "turn-start-style" notices (startup,
// /setup confirmations) around the busy indicator (issue #284): backends
// that pair NotifyTurnStart with a busy status — Telegram's "typing…" —
// expose Announce, and an announcement that never gets a matching
// NotifyTurnDone would otherwise leave it running forever. Backends without
// Announce (Noop, test fakes) keep the old path unchanged.
func notifyAnnouncement(n oversight.Notifier, agent, target, prompt string) {
	if a, ok := n.(interface{ Announce(string, string, string) }); ok {
		a.Announce(agent, target, prompt)
		return
	}
	n.NotifyTurnStart(context.Background(), agent, target, prompt)
}

// newNotifierFor is newNotifier behind a seam: the ACP serve process builds
// its notifier through it (acp_oversight.go's resetNotifier), so tests can
// substitute a fake and never ping Telegram or spawn a poller.
var newNotifierFor = newNotifier
