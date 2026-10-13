# 55. The Implicit Claude Default Is Never a Silent Fallback for a Down Primary

Date: 2026-10-13

Status: Accepted

## Context

Every config has a built-in `claude` (Claude CLI) entry, and `escalation_agent` defaults to it. When the primary agent was unreachable, `resolveTarget` routed every turn to that default with nothing but a startup warning. For a user who never configured an escalation agent — typically running a local or private model on purpose — that silently sent the prompt and conversation context to a cloud model they had not chosen, as soon as the local server was down.

## Decision

- **"Defined" means configured.** `Config.EscalationAgentIsImplicit()` is true when `escalation_agent` is unset and no `claude` entry exists in `agents`, or when it names an agent that does not exist. `escalation_agent: "claude"` (or a `claude` entry) is a deliberate choice and keeps the old behavior, as does any user-named escalation agent.
- **Consent for the fallback only.** When the primary is unavailable and the turn would fall back to the implicit default (`routedTurn.NeedsFallbackConsent`), milk asks first: *primary agent X is unavailable and no escalation agent is configured; Claude Code is available on this host — run this turn on it?* The answer defaults to **No** (`events.PermissionRequest.DefaultNo`; the prompt says `[y/N]`): the cost of a wrong yes is data leaving the machine. Explicit `/escalate`, `--escalate`, a router decision to escalate, and the model's own `escalate()` are the user's or the router's own routing and are unchanged.
- **One ask layer, every surface.** `gateFallback` (`cmd/milk/fallback_core.go`) races the TUI prompt, the ACP permission dialog and Telegram through `askPermission` (ADR-0052's foreground policy), shared by the TUI turn and ACP. Single-prompt mode asks on the terminal when stdin is one and otherwise refuses with the explicit ways to proceed. With no surface to ask, the answer is no.
- **An approval covers the outage.** `interactiveState.claudeFallbackApproved` stops the question repeating every turn, is cleared as soon as the primary answers again, and an approved fallback turn does not become auto-sticky (otherwise Claude would keep receiving turns after recovery).
- **Declining is not a failure.** The turn is not run and not recorded in history (`errFallbackDeclined`, shown as a notice; ACP ends the turn normally).
- **Nowhere to run.** Primary down and no escalation agent available fails the turn up front with a message naming both, instead of an exec error later.
- Startup/reload warnings and the empty-state banner say that a fallback will be asked about, rather than that turns are being routed to escalation.

## Consequences

- A down primary no longer produces a working-looking session that is quietly on a cloud model; the first turn after an outage costs one keystroke.
- Users who want the old automatic behavior set `escalation_agent: "claude"`.
- Unattended callers (single-prompt in a pipe, ACP clients that cannot answer permission requests) fail or decline rather than fall back.
- Not covered: a primary that is up but fails mid-turn (that error is reported as before), and background jobs/workflows, which have no fallback path.
