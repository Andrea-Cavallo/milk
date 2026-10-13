# 55. The Implicit Claude Default Is Never Reached Without Approval

Date: 2026-10-13

Status: Accepted

## Context

Every config has a built-in `claude` (Claude CLI) entry, and `escalation_agent` defaults to it. That default was used silently: when the primary agent was unreachable `resolveTarget` routed every turn to it with only a startup warning, and `/escalate`, the router and the model's own `escalate()` all landed on it. For a user who never configured an escalation agent — typically running a local or private model on purpose — that sends the prompt and the conversation context to a cloud model they did not choose.

## Decision

- **"Defined" means configured.** `Config.EscalationAgentIsImplicit()` is true when `escalation_agent` is unset and no `claude` entry exists in `agents`, or when it names an agent that does not exist. `escalation_agent: "claude"` (or a `claude` entry) is a deliberate choice and keeps the automatic behavior, as does any user-named escalation agent.
- **Every route to the implicit default asks first.** A turn headed there — a down primary falling back, an explicit `/escalate` or `--escalate` (an instruction to escalate is not a choice of Claude), a pin, sticky escalation, a router decision — sets `routedTurn.NeedsClaudeConsent`, and so does the model's own `escalate()` handoff (a hook on the turn context, `confirmSelfEscalation`, because single-prompt mode deliberately has no `dispatchAgents`). The question names the reason: primary unavailable, no escalation agent configured, or the model's escalation reason.
- **Default No, once per session.** The prompt is `[y/N]` (`events.PermissionRequest.DefaultNo`): the cost of a wrong yes is data leaving the machine. An approval (`interactiveState.claudeApproved`) holds for the rest of the session on every route, so the question is not repeated; it is not persisted — `escalation_agent` is how to make Claude the configured agent.
- **One ask layer, every surface.** `gateClaude` (`cmd/milk/fallback_core.go`) races the TUI prompt, the ACP permission dialog and Telegram through `askPermission` (ADR-0052's foreground policy). Single-prompt mode asks on the terminal when stdin is one and otherwise refuses, naming `escalation_agent`. With no surface to ask, the answer is no.
- **Declining is not a failure.** The turn is not run and not recorded (`errFallbackDeclined`, shown as a notice; ACP ends the turn normally). A declined `escalate()` is recorded as an assistant note so the request does not look unanswered.
- **An approved outage fallback does not make escalation sticky** (`noteTurnSucceeded`): the approval is for Claude, not for pinning the session there after the primary recovers.
- **Nowhere to run.** Primary down and no escalation agent available fails the turn up front with a message naming both, instead of an exec error later.
- Startup/reload warnings and the empty-state banner say that Claude will be asked about, rather than that turns are being routed to escalation.

## Consequences

- No session ends up quietly on a cloud model; the first Claude use of a session costs one keystroke.
- Users who want the old automatic behavior set `escalation_agent: "claude"`.
- Unattended callers (single-prompt in a pipe, ACP clients that cannot answer permission requests) fail or decline rather than escalate.
- Not covered: workflow stages that name the `escalation` role explicitly in their YAML, and a primary that fails mid-turn (reported as before).
