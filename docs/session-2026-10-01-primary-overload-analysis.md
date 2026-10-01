# Session 2026-10-01: primary-model overload, loop recovery, and payload trim thrash

Date: 2026-10-01

## Method

Investigation of a single live dogfooding session (`64d7e2fc-2a05-4699-b46f-6913b640a200`,
2026-10-01 00:49–04:25, this repo) that the user flagged as "took a bit longer than expected."
No part of this report is guessed — every claim below is backed by one of:

- `~/.milk/sessions/64d7e2fc-....json` (session history, turn timestamps)
- `~/.milk/sessions/index.json` (cwd → session → `last_used` mapping)
- `~/.milk/otel/metrics.jsonl` (cumulative OTel metrics, 5-min export interval; counters reset
  per metric series on process restart, which is how the mid-session restart was detected)
- `~/.milk/otel/logs.jsonl` / `traces.jsonl` (OTel structured events — memory consolidation only
  for this session; turn/loop/tool events are **not** OTel-instrumented today, see Gaps below)
- `~/.milk/otel/milk.log` (plain `slog` text log — this is where the actual loop-detection,
  duplicate-tool-call, and payload-trim WARN lines live)
- `~/.milk/config.json` (agent/model → role mapping)

## Setup

- `escalation_agent: "mimo-pro-local"` → model `mimo-v2.6-pro`
- primary/default local agent → model `mimo-v2.6-flash`
- Both are small self-hosted models. No Claude CLI/API involvement in this session.

## Headline finding: the router never escalated

Diffing `milk.router.decisions` across every 5-minute metrics export in the session window shows
**100% of routing decisions targeted `local` for the entire 3.5-hour session** — zero
escalations to `mimo-v2.6-pro`. The task carried entirely by the primary model included:
implementing a full UI feature (floating toast notifications, #162), a multi-angle code audit of
memory-tool availability across agent types, and a loop-detection-message audit — all genuinely
complex, multi-step engineering work, done by the smaller of the two configured models, without
ever handing off to the model configured specifically to receive escalations.

This is the central fact behind "it took longer than expected": a simple model was carrying a
complex task alone, by routing decision, not by necessity.

## Timeline, with evidence

| Time | Event | Evidence |
|---|---|---|
| 00:49–01:07 | Issue filing (7 GitHub issues), fast, normal | session history turns 0–9 |
| 01:29:30 | "start improvement plan" turn begins | session history turn 10 |
| 01:31:04 | Turn 13 lands ("M0 of the improvement plan is complete") | session history |
| 01:31:04 → 03:52:29 | **One continuous ~2h21m turn** implementing #162 | session history turns 13→15 |
| 02:05:15 → 03:50:50 | `payload exceeds limit, trimming history` fires on nearly every tool iteration; history grows 115 msgs/923KB → 285 msgs/3.15MB over the stretch, continuously re-exceeding the 921,600-byte (900KB) per-request cap and being re-trimmed. **No duplicate-call or loop signal fires in this window** — legitimate incremental progress, not a stuck loop. | `milk.log` lines ~24152–24262 |
| 03:52:29 | `loop: SIGNAL FIRED (cross-turn) signal=token_velocity confidence=0.9 message="high token burn rate"` — fires twice, right as the long turn lands | `milk.log` |
| 03:52:29 → 04:08:45 | Next turn (~16 min). **Five separate duplicate-tool-call loop detections**, each with crop+nudge recovery: 03:55:55 (57→20 msgs), 04:01:34 (108→44), 04:04:11 (86→45), 04:06:09 (72→46), 04:07:51 (59→47). Plus one `reasoning_chunk_flood` signal (5000+ reasoning chunks, confidence 0.85) at 04:01:45. | `milk.log` |
| 04:08:45 | Turn lands: "Filed both audit results as GitHub issues... #175, #176" | session history turn 17 |
| 04:13:53, 04:16:16, 04:17:31 | Three MCP-reconnect bursts (8 lines each, same 4 Cloudflare MCP servers) in ~4 minutes | `milk.log` |
| 04:17:36–04:17:48 | OTel cumulative counters reset (new `StartTime` on every metric series) — **the milk process restarted** here. No panic/fatal/crash logged anywhere nearby. | `metrics.jsonl` lines ~959–963 |
| 04:17:48 | "hi" → normal reply | session history turn 18 |
| 04:17:48 → 04:25:09 | 2 turns, latency 6,994ms and 12,048ms — fully normal | `milk.turns.latency_ms` histogram, final export |

## Root-cause verdict

**Primarily a model problem, not a milk-mismanagement problem.** The small primary model (a) was
never escalated despite the size/complexity of the task, and (b) did get stuck repeating identical
tool calls on 5 separate occasions within one 16-minute turn. But milk's own safety nets worked
as designed in both failure modes:

- The per-request payload trim kept each individual HTTP call under budget throughout the
  2-hour legitimate stretch, even as the underlying tool-call history grew unbounded.
- The duplicate-tool-call detector caught all 5 stuck-repeating episodes and recovered every one
  via crop+nudge — this is what let the turn finish with a real answer instead of hanging
  indefinitely.

One thing is a genuine milk-side cost worth optimizing, not a correctness bug: for ~1h45m
(02:05–03:50) nearly every tool iteration re-exceeded the 900KB trim cap and paid the
trim-and-resend cost again, immediately. The cap never actually got ahead of the growth — it
just prevented individual requests from blowing past it. That's expected per-request behavior for
the mechanism touched in `fix(local): anchor payload trim on the turn's own message, cap
unbounded tool results` (commit `3f09b1d`), but a 900KB cap re-applied every single iteration of
a long primary-agent tool loop has real recurring latency cost and may itself be shrinking the
model's effective context more aggressively than a one-shot summarizing compaction would.

The post-04:08 process restart (3 MCP-reconnect bursts) looks like a clean exit+relaunch (likely
a deliberate rebuild to pick up the just-implemented #162 code — this user always runs milk via a
fresh build), not a crash: no fatal/panic anywhere nearby in the log.

## Open items for escalation-mechanism enhancements

1. **Router never escalated across a 3.5h, clearly-complex session.** Worth checking whether the
   hard-threshold/default rules in `internal/router` have any signal at all for "turn has been
   running continuously for N minutes" or "payload has been re-trimmed N times this turn" — right
   now escalation appears to be purely pre-turn classification, with no mid-task
   complexity/duration signal feeding back into a self-escalation decision. The primary model
   *can* call `escalate(reason)` itself (self-escalation, per CLAUDE.md) — worth checking in a
   follow-up whether that path was ever offered/considered during the 2h21m turn or the 5x-loop
   turn, or whether the model simply never reached for it.
2. **Loop detector recovered 5 times in one turn without ever escalating or asking the user.**
   Current ladder is crop → nudge → (strong nudge) → terminate, same-model throughout. Is there a
   case for escalating to the configured escalation agent after N recovered loops within a single
   turn, rather than only cropping/nudging the same (already-stuck) model repeatedly?

## Open items for request-length / context-management enhancements

1. **900KB trim cap re-fired on nearly every iteration for ~1h45m straight** (see timeline). Measure
   whether raising the cap, or switching this path to the existing
   `trimLocalMessagesWithCompaction` summarization strategy (currently only invoked elsewhere —
   no `compaction`/`Summarize` log lines appear anywhere in this session), reduces the number of
   trim-and-resend cycles for long primary-role tool loops without losing correctness.
2. **No OTel instrumentation for turn/loop/tool events** — `logs.jsonl`/`traces.jsonl` for this
   session contain *only* `milk.memory.consolidation`/`milk.memory.recall`/`milk.memory.record`
   spans. All of the loop-detection, duplicate-tool-call, and payload-trim evidence above came
   from grepping the plain-text `slog` log (`milk.log`), not from structured OTel data, even
   though `internal/obs` already exports `milk.turns.latency_ms`, `milk.inference.latency_ms`,
   etc. as metrics. Loop-detection events (`SIGNAL FIRED`, duplicate-tool-call recovery, doom-loop
   gate) have no corresponding OTel log/span/metric — reconstructing this timeline required manual
   log scraping and cross-referencing against `milk.log` line numbers. A `milk.loop.events` OTel
   log record (or counter) keyed by `session_id` + `signal` would make this kind of investigation
   a single structured query instead of a multi-file manual correlation exercise.
3. **Mixed/concurrent-session log attribution risk**: `milk.log`/`metrics.jsonl`/`traces.jsonl`
   are process-wide, not per-session. A second, unrelated session (`61ad8f04`) was running
   background jobs concurrently during part of this window, interleaving `job=job_4`-tagged
   WARN lines with this session's own entries in the same file. Any future automated analysis of
   `milk.log` needs to filter on `job=`/`agent=`/`model=` attribution carefully, or (better) tag
   these WARN lines with `session_id` the way `milk.memory.*` OTel logs already are.
