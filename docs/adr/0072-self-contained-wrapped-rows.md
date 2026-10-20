# ADR-0072: Self-contained wrapped rows — one SGR tracker instead of per-feature patches

*Date: 2026-10-20*

## Status

Accepted

## Context

A word wrap can split a styled span across rows, and a terminal carries SGR
state across the newline — but nothing downstream of the wrap does. A viewport
window can start on a continuation row, a per-row tint closes each row with a
reset, a selection rebuilds a row from scratch. The symptom that surfaced it:
the workflow attach view dims reasoning per streamed chunk, and the first words
of every wrapped row rendered white (the row-by-row background tint reset the
dim the previous row had left open).

Each earlier symptom had been patched where it appeared: `sgrThinkBg`/
`thinkRowSurface` and `sgrDiffBg`/`diffRow` carried background state between
rows for the two full-row painters; `leadingANSI`, `trailingOpenANSI` and a
"last code" carry in `applyInlineMarkdown` and `splitTurns` carried dim across
newlines (a single code cannot represent bold *and* dim); `withPanelBackground`
re-applied a tint after the literal `ESC[0m` only; the colorize-cache seams
force-appended resets; selection stripped every escape from selected rows; a
local `stripANSI` only understood sequences ending in `m`. About fifteen
trackers, each a partial model of the same state.

## Decision

Make rows self-contained once, at the wrap, and let everything downstream
treat rows independently.

- **`internal/sgr`** is the single SGR model: `State` (bold/dim/italic/
  underline/blink/reverse/hidden/strike + fg + bg, in every colour form),
  `Walk`/`Scan`, and the primitives built on it — `SelfContain` (re-open the
  inherited state at the start of each row, close at its end; idempotent),
  `WithBackground` (re-applies a tint after anything that clears the
  background, including `ESC[m` and `0;…`), `LastBackground` (which of a set of
  backgrounds a row shows) and `Highlight` (mark a visible-rune range while
  keeping the row's own styling).
- **`wrapRows`** (`cmd/milk/wraprows.go`) is `ansi.Wrap` + `SelfContain`; every
  wrap of styled transcript content goes through it (transcript, colorize
  cache, streaming suffix, attach view). The cache therefore stores balanced
  rows and the fast-path seam needs no forced reset.
- The think and diff painters become per-row (`LastBackground` → pad →
  `WithBackground`/close-then-reopen); there is no carried state and no
  "open at row end" branch.
- Selection (transcript, panels, attach) and the input selection use
  `Highlight` instead of strip-and-rebuild, so selected rows keep their
  colours and surfaces; the four copies of the range arithmetic collapse into
  `orderedSelection`/`selectionSpan`.
- `applyInlineMarkdown` and `splitTurns` carry an `sgr.State` instead of the
  last escape code. A differential run of old vs. new over ~20k generated
  inputs showed the only behavioural changes are edge cases the old code got
  wrong: a leading `ESC[0m` treated as an outer context, text re-dimmed after
  a source line's own reset, and multi-attribute contexts reduced to their last
  code.
- `colorize` and `thinkSurfaceLines` share `paintLines`; the local `stripANSI`
  is gone in favour of `ansi.Strip`.

## Consequences

- A scrolled viewport that starts mid-line, a tinted row, a selected row and a
  truncated row all see the same self-contained text — fixing the dim/colour
  loss on continuation rows that the attach view, the transcript and
  the fold header's wrap all shared.
- New code that wraps styled text calls `wrapRows`, and needs no state tracking
  of its own; the unit of correctness is "every row is balanced".
- Attributes `sgr.State` does not model (overline, underline colour) are not
  re-opened on continuation rows; nothing in milk emits them.
- `internal/ansi` (TTY-gated `Colorize`/`Dim` for packages below `cmd/milk`) is
  deliberately unchanged.
