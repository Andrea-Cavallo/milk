# ADR-0073: Team live-run fixes — dataflow fan-out scheduling, board-driven item gating, retry reopen

*Date: 2026-10-21*

## Status

Accepted

## Context

The first live run of the `team` workflow (session `7c0abf71`, a six-item game
build, 3 h 12 m) finished and its board was consistent, but the review found
three engine problems that cost real time or silently disabled a feature, plus
smaller rough edges:

1. **Refinement retries were dead.** A worker marks its item `done` before its
   evaluation, and `Board.Claim` refuses a `done` item even for its owner. When
   an evaluator said `needs_refinement`, the retried worker's mandatory
   `board_claim` was refused and it did no work.
2. **A second pass re-ran the whole fan-out.** `more_work` reopened one item,
   but all six were dispatched again; five workers answered `SKIPPED` and their
   evaluators still ran (36 requests, ≈38K completion tokens, ≈11 min). Item 6,
   the only item with real work, depends on all five, so its rework could not
   start until their wasted chains had finished — it began 10.7 min after the
   stand-up instead of at once. The same dead retry had earlier turned item 6's
   refinement attempt into a no-op even though its evaluator had already
   diagnosed the defect (and the fix); the coordinator then rediscovered it.
3. **Dependency waves were barriers.** Items were scheduled wave by wave, so an
   item waited for unrelated members of an earlier wave. In this plan the effect
   was small — item 6 depends on every other item and so waits for the slowest
   (item 3) either way; only item 5 (depends on item 1 alone) started ≈3 min
   later than possible — but it is a general inefficiency for plans with partial
   dependencies.
4. Rough edges: the inner retry loop and the outer pass loop both exposed a
   variable called `pass` (the coordinator's "pass 2/3" read oddly and the panel
   labelled both loops "pass N"); board labels kept the plan's `**bold**`; a
   model repeating a `board_post` duplicated it; token usage landed in one
   `primary:workflow` bucket with no per-role split.

## Decision

Fix each in the engine as a generic, definition-driven capability — no code
path keys on a workflow name; `team.yaml` just uses the new fields.

- **Retry reopen.** A verdict rule may carry `board_status` (validated: board
  definition, inside a `parallel_group`, settable status). When it fires, the
  current fan-out item's board entry is set to that status; `todo` releases the
  claim, so the retried worker claims it again. `Board.Claim` additionally
  treats a re-claim by the owner of an `in_progress` item as idempotent (retry
  and resume), not only a still-`claimed` one.
- **Item gating.** `parallel_group.skip_if_board_status` leaves out items whose
  board status is listed (`[done]` for `team`). Skipped items are not
  dispatched, count as finished for their dependents, and keep the result the
  group's `save_as` aggregate held from the previous run, so the aggregate
  always describes every item. The coordinator reopens what needs rework
  (`todo`); everything else is left alone — no `SKIPPED` worker turns, no
  due-diligence evaluator turns.
- **Dataflow scheduling.** (A general improvement; it does not shorten this
  run's critical path, see above.) The fan-out starts an item as soon as its own
  `depends_on` items have finished, bounded by `max_concurrency`; waves remain
  only as the up-front cycle / missing-reference check. A failed dependency
  still releases its dependents (unchanged).
- **Names and labels.** `team.yaml`'s inner loop is `worker_attempt_loop` with
  `iteration_var: attempt`; the panel labels any `<…>_<noun>_loop[N]` as
  "<noun> N" from the id itself. Board labels are de-markdowned
  (`cleanBoardLabel`); an exact repeat of a recent finding from the same
  sender and scope is absorbed.
- **Attribution.** The interpreter puts the stage's role on the turn context
  (`workflow.WithTurnRole`); a workflow step executor tags its usage
  `…:workflow:<role>`, which keeps prefix queries working.
- **Designer guidance** in `team.yaml` discourages a catch-all integration item
  that depends on every other (it serializes the tail even with dataflow
  scheduling).

## Consequences

- A `team` run does less redundant work in later passes. Replaying the first
  live run's timeline: ≈5–7 % fewer completion tokens, ≈6–8 % fewer fresh prompt
  tokens, ≈2–3 % fewer cached input tokens, and ≈14–24 min (≈7–12 %) less wall
  time — the dominant cost, the plan's serial item-3 → item-6 chain, is untouched
  (see the planner guidance).
- Existing definitions are unaffected: both new fields are optional, an unset
  `skip_if_board_status` dispatches every item, and a definition without
  `board_status` behaves as before.
- Scheduling is by item, so progress events interleave differently; the panel
  and plan views already key off active/completed path snapshots and need no
  change.
- The other built-ins: `swarm` gains the dataflow scheduling and the planner
  guidance (it fans out with `depends_on` but has no board and no repeated
  fan-out, so the retry-reopen and item-gating fields do not apply); `dev` and
  `pair` are sequential and unaffected except for the shared fixes — per-role
  token tags, logging, and the snapshot-root rule (PR #300), whose absence made
  *every* built-in's panel draw a running workflow as done (pinned by
  `TestWorkflowPanel_BuiltinsShowRunInProgress` over dev, pair, swarm and team).
- Not changed: the milk log stays off unless `otel.enabled` is set, and debug
  log rotation can split a long run's evidence across files.
