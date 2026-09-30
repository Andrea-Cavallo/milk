# 4. Context Handoff via --append-system-prompt-file (split static/dynamic)

Date: 2026-05-05
Updated: 2026-06-03, 2026-09-26 (resumed turns — see Update below), 2026-09-30 (corrected the
"two flags" claim below — see Update)

## Status

Accepted — updated to split-file approach

## Context

When escalating from the local agent to Claude, the local conversation history must be transferred. Options are: (a) a separate LLM reformulation call, (b) template-based summarization, or (c) passing the raw transcript to Claude directly.

The original implementation passed a single `--append-system-prompt` (later `--append-system-prompt-file`) containing everything: identity block, instructions (with a per-turn nonce), and the dynamic summary. This guaranteed a cache miss on every turn because the nonce changed and `LastLocalSummary` changed on each primary→escalation transition.

## Decision

Conceptually, split the context into two parts with different cache-sensitivity:

1. **Static part** — `BuildStaticContext`: identity block + `NeedInstruction` + `MemoryInstruction` + percepts. The identity block (telling the agent it runs inside milk) is always present, even on resume/continuation turns. Uses a per-session stable nonce (`sess.EscalationNonce`, generated once at `ContextModeFirst`). This content is byte-identical across turns *when nothing in it has changed*, and is intended to hit Claude's prompt cache.

2. **Dynamic part** — `BuildDynamicContext`: escalation brief + current need + `LastLocalSummary`. On `ContextModeFirst` when a prior escalation session exists (fresh-start forced by staleness), `LastEscalationSummary` is also injected as a `[Prior escalation session summary]` block so Claude retains key context from the dropped session. Changes per turn but is suppressed when content is unchanged (hash guard).

Claude orients itself from this context without a separate reformulation step.

**Implementation note (see 2026-09-30 Update):** the two parts are concatenated into a single
file behind one `--append-system-prompt-file` flag, not passed as two separate flags — the real
Claude CLI only honors the last such flag when more than one is given, so a genuine two-flag
approach silently drops the first flag's content. The static part is still a byte-identical
*prefix* within that one file when unchanged, so the caching intent above is only partially
realized in practice — see the Update for what this means and what was and wasn't verified.

## Consequences

Cache hits on the static instruction prefix across all resume and returning turns. Only the dynamic summary (small, frequently empty on RESUME turns) changes between turns. The nonce must now be persisted in the session file (`EscalationNonce`) rather than regenerated per turn. The `BuildContext` function is kept as a deprecated compatibility wrapper over the two split functions.

## Update (2026-09-26): resumed turns

The split files only reach Claude on a session's **first** request. Claude Code records the system prompt then and replays it on every `--resume` (`--system-prompt-snapshot`, default on), ignoring `--append-system-prompt-file` until a compaction re-records it from the compacting launch's files (verified live on Claude Code 2.1.283). The dynamic file sent on resumed turns, and instruction re-injection, therefore never took effect.

On resumed turns milk now:

- still passes the **full static block** as a file (inert unless the turn compacts, in which case it is what gets re-recorded);
- prepends context that is new for the turn — the dynamic content, plus the static instructions when re-injecting — to the **user message** in a `<milk-context>…</milk-context>` block. It persists in conversation history and costs no cache invalidation of the recorded prefix. The hash guard now applies to this block (skipped when identical to the previous resumed turn's) and no longer to first-turn files.

`--system-prompt-snapshot off` was rejected: it re-reads the files every turn, breaking the prompt-cache benefit this ADR is about, and would drop the instructions on any turn where the file is suppressed.

## Update (2026-09-30): corrected "two separate flags" claim, and a live measurement attempt

A prompt/context management review (`docs/prompt-context-management-review.md`) found that this
ADR's Decision section claimed context is passed as "two separate `--append-system-prompt-file`
flags," each hitting Claude's cache independently. That was never what the code does:
`appendContextFiles` (`internal/agent/claude/claude.go`) has always concatenated static and
dynamic content into **one** temp file behind **one** flag — its own doc comment explains why:
the real Claude CLI only honors the *last* `--append-system-prompt-file` flag when more than one
is passed, so a genuine two-flag approach would silently drop the static file's content entirely.
The Decision section above has been corrected to describe this.

The practical consequence: the static content is still a stable, byte-identical *prefix* within
the single combined file when nothing in it changes turn-to-turn, so the caching intent isn't
abandoned — but whether that prefix actually earns a cache hit depends on the CLI/API tokenizing
the combined file's content from byte zero, a weaker and previously-unverified guarantee than
"two independent flags" implied.

**Live measurement attempted:** two fresh (`ContextModeFirst`) escalation calls were run
back-to-back against the same repo with identical static content (`claude-mimo`, a
`claude-cli`-provider agent routed through a third-party gateway at `api.xiaomimimo.com`, not
directly against Anthropic's own API), with `otel.log_context: true` and inspecting
`~/.milk/claude_debug.ndjson`'s `usage` fields on the `result` event. Both calls reported
`cache_creation_input_tokens: 0` and `cache_read_input_tokens: 0` — including the *first* call,
which should show cache creation greater than zero if prompt caching were active at all for this
path. This means the measurement is **inconclusive**: it doesn't confirm the merged-file approach
gets a cache hit, but it also doesn't confirm it doesn't — the gateway in front of this
particular agent may simply not surface (or support) Anthropic-style prompt-cache accounting in
its usage reporting, independent of anything milk does. Re-measuring against a `claude-cli` agent
backed directly by Anthropic's own API (not a third-party gateway) would be needed for a
conclusive answer, and is left as a follow-up rather than blocking this correction.
