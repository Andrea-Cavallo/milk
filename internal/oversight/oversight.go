// Package oversight defines the Notifier interface for remote oversight
// of agent turns and permission prompts.
package oversight

import "context"

// PermDecision is the result of a remote permission query.
type PermDecision int

const (
	PermAllow PermDecision = iota
	PermDeny
	PermTimeout // remote did not respond within deadline
	PermAllowAlways
	PermDenyAlways
)

// Allow reports whether the decision approves the action — any variant,
// including the "always" answers (issue #302), which approve this ask AND
// ask for it to become the session's standing answer (see Always).
func (d PermDecision) Allow() bool { return d == PermAllow || d == PermAllowAlways }

// Always reports whether the answer is also meant to stick as the session's
// standing answer for tool asks (the remote counterpart of the TUI's a/d
// keys and ACP's allow_always/reject_always options). Callers that cannot
// honor stickiness (the doom-loop safety gate) must treat the decision as
// its plain value via Allow.
func (d PermDecision) Always() bool { return d == PermAllowAlways || d == PermDenyAlways }

// PermRequest carries the details of a pending permission prompt.
type PermRequest struct {
	ToolName    string
	Input       string // human-readable one-liner (from cliToolArgSummary)
	Description string
	BlockedPath string
	// AllowAlways tells the remote surface it may offer a sticky answer
	// ("answer this and everything after it the same way", issue #302) —
	// false for asks the session mode must never cover (the doom-loop
	// safety gate), where the prompt keeps its plain y/n wording.
	AllowAlways bool
}

// InputRequest carries an ask_user question to the remote interface.
type InputRequest struct {
	Question string
	Options  []string // optional suggested choices; free text still accepted
}

// Notifier is the interface for remote oversight backends.
// All methods are best-effort — implementations must not block the caller
// on network errors and must return quickly.
type Notifier interface {
	// NotifyTurnStart fires when a turn is dispatched to an agent.
	// agent is the display name; target is "local" or "escalation".
	NotifyTurnStart(ctx context.Context, agent, target, prompt string)

	// NotifyToolUse fires when the escalation agent begins a tool call.
	NotifyToolUse(ctx context.Context, toolName, summary string)

	// NotifyToolResult fires when a tool call completes, forwarding a
	// truncated summary of its output. isError reports whether the tool
	// itself reported a failure (not a transport-level error).
	NotifyToolResult(ctx context.Context, toolName, summary string, isError bool)

	// NotifyTurnDone fires when a turn completes (or errors).
	NotifyTurnDone(ctx context.Context, agent string, err error)

	// NotifyResponse forwards the agent's final response text.
	NotifyResponse(ctx context.Context, agent, text string)

	// AskPermission sends a permission request to the remote interface and
	// waits for a reply. answered reports whether a human actually replied;
	// a timeout — the backend's own prompt timeout or ctx cancellation — is
	// not an answer (see ADR-0052). On silence, decision carries the
	// backend's configured timeout action, and the caller decides what
	// silence means: the timeout action for foreground asks, the timed
	// default for background ones.
	AskPermission(ctx context.Context, req PermRequest) (decision PermDecision, answered bool)

	// AskInput sends an ask_user question to the remote interface and waits
	// for a free-text reply. answered reports whether a human actually
	// replied; a timeout — the backend's own prompt timeout or ctx
	// cancellation — is silence, and for questions silence is never an
	// answer (unlike AskPermission there is no timeout action): the caller
	// keeps waiting on its other surfaces or the timed answer.
	AskInput(ctx context.Context, req InputRequest) (answer string, answered bool)
}

// Noop is a no-op Notifier used when remote oversight is disabled.
type Noop struct{}

func (Noop) NotifyTurnStart(_ context.Context, _, _, _ string)       {}
func (Noop) NotifyToolUse(_ context.Context, _, _ string)            {}
func (Noop) NotifyToolResult(_ context.Context, _, _ string, _ bool) {}
func (Noop) NotifyTurnDone(_ context.Context, _ string, _ error)     {}
func (Noop) NotifyResponse(_ context.Context, _, _ string)           {}
func (Noop) AskPermission(_ context.Context, _ PermRequest) (PermDecision, bool) {
	// Nobody to answer: silence, with the timeout-action-shaped decision the
	// callers fall back to for foreground asks (deny, like the default
	// timeout action).
	return PermDeny, false
}
func (Noop) AskInput(_ context.Context, _ InputRequest) (string, bool) {
	return "", false
}
