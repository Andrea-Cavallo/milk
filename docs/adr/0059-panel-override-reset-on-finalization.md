# 59. Panel Manual Override Resets When Content Finalizes

- **Status:** accepted
- **Date:** 2026-09-29

## Context

ADR-0044 made side-panel auto-open override-aware: once the user explicitly
shows or hides a panel (`/panel <name>` or F1-F4 → `handlePanelCmd` →
`model.panelManualOverride`), no automatic trigger touches that panel again
for the rest of the session.

That stickiness solves the real problem — live content must not keep popping
open a panel the user just dismissed — but it never expires. Closing the
tasks panel while a task is open locks it out even after every task is long
done; closing the background panel mid-run means a spawn the next day can't
surface either. The override conflates "don't interrupt me about *this*" with
"never manage this panel again", so one dismissal permanently blinds a
panel to genuinely new activity.

## Decision

An explicit close now sticks only while the panel's content stays live.
When the content *finalizes* — empties, completes, or reaches a done state —
the override is dropped, and the next "content became active" trigger
auto-opens the panel again (with the usual ADR-0048 transition toast).

### Finalization predicates

`(*model).panelContentFinalized(region)` (`panel_common.go`) is the single
source for what "finalized" means per panel, mirroring each
`build*PanelLines` view of active content:

| Region | Finalized when |
|---|---|
| memory | no percepts in either scope (context bricks are session-summary state, not activity) |
| tasks | every session + global task is `done`, or there are none (blocked/in-progress/pending stay active) |
| background | no job is `JobRunning` (an empty list — all jobs finished and drained — included) |
| workflow | no run in flight and the state is nil, nameless ("no active workflow"), or `Role == "done"` — `interrupted` / `waiting for your answers` stay active |

`(*model).resetManualOverrideIfFinalized(region)` drops the override iff it
exists *and* the content has finalized — a no-op otherwise.

### Where the reset runs

Two classes of call sites:

1. **Close time** — `handlePanelCmd`, whenever a toggle *closes* the panel:
   closing a panel whose content has already settled must not lock it out
   of its next content either. (Toggles that *open* a panel keep recording
   the override unconditionally, as in ADR-0044 — that direction only ever
   suppresses no-op re-opens.)
2. **Content-finalization events** the TUI observes:
   - `taskStoreChangedMsg` — any store mutation can be the one that leaves
     every task done (the store's `SetOnChange` covers create/update/
     complete/delete, so no completion path escapes the check);
   - `backgroundJobDoneMsg` — each job's `SetOnDone`; when it is the last
     running job, the panel is finalized;
   - `workflow.WorkflowDoneMsg` — after `Role` is set to `"done"`;
   - `/workflow clear` — both paths (state file cleared, and the
     "no saved workflow state" short-circuit) empty the content;
   - `resolveForget` — `/forget` deleting the last percept empties the
     store.

**Ordering rule at event sites:** call `autoOpenPanel` *first*, the reset
second. The finalizing event itself must never force-open a panel the user
closed mid-run (completion is not "new content"); only content arriving
*after* the reset does.

Close time intentionally does not need the same ordering: it never opens
anything — it only decides whether the just-recorded override survives.

### Still per-session, still one map

Nothing is persisted; a fresh session starts with no overrides exactly as
before. The only change is the lifetime of an entry: bounded by the content
it was suppressing rather than by the session.

## Consequences

- The full cycle now works as users expect: close a panel while its content
  is live → it stays closed through that content's finalization → the next
  new task/job/workflow opens it again with the transition toast.
- Closing an already-settled panel no longer records a durable override
  (behavior change vs ADR-0044, covered by
  `TestPanelClose_FinalizedContentDoesNotStick`); the other ADR-0044
  invariants — sticky while live, open-direction still recorded, toast only
  on closed→open transitions — are pinned by the updated tests in
  `cmd/milk/panel_auto_open_test.go`.
- Finalizing content re-opens nothing by itself; tests assert the panel
  stays closed at the finalizing event (`…Finalize`/`…ResetsManualOverride`
  cases) so a reordering of the auto-open/reset calls fails loudly.
- Known gap, deliberate: the memory panel has no auto-open trigger at all
  (percept writes from the model's `record_memory` happen inside the
  agent's tool loop with no host event), so its reset — wired at `/forget`
  and at close time — is bookkeeping today. If a memory auto-open trigger
  is ever added, the finalization half is already in place.

## Amendment (2026-10-24): the expiry is the feature-off fallback

[ADR-0078](0078-persisted-panel-state.md) made panel state a durable
preference behind `remember_panel_state` (default on). With it on, a manual
toggle survives content finalization for **every** panel — this ADR's
expiry no longer runs. With `"remember_panel_state": false` the semantics
above stand exactly as written, and their tests
(`TestPanelClose_FinalizedContentDoesNotStick` and the
`…Finalize`/`…ResetsManualOverride` cases, all run with the feature off)
pin them. The "known gap" is also closed: the memory panel now auto-opens
on content presence (its 5s poll detects percept arrival), and the memory
panel's closed-by-default, content-driven open is part of ADR-0078.
