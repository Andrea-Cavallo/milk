# 61. Idle Tip Line Below the TUI Input

- **Status:** accepted
- **Date:** 2026-10-17

## Context

milk's feature surface is large (38 slash commands, F1-F4 panels, ctrl+r
search, `@`-path completion, `!` direct bash, ctrl+enter background jobs)
and mostly invisible until a user already knows it. `/help` exists but is a
wall of text nobody reads unprompted. The welcome screen lists a handful of
hints — and only while the transcript is empty. Once a session is underway,
nothing surfaces the rest.

Reference harnesses solve this the same way. Both surveyed codebases
(MiMo-Code, an opencode fork, and opencode itself) show an idle-screen hint
surface on their home screen:

- a rotating **`● Tip <text>`** line *directly below the input box*
  (`feature-plugins/home/tips.tsx` → `home_bottom` slot), drawn from a
  static built-in pool of ~80-95 one-liners, mostly keyboard shortcuts and
  feature upsells;
- rotating **example placeholders** inside the empty input
  (`"Fix a TODO in the codebase"` …);
- static footer shortcut reminders (`@ attach file`, `/ commands`).

Freshness in those harnesses is rotation only: MiMo-Code re-picks a
weighted-random tip every 10s (no seen-tracking, repeats possible);
opencode picks one random tip per home-screen mount. Both gate visibility
with a persisted `tips_hidden` flag and a `tips_toggle` keybind. Content is
mostly static, with a few state-conditioned variants (no provider connected
→ "run /connect"; platform branch; keybind-derived shortcut text so the
copy cannot drift from the real bindings).

milk has no "home screen" — its TUI is one persistent transcript view with
the input at the bottom — but it already has the right slot: the hint
region under the main area (`tabHints`, the completion-hints area), whose
line count `viewportHeight` already accounts for.

## Decision

milk shows a **one-line idle tip below the input area**, in the existing
hint region, whenever it is not occupied by completion hints
(`(*model).hintRegion`, `cmd/milk/layout.go`). One row is *reserved* while
tips are enabled, so the layout never jumps when a turn starts or ends; the
line yields the slot to completion hints (the user's active task) and is
absent under the PTY pane, whose layout branch renders no hint rows.

Rendering (`renderTip`, `cmd/milk/tips.go`): a gold `tip:` label, dim
prose, yellow `{span}` highlights, truncated to the available width with an
ellipsis — one row is a hard layout invariant. The line ends with the
browse-shortcut reference (`tipNavRef`), which yields before the tip text
does on narrow terminals.

### Contents: three sources, precedence order

1. **Contextual hints** (`contextualTip`) — derived from live state at
   render time, so they are fresh by construction: no primary agent →
   `/config init`; no agents reachable → `/agent`; update pending →
   `/update install` with the actual release tag. Deliberately tiny:
   anything the status bar already announces permanently (jobs running,
   workflow in progress) does not belong here.
2. **Curated tips** (`curatedTips`) — hand-written one-liners for the
   features and key bindings that exist nowhere else (`@`-paths, `!`
   bash, ctrl+r, F1-F4, ctrl+enter, …), in priority order. The order *is*
   the priority (the MiMo-Code weighted-pool idea, expressed by position
   instead of random weights): the rotation starts at the top, so a fresh
   session sees the most valuable tips first.
3. **Registry-derived tips** (`registryTips`) — generated from the
   slash-command registry (slashcmd.go, issue #226): every non-hidden
   TUI-hosted command no curated tip names gets `{/name <hint>} — desc`
   straight from the registry's own `desc`/`hint`. A command gets a tip the
   day it lands in the registry; a renamed or re-described command cannot
   leave a stale tip behind. The same single-source invariant the registry
   already grants for help, completion and `available_commands_update`.

### Freshness

- **Rotation** (`tipsTick`): every 20s the tip advances one position
  through `buildTipList()` (curated first, then the derived tail) — a
  deterministic cycle, no repeats until the pool is exhausted. The tick
  *freezes* while a turn runs or a prompt is pending (the row stays, the
  content stops changing — the user's attention is elsewhere) and resumes
  when they are idle again. No seen-tip persistence across sessions; a
  fresh session restarts at the top of the priority order.
- **Contextual tips** are recomputed every render, so they can never go
  stale.
- **Derived tips** are rebuilt from the registry, so they track the
  command set by construction.

### Control

`/tips [on|off]` (registry entry, TUI-hosted) toggles the line and
persists the default to config `show_tips` (unset = on) via the same
`saveLocalOrGlobal` path `/colorize` uses; the bare query reports the
state and where the shown tip sits in the rotation. The toggle reflows
the layout by exactly one row (`syncLayout`).

*Extended 2026-10-19 (tip browsing).* The same registry entry grew
`/tips next|prev|pick [<n>|<text>]|search <query>` plus the global
`Alt+Up`/`Alt+Down` shortcuts (same any-mode availability as F1–F4):
step through the pool, jump to a numbered or text-matched tip (echoed to
the transcript at full width), search it. Every manual move restarts the
cycling timer — `tipsTickMsg.gen` vs `tipsGen`, the toastTickMsg generation
check — so the 20s countdown starts fresh and the tick in flight is
dropped instead of cutting the browse short. The rendered line carries the
shortcut reference itself (`tipNavRef`, `· prev/next: Alt+Up/Down`) —
dropped on narrow terminals before the tip text is cut, so the
visualization teaches its own navigation. A manual browse pins the
rotation's entry over contextual hints (`tipPinned`) until the next
automatic advance, and a browse with the line hidden echoes the tip to
the transcript — stepping is never a silent no-op. Registry-derived tips
render the registry's full argument grammar (`{/name <hint>}`), and the
hints behind them were completed and corrected so that grammar is worth
rendering (the `/server` gap).

## Consequences

- Users idle at the prompt discover features they would never have found in
  `/help` — the exact gap both reference harnesses target.
- The command surface is now self-documenting twice over: `/help` for when
  someone asks, the tip line for when nobody does — and the derived half
  cannot drift from the registry.
- One reserved row is the cost on every screen while tips are on; `/tips
  off` or `"show_tips": false` gives it back. Completion hints still expand
  the region to as many rows as they need, as before.
- Curated tips are priority-ordered copy; editing the order changes what
  fresh sessions see first. Adding a curated tip for a command retires its
  derived duplicate automatically (the `{/cmd …}` coverage scan), so the
  pool never double-tips a command.
- Known gaps, deliberate: no input-box example placeholders (the harnesses'
  other hint surface) — milk's input shows the route/agent prompt label
  instead, and example prompts there would fight it; no localization (milk
  has no i18n layer at all); no per-tip suppression or seen-tip history.
