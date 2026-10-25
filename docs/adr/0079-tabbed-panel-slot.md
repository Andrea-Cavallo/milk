# 79. Tabbed Panel Docking: One Shared Slot When the Theme Draws Tabs

- **Status:** accepted
- **Date:** 2026-10-25

## Context

The supermilk skin (ADR-0077) gives every side panel the same title row: the
six-tab strip (`panelTabStrip` — memory, tasks, jobs, flow, files, git), with
the panel's own tab in the accent colour. What the panels did *not* do is
what a tab bar means: they were still laid out **side by side**, so with
memory and git open the user got two copies of the six-tab strip over two
sibling columns, each highlighting a different tab. Six tabs over six
stacked panels is a tab bar; six copies of it over six sibling columns is
not. The report was direct: *if the theme uses tabs, panels should occupy the
same spot*.

## Decision

When the theme renders the tab strip (exactly `th.modern` — the two are tied
so the tab bar and the layout can never disagree, `panelTabsActive()`), the
side panels are **tabs of one shared slot**:

- **One slot.** At most one panel occupies layout columns — the *front tab*
  (`panelActive`). The other open panels keep their flags, scroll offsets and
  content behind their tabs. Everything that measures or joins the layout
  (`mainWidth`, `regionAt`, `viewBody`, `panelX`, `panelAltBackground`, the
  animation tracker) derives from `panelVisible` (open + width-permitted +
  front tab); `panelOpen` stays the plain "is this panel open" for state,
  persistence and notifications. The alternating background invariant holds
  trivially: the single panel in the slot is always the first one, so it
  always differs from the chat.
- **Tab-shaped toggles.** F1-F6 / `/panel <name>` on a panel that is open but
  not in front *bring its tab to the front* — closing a panel the user cannot
  even see would be a trap. On the front tab they still close it, and on a
  closed one they still open it (taking the slot). A click on a tab label
  (the strip is the panel's first row) does what that tab's F-key does;
  the click hit-test (`panelTabAt`) walks the same join the strip renders
  with.
- **Front-tab history.** Activation goes through an MRU stack
  (`panelTabMRU`), so closing the front tab hands the slot to the tab viewed
  before it — like closing a browser tab — instead of an arbitrary one.
- **Auto-open takes the slot once.** Content-driven auto-open (ADR-0044)
  brings a *freshly opened* panel to the front — "panel opened" must mean the
  user can see it — but only on the closed→open transition. Later content for
  an already-open panel (every workflow chunk, every task update) never yanks
  the tab back from wherever the user has moved on to.
- **Tab switches animate nothing.** The slot stays occupied and only its
  content changes: a browser-like instant swap. The existing slide-in /
  shrink-out choreography (ADR-0076) still runs for what it was built for —
  the slot appearing (first panel) and disappearing (last panel).
- **The front tab is a preference.** `panel-state.json` (ADR-0078) gained an
  optional `active` key, written only for manually chosen panels (the same
  rule as the open flags: auto-opened state is not a preference) and restored
  at startup. A stale `active` naming a closed panel is harmless — the front
  falls back to the first open panel.

Without tabs (theme `"off"`) none of this applies: panels stay siblings,
joined left-to-right in `panelRenderOrder`, and every predicate degenerates
to the plain open flag — the pre-existing behaviour is untouched.

## Consequences

- Opening several panels on a tabbed theme costs one panel width of screen
  instead of N, and switching is a click on a tab or a press of its F-key.
- The tab bar is the panel's first content row, so it scrolls with the
  panel's content like any title row — a tab click is only possible while the
  panel is scrolled to the top. Pinning the bar to the panel's top edge would
  change every panel's line indexing (selection, click → row); left for a
  follow-up if it bothers anyone.
- To close a *hidden* panel you first switch to its tab (then its F-key
  closes it). There is no "close background tab" gesture beyond `/panel`'s
  toggle-on-front rule.
