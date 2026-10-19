# 65. Confirmation-aware routing: "proceed/go ahead" routes on the confirmed content

Date: 2026-10-19

Status: Accepted

## Context

Issue #263: routing scored **each prompt in isolation**, so short confirmation prompts ("yes, go ahead", "proceed", "do it") that execute a previously discussed hard/complex task hit the `local_below_tokens` unconditional-local shortcut (≤ 30 tokens) or scored firmly local. The cheap primary agent was then handed the *execution of the hard task the user was confirming*, with predictable results: shallow work, thrash, or a mid-task `escalate()` after turns were already burned. Worse, some confirmations are literally the decision point for hard work ("yes, do the big refactor") and the router had zero visibility into what was being confirmed.

The failure mode to avoid cuts the other way: over-escalating cheap follow-ups ("yes, list the files too") onto the expensive agent. Auto-sticky escalation only helps once the router has already escalated; `ESCALATION_WAITING` already bypasses the router and was never affected.

## Decision

Confirmation-class prompts route on the **resolved confirmed content** instead of the bare prompt text (`internal/router/confirm.go`).

**Detection.** A prompt opening with a `rules.confirmation_phrases` entry (word-exact, case-insensitive, longest first, chainable: "yes, please go ahead and do it" → pure) is a confirmation. Anything left after the phrase is its **scope** ("yes, do the big refactor" → "do the big refactor"); politeness/flow connectors ("and", "please", "then", …) are consumed between phrases, and leading filler ("with", "the", …) is stripped from the scope ("proceed with the refactor" → "refactor").

**Resolution** of what is being confirmed, in order:

1. the prompt's own scope (user words) — new direction the user just gave;
2. the last agent message, when it proposes **new scope** (a `rules.confirmation_new_scope_markers` hit: "want me to also rename the test file?") — the question is what is being confirmed;
3. the **pending task**: the last non-confirmation user message (chained confirmations walk back past their siblings; a tail-bearing one contributes its scope as the task);
4. the last agent message as-is (a plan/proposal with no user task behind it);
5. the prompt itself when there is no history — status quo.

**Scoring** of the resolved scope (`confirmationDecision`):

- Escalate markers in **user-authored** scope are conclusive — a confirmed task's hard/complex words are the only signal the user gave. One escalate verb scores 4 < `escalate_threshold` 6, so left to the soft scorer a marker would never bind on short scopes; that *is* the bug ("refactor the whole dispatch layer" is 7 tokens).
- **Agent-authored** scope (case 2/4) routes exactly as if the question text were typed directly (keywords hard, verbs soft, marker-aware shortcut) — agent prose uses words like "reviewed"/"planned" incidentally, so its verbs must not bind.
- The short-prompt shortcut is **marker-aware instead of unconditional**: short confirmed content without hard markers stays local ("yes, list the files too") — the over-escalation guard.
- Everything else is the ordinary rules layer on the resolved text — a long confirmed task routes exactly as the same text would typed directly, and the model classifier (fallback for inconclusive scopes) classifies the *resolved content*, not "proceed".

Explicit pins (`/escalate`, `/primary`), single-turn overrides, auto-sticky and `ESCALATION_WAITING` are checked before the router and are unaffected. The behavior shows up in `milk.router.decisions` under the new `rule="confirmation"` label. Knobs follow the `rules` block conventions (configurable lists, built-in English + Italian defaults, "lists are replaced not merged").

## Consequences

- "Proceed" on a hard task escalates; "proceed" on a small task stays local; a small new-scope tail stays local even mid-hard-session. Unit scenario matrix in `internal/router/confirm_test.go`; output-quality coverage in `eval/scenarios/routing.yaml`.
- A bare "yes/do it" confirming a *small sub-step question without new-scope markers* inherits the pending task's routing (deliberate: bare affirmations are ambiguous, and continuity with the hard task is the safe bias — auto-sticky keeps such sessions on escalation anyway). Use a tail ("yes, rename it") to direct new scope, which routes on its own words.
- Direct prompts keep the unconditional shortcut: "refactor the whole dispatch layer" typed fresh still routes by today's rules. Only the confirmation class routes on content — the minimal delta the issue asked for. (Turning the shortcut marker-aware for *all* prompts would change established behavior broadly; revisit separately if wanted.)
- Detection is lexical, not semantic: a confirmation must *open* with a lexicon phrase ("proceeding with the fix" is not one). Unrecognized confirmations ("option b") fall through to ordinary routing — no worse than before.
