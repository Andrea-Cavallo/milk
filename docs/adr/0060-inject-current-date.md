# 60. Milk Injects the Current Date into Every Agent Context

- **Status:** accepted
- **Date:** 2026-10-17

## Context

LLMs do not know what day it is. A model's only inherent temporal knowledge
is its training cutoff; nothing in the chat-completions/Converse wire
contract tells it the current date. Left to themselves, models confidently
answer date questions from their cutoff — the failure this ADR replaces was
observable in milk itself:

- a memory percept froze "today is 2026-09-29" at the moment the user
  corrected the model, and went silently stale — immutable user-authored
  snapshots are the wrong mechanism for a value that changes daily;
- obtaining the date cost a whole background-agent fork ("tell me what day
  is today"), plus an extra `date` tool round trip whenever a model thought
  to check.

Milk had no date injection anywhere: grepping the prompt builders
(`buildSystemPromptOpts`, `BuildStaticContext`, `BuildPrimaryStaticContext`,
`BuildDynamicContext`) found no date/today line. Every mainstream harness —
Claude Code, Cursor, the OpenAI/Anthropic docs' own guidance for
time-sensitive prompts — solves this the same way: the *host* prepends the
date. Provider built-ins (e.g. Gemini's current-time tool) don't exist on
OpenAI-compatible or Bedrock backends.

## Decision

milk renders a single **current-date context line** and injects it into every
agent's system context itself:

```
Current date: 2026-10-17 (Saturday), local time zone CEST (UTC+02:00).
```

ISO-8601 date + weekday (people say "next Tuesday") + local zone name and
UTC offset (transcripts, `git log`, and file mtimes are local time).

### One helper, two prompt builders

`escalation.DateLine(now time.Time)` (`internal/escalation/date.go`) is the
single source of the line — shared because `internal/agent/local` already
imports `internal/escalation` (same precedent as `ConfigWriteWarning`).
It takes the clock as an argument so tests pin the exact format,
including negative and half-hour offsets.

Injection points:

- **`escalation.BuildStaticContext`** — appended directly after
  `identityBlock`, *before* the `ContextModeContinuation` /
  `injectInstructions=false` early returns, so all four `ContextMode`s and
  both instruction gates carry it (identity-only blocks included). Covers
  Claude-CLI escalation and — via `BuildPrimaryStaticContext` — the
  aider/smolagents subprocess primaries.
- **`local.buildSystemPromptOpts`** — appended to the prompt core after the
  tier/guidance assembly and before the working-directory/project-instructions
  block. Because it lives in the core (not the sheddable guidance layer),
  every tier including `minimal`, every role (primary, escalation, workflow),
  and every context-reduction level of ADR-0058 keeps the date.

Both builders re-render **per turn** from `time.Now()`, so a session crossing
midnight self-corrects on the next turn instead of going stale.

### Date granularity is a caching requirement

The line deliberately contains **no `HH:MM`**. Both injection points sit in
static prompt prefixes that providers cache by prefix: Claude's cached
`--append-system-prompt` prefix, and the Bedrock `cachePoint` breakpoint
anchored after the stable system-prompt prefix (milk's prompt-caching
feature). Content that changed every render would invalidate those caches
every turn. At date granularity the prefix goes stale only at midnight — or
at a DST boundary, where the zone abbreviation/offset legitimately changes —
which is the accepted cost of a fresh date per turn. Anything wanting
time-of-day precision can still shell out to `date`; the injected line
covers the everyday questions ("is it Friday?", "how old is this issue?").

## Consequences

- Every milk agent — local, Bedrock, Claude-CLI escalation, subprocess
  primaries, workflow executors, background sub-agents, ACP clients —
  answers date questions correctly with zero round trips (~15 tokens).
- The `Current date:` line is asserted by tests at three levels: exact
  format with fixed clocks (`internal/escalation/date_test.go`), presence
  across every `ContextMode`/instruction gate, and presence across all
  tiers/roles plus survival under `LevelToolsCompact` reduction
  (`internal/agent/local/date_prompt_test.go`). A same-day render is pinned
  to be byte-identical and a midnight crossing to roll the line over — the
  cache-stability and freshness properties fail loudly if regressed.
- Prompt-cache invalidation now happens once per day (midnight, or rarely a
  DST shift) instead of only on session start — measured, accepted cost.
- Stale date percepts are no longer load-bearing; the memory system keeps
  facts, not clocks. The `date` shell tool remains as the precision
  fallback, but is no longer the first line of defense against
  hallucinated dates.
