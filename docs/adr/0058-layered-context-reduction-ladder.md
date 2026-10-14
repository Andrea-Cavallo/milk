# 58. Layered context reduction ladder

Date: 2026-10-14

Status: Accepted

## Context

Issue #236. The payload analysis in [`docs/internal/analysis/context-bloat-analysis.md`](../internal/analysis/context-bloat-analysis.md) measured a representative fresh-session primary-agent request at 15,180 chars: tools JSON array ~63%, system prompt + instructions ~28%, conversation history ~4%. **~94% of every request is static overhead**, and history — the actual signal, the only thing that changes turn to turn — is less than one twentieth of it. [`docs/internal/analysis/prompt-context-management-review.md`](../internal/analysis/prompt-context-management-review.md) confirmed the picture across all four prompt surfaces and catalogued the prior art (Claude Code, OpenCode, MiMo-Code).

The irony: history was the *only* layer milk knew how to shrink. It has three compaction triggers (the running turn's 85%-of-(window−output-reserve) token trigger, the turn-boundary summarize-what-would-be-dropped, the payload-size safety net) plus hard drop-oldest — all aimed at the smallest, highest-value part of the payload, while the two bloat layers rode along at full fidelity on every request. For a small window (8K–32K) the tools array alone can eat most of the usable context before the first user message. The only lever was static: the manual recipe in `docs/guides/providers.md` (`system_prompt_tier: "minimal"` plus `limits.included_tools` narrowed to 7–8 tools), a guess fixed at config time that either wastes budget (window barely used) or isn't enough (window overflowing mid-session).

## Decision

A three-layer static-side reduction ladder that escalates with context pressure and sits *above* the history layers, ordered by value per token shed — the cheapest capability is lost first:

| Pressure | Layer | Reductions |
|---|---|---|
| ≥ 55% | `instructions` | system-prompt tier steps down one level (full→standard, standard→minimal); the optional per-turn guidance blocks (background-agent, task-tool, memory-tool) are dropped |
| ≥ 65% | `tools_compact` | tier forced to minimal; the project-instructions (AGENTS.md/CLAUDE.md) block is head+tail-trimmed to `instructions_trim_chars` (default 4000); tool descriptions are one-lined (first sentence, ≤120 chars) |
| ≥ 75% | `tools_prune` | the tool set is pruned to the tools observed in use this session plus `protected_tools` (default `["bash","read_file"]`) |

- **Pressure** is prompt tokens ÷ (context window − output reserve) — the same window-minus-reserve denominator `AgentCompactionTriggerTokens` already derives (shared `contextOutputReserve`: models.dev output limit capped at 32K, 20K default, ≤ ¼ window), so the ladder's thresholds and the 85% history trigger live on one scale. When the window is unknown (no `context_window_tokens`, no models.dev match) or `mode` is `"off"`, the ladder is disabled and behavior is exactly as before.
- **Cumulative and request-scoped**: each layer includes the reductions of the ones before it; the level is recomputed at every tool-loop iteration from the pristine system prompt and full tool list (`sysPromptBuilder.render`, `reduceTools` transform copies — `msgs`/`tools` are never mutated). Nothing is persisted, so a layer eases back by itself once compaction or tool output frees room, and a later turn starts from full fidelity again.
- **Reuse over reinvention**: the instruction layer is dynamic `system_prompt_tier` stepping; the prune layer is a dynamic `included_tools` narrowed to observed-in-use (a session-wide `toolUseTracker` feeds it); the denominator is the compaction trigger's. The existing history layers stay deeper and unchanged — turn-boundary/85%-token/payload-triggered compaction, then hard drop-oldest as the final safety net.
- **Config**: per-agent `context_reduction` block (`mode` `"auto"`/`"off"`, `instruction_threshold_pct` 55, `tool_compact_threshold_pct` 65, `tool_prune_threshold_pct` 75, `instructions_trim_chars` 4000, `protected_tools`); all fields optional, unknown `mode` is a config-load error. An OTel counter `milk.inference.context_reduction` with a `layer` attribute (`instructions`|`tools_compact`|`tools_prune`) records when a layer is active.
- **Scope**: local agents (OpenAI-compatible, Bedrock native, Responses API) via `localRunner`; CLI/subprocess runners are unaffected. Background jobs get the tool-side layers only (their prompt is already minimal). Custom `prompt`/`prompt_file` text and percepts/current-need messages are never dropped.

Implementation: [`internal/contextreduce/contextreduce.go`](../../internal/contextreduce/contextreduce.go) (pure level math and trim/compact/prune helpers), [`internal/agent/local/reduction.go`](../../internal/agent/local/reduction.go) (request-scoped assembly, tool-use tracking), [`internal/config/config.go`](../../internal/config/config.go) (`ContextReductionConfig`, `AgentContextReduction`, `AgentContextPressureDenominator`).

## Consequences

- Small windows get dynamic relief with no config at all: the ladder defaults on and does statically what the small-model recipe used to prescribe, tuned to the moment's actual pressure instead of a startup guess.
- Large windows are unaffected — below 55% pressure nothing changes, and a session that compacts back under the thresholds immediately runs at full fidelity again (the reduction is a per-request transform, not state).
- Capability degrades gracefully and reversibly: first the prose of tool descriptions (one-lining), then tools the session has never used (the protected core and everything observed in use survive). The model loses polish before it loses abilities.
- The ladder never touches history, so it is fully reversible: nothing it does loses information the next request couldn't reconstruct, and the only lossy layers remain the pre-existing history compaction/drop-oldest, deeper and unchanged.
- Unknown window or `mode: "off"` reproduces the prior behavior exactly, so the change is a no-op for anyone it doesn't fit; the same window knowledge that powers `ctx:x/y` display and the compaction trigger now also gates the ladder.
