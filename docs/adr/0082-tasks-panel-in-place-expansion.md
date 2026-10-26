# 82. Tasks Panel In-Place Expansion (Memory-Panel Parity)

- **Status:** accepted
- **Date:** 2026-10-26

## Context

The memory panel solved "long content in a narrow panel" with in-place
expansion: rows and IDs come from one function (`memoryPanelRows`), a
double-click toggles a row between a collapsed line budget and its full
wrapped text, and no separate detail view exists. The tasks panel had none of
it — one ellipsized line per task (`renderTaskLine` truncates with `…`), and
`regionTasks` was not even in the panel click dispatch, so long task titles
were unreadable in the panel and the hint row advertised no interaction.

## Decision

Mirror the memory panel's mechanism exactly, in `cmd/milk/panel_tasks.go` +
`cmd/milk/panel_select.go`:

- **Shared rows + IDs.** `taskPanelRows(ts, inner, expanded)` returns the
  rendered lines *and* a parallel IDs slice (the task ID per task row, `""`
  elsewhere). Rendering (`sidePanelLines`) and hit testing
  (`handleTasksPanelClick`) call the same function, so expanding a title can
  never shift click targets. Wrap widths come from one shared `taskWrapWidths`
  (issue #212's drift lesson).
- **Expansion is a line budget, not a view.** Collapsed rendering stays
  byte-identical (one line, `…`); expanded, the full title wraps via the
  shared `wordWrap` with continuation lines indented to the title column.
  `taskExpanded` is a single string like `memoryExpanded` (one row expanded at
  a time, `""` = all collapsed); it is not persisted, matching memory.
- **Double-click toggle, disambiguated arm key.** The 400 ms arm/trigger runs
  on `m.lastPanelClickID`, which memory, background and workflow clicks share;
  tasks arm under a `"task:"` prefix (the `"bg:"` precedent in `attach.go`) so
  task IDs can never collide with percept IDs.
- The panel gains a `F2 close · double-click expand` hint row, matching
  memory's; the key help's stale "print memory entry details to transcript"
  line is corrected (expansion has been in-place for a while) and now covers
  both panels.

## Consequences

- Long task titles are fully readable without leaving the panel; the
  interaction is identical in the two panels that need it.
- Expanding shifts rows below the task — safe by construction (IDs track
  rows), scroll clamping already handles the grown line list.
- A future panel that grows rows (notes, logs) should follow this
  rows+IDs pattern rather than inventing a detail view.
