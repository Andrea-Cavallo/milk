package main

// Host-independent rules for running a turn on the implicit Claude CLI when
// the primary agent is unavailable. The escalation target is only "defined"
// when the user configured one (config.EscalationAgentIsImplicit); the
// built-in Claude default is never used as a silent stand-in for a down
// primary, because that sends the conversation to a cloud model the user did
// not choose. Shared by the TUI (runTurn), ACP and single-prompt mode.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/events"
	"github.com/scoutme/milk/internal/oversight"
	"github.com/scoutme/milk/internal/router"
)

// errFallbackDeclined marks a turn that was not run because the user declined
// (or nobody could answer) the Claude fallback question. Hosts show it as a
// notice, not as a failure.
var errFallbackDeclined = errors.New("turn not run")

// primaryDisplayName is the configured primary agent's name for messages.
func primaryDisplayName(cfg config.Config) string { return cfg.ActiveAgent().Name }

// claudeFallbackQuestion is the plain-text question (no styling) both hosts
// and remote oversight show.
func claudeFallbackQuestion(cfg config.Config) string {
	return fmt.Sprintf("Primary agent %s is unavailable and no escalation agent is configured. "+
		"Claude Code is available on this host — run this turn on it? (the prompt and conversation context go to Claude)",
		primaryDisplayName(cfg))
}

// noAgentError is returned when a turn has nowhere to run.
func noAgentError(cfg config.Config) error {
	primary := primaryDisplayName(cfg)
	if cfg.EscalationAgentIsImplicit() {
		return fmt.Errorf("neither the primary agent (%s) nor an escalation agent is available — "+
			"no escalation agent is configured and Claude Code was not found on this host; use /agent to reconfigure", primary)
	}
	return fmt.Errorf("neither the primary agent (%s) nor the escalation agent (%s) is available — use /agent to reconfigure",
		primary, cfg.EscalationAgentConfig().Name)
}

// fallbackDeclinedError wraps errFallbackDeclined with the user-facing reason.
func fallbackDeclinedError(cfg config.Config) error {
	return fmt.Errorf("%w: primary agent %s is unavailable and Claude Code fallback was not approved "+
		"(/escalate runs a turn on Claude explicitly; set escalation_agent to make it the configured fallback)",
		errFallbackDeclined, primaryDisplayName(cfg))
}

// gateFallback applies the consent rules to a routed turn. It returns nil
// when the turn may run, noAgentError when there is nowhere to run it, and
// fallbackDeclinedError when the user said no. host and n are the ask
// surfaces (either may be nil); with neither, the question resolves to "no".
// An approval holds until the primary answers again (routeTurn resets it).
func gateFallback(ctx context.Context, st *interactiveState, rt routedTurn, host events.Host, n oversight.Notifier) error {
	if rt.NoAgent {
		return noAgentError(st.cfg)
	}
	if !rt.NeedsFallbackConsent {
		return nil
	}
	question := claudeFallbackQuestion(st.cfg)
	var direct func(context.Context) bool
	if host != nil {
		direct = func(c context.Context) bool {
			out, err := host.RequestPermission(c, events.PermissionRequest{
				Prompt:    "\n" + milkTag() + " " + question + " [y/N] ",
				DefaultNo: true,
			})
			return err == nil && out.Allow
		}
	}
	allow, _ := askPermission(ctx, n,
		oversight.PermRequest{ToolName: "claude-fallback", Input: question},
		direct, foregroundAskPolicy)
	if !allow {
		return fallbackDeclinedError(st.cfg)
	}
	st.claudeFallbackApproved = true
	return nil
}

// singlePromptFallbackGate is gateFallback for single-prompt mode, which has
// no TUI or ACP client: it asks on the terminal when stdin is one, and
// otherwise refuses with the explicit ways to proceed. decided is the
// router's choice, target the choice after the availability fallback.
func singlePromptFallbackGate(cfg config.Config, decided, target router.Target, interactive bool, in io.Reader, errOut io.Writer) error {
	if decided != router.TargetLocal || target != router.TargetEscalation || !cfg.EscalationAgentIsImplicit() {
		return nil
	}
	question := claudeFallbackQuestion(cfg)
	if !interactive {
		return fmt.Errorf("primary agent %s is unavailable and no escalation agent is configured; Claude Code is available on this host — "+
			"re-run with --escalate to use it explicitly, or set escalation_agent", primaryDisplayName(cfg))
	}
	fmt.Fprintf(errOut, "%s %s [y/N] ", milkTag(), question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		return fallbackDeclinedError(cfg)
	}
	return nil
}

// primaryDownWarning is the startup/reload line for an unreachable primary.
// With an implicit Claude default nothing is routed there on its own; the
// user is asked per outage instead.
func primaryDownWarning(cfg config.Config, escalationAvail bool) string {
	name := primaryDisplayName(cfg)
	switch {
	case !cfg.EscalationAgentIsImplicit():
		return "warning: " + name + " primary agent unreachable — routing all to escalation agent"
	case escalationAvail:
		return "warning: " + name + " primary agent unreachable; no escalation agent is configured — you will be asked before falling back to Claude Code"
	default:
		return "warning: " + name + " primary agent unreachable; no escalation agent is configured and Claude Code was not found"
	}
}
