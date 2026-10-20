# ADR-0071: Shared workflow board + the `team` workflow

- **Status:** Accepted
- **Date:** 2026-10-21
- **Issues:** [#197](https://github.com/scoutme/milk/issues/197) (design), [#297](https://github.com/scoutme/milk/issues/297) (fan-out races, Phase 0)
- **Related:** [ADR-0039](0039-native-workflow-engine.md) (the engine), [ADR-0043](0043-background-agents.md) (jobs)

## Context

`swarm` maximizes parallelism on *independent* items. Issue #197 asked for the other shape: a
coordinated swarm for **loosely coupled** work — a shared kanban-style board as the single source
of truth, claims to prevent duplicate work, findings to unblock teammates mid-flight, and a
coordinator reconciling between passes. Design questions the issue listed: board as tools vs
prompt-injected state, coordinator role, atomic claiming, size budgets, persistence, naming.

A design pass grounded in the engine (posted on the issue) settled them before any code, and
surfaced two hard facts that shaped the result:

1. **The engine has no conditional stage and no shared writable state inside a fan-out.**
   `run_unless_marker_in` is static and reads one parent var; inside a `parallel_group` it reads
   the item's *private* vars copy, so sibling writes are invisible. Faking claims with
   `agent_turn` + `save_as` cannot short-circuit an already-claimed item — rejected outright.
2. **Fan-out items shared one runner adapter** — session ID, nonce, one-shot flags and the
   shared `*local.Agent`'s pending-image field raced across item goroutines. Phase 0 (#297,
   merged first) closed that with per-item clones (`workflow.CloneableTurnRunner`) and per-call
   ctx-carried image parts, establishing the discipline everything below follows: *concurrent
   items share nothing mutable except the board, which is mutex-protected by construction.*

## Decisions

### 1. The board is a generic engine capability, not a workflow feature

`internal/workflow/board` knows nothing about which workflow uses it. The definition flag
`board: true` opts any YAML workflow in; `board_seed` is an ordinary stage kind; the tools and
`{{.board}}` injection key off "is a board on this turn", never off a name. **No workflow name is
branchable anywhere in the interpreter** — `team` is a YAML file (`definitions/team.yaml`) using
those primitives, exactly like `swarm` uses `parallel_group`.

### 2. Read path: injection + tools, layered

- `{{.board}}` renders into **every** stage prompt at stage start (rides the engine's existing
  12K-per-var / 30K-total budgets; `missingkey=zero` + an explicit empty render keep board-less
  definitions clean).
- `board_get` / `board_claim` / `board_update` / `board_post` are registered **per call**, only
  when the turn's ctx carries a board (same dynamic-backing-store treatment as task/otel tools,
  appended after `filterTools` and pre-marked observed so ADR-0058 pruning keeps them). Handlers
  read the board and the acting identity (`worker·Item-2`) back off the same ctx — nothing is
  written to any shared agent instance (#297's rule).
- **CLI-provider roles get the snapshot only** — they cannot receive milk's tools. Honest v1
  limitation, documented in `team`'s worker prompt with an explicit fallback line.

### 3. Claims: in-process CAS, attributed by ctx identity

`Claim` is compare-and-set under one mutex — first claimer wins; the holder is returned to the
loser, idempotent for the same identity, `done` never claimable. This is race-free *because*
fan-out bodies are goroutines in one process and tool handlers are direct calls (no fs race
window, unlike bash-claimed files). Identity is threaded ctx-plain by the interpreter
(`role·Item-N`); no shared-runner mutation.

### 4. Persistence: write-through sidecar, not checkpoint payload

`<checkpoint-path>.board.json` (sibling of `*.workflow[.id].interp.json`), atomic tmp+rename on
**every** mutation. Parallel groups checkpoint only leaf-atomically — a mid-fan-out crash would
lose live claims if the board rode the checkpoint. Resume loads the sidecar; if it is gone but
the checkpoint still holds the `board_seed` leaf, the item skeleton is rebuilt from that value
(findings in a deleted sidecar are unrecoverable by construction). Caps: 200 findings, 500-rune
finding text, 400-rune notes, compact render (~8K) — long runs degrade, never blow the prompt.

### 5. Verdicts: declared keys are real (`ParseVerdictKey`)

`Stage.Verdict` looked like a free-form map but `ParseVerdict` only ever recognized three
hard-coded keywords, so a stage could not prompt for its own words — `team`'s stand-up needs
`all_done` / `more_work`. The stage path now matches **the stage's own declared keys** (end-scan,
longest-key-first, `unknown` never scraped from prose) and falls back to the historical
keywords only when no declared key matched — full backward compatibility for `dev`/`pair`/`swarm`.

### 6. `team` (the YAML)

Designer (same NO_QUESTIONS flow) → `board_seed` → bounded pass loop {
  `parallel_group` over items [worker+evaluator retry loop] , coordinator **stand-up**
} → final evaluation → optional implementer pass. The stand-up verdict breaks the loop
(`all_done`) or re-queues with context (`more_work`), which is how blocked items get un-blocked
without any engine-level dynamic re-waving. Claims stay advisory on contention: a loser spends
one cheap turn to skip (no per-item conditional-skip yet — see below).

## Rejected / deferred

- **Fake `board_seed`/`board_claim` with `agent_turn` + `save_as`** — impossible (fact 1 above).
- **Peer-to-peer messaging, cross-process claiming** — single process is the execution model;
  revisit only if items ever become subprocesses.
- **Engine-side per-item conditional skip, board-aware `from:` collections, dynamic re-waving**
  — the prompt-convention skip costs one cheap turn on rare contention; premature otherwise.
- **Board panel view (Phase 3)** — live board renders fine via `board_get` / `{{.board}}`; a
  dedicated TUI row surface is an on-demand follow-up.

## Consequences

- `swarm`-class runs unchanged unless they opt in; every existing definition stays valid
  (`Validate` rejects `board_seed` without `board: true`, not the reverse).
- `go test -race` over `./internal/workflow/...` and `./cmd/milk` is part of the contract
  (#297's acceptance) — the board is the one deliberately shared mutable thing, so it must
  stay mutex-first.
- Custom verdict keys are now available to *any* definition, not just `team`.
