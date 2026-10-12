package main

import (
	"strings"

	"github.com/scoutme/milk/internal/loop"
)

// The message body of a loop/consumption verdict, shared by the TUI
// (repl.go's chunk/think/turn handlers) and the ACP server (acp_signals.go's
// surfaceVerdicts) — issue #188's loop-feeding row. The hosts kept drifting
// here (the ACP line said "turn auto-interrupted", the TUI one didn't; the
// "consider /escalate" suggestion used different pinned-ness rules), so the
// suffix rules live in one place now. Hosts still decide what a verdict
// *reaches* — the TUI's two-tier transcript/status-bar thresholds, ACP's
// notifications — and how it is styled.

// loopVerdictMsg returns the shared line for a verdict: the detector's own
// message, plus
//
//   - "— consider /escalate" for token_velocity (the one cross-turn signal
//     with no mid-turn equivalent to act on — the only lever is suggesting
//     escalation for the *next* turn), only when the session isn't already
//     headed to escalation (explicit or auto-sticky — there is nothing new to
//     suggest there);
//   - "— turn auto-interrupted" when the detector's interrupt fired.
func loopVerdictMsg(v loop.Verdict, pinned bool) string {
	msg := v.Message
	if v.Signal == loop.SignalTokenVelocity && !pinned {
		msg += " — consider /escalate"
	}
	if v.ShouldInterrupt {
		msg += " — turn auto-interrupted"
	}
	return strings.TrimSpace(msg)
}
