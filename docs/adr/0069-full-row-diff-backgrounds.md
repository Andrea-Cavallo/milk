# ADR-0069: Full-row diff backgrounds — the surface painter reused for +/- rows

*Date: 2026-10-20*

## Status

Accepted

## Context

`internal/diff` renders inline edit results as bright glyphs on a dark
background — `ESC[38;5;119;48;5;22m` for additions, `ESC[38;5;203;48;5;52m`
for deletions — and closes every row with a reset before the newline. The
background therefore covers only the glyphs: on a short row the remainder of
the line sits on the terminal default, which reads as a truncated colour
block (issue #273: *"green/red background ends when characters end"*).

ADR-0067's fourth pass had already built the fix's technique for the thinking
surface: pad **after wrapping**, at **render time only** — buffers stay
plain, the colorize cache keeps storing the untinted wrap (width-dependent
pads never enter the cached bytes), and `transcriptPlainLines` strips the
escapes and trims the pad so copy, selection and hit-testing keep their
coordinate space.

## Decision

Reuse that technique for diff rows via a second, tightly-scoped painter:

- **`tintDiffSurface`** (`cmd/milk/diffsurface.go`) classifies each wrapped
  row by SGR state: only the two background colours `internal/diff` emits
  (256-colour `22`/`52`) count as diff rows. A diff row is padded to the
  viewport width with the pad *inside* the background. Rows without a diff
  colour come back byte-identical.
- **Emission is untouched.** The per-line reset stays exactly where it is;
  a row that closed its background gets the pad appended *after* the reset —
  re-opening the same colour just for the pad and closing again — so nothing
  after the row can inherit the background (SGR state carries across `\n`,
  and a following dim code does not clear it: the reset is what prevents
  green/red bleeding onto the next line). A row whose background is still
  open at the row end (an `ansi.Wrap` continuation of a long +/- line) pads
  inside the open run and keeps it open for the next continuation row, which
  is exactly what the terminal does anyway.
- **`tintFullRows`** applies the painters — thinking surface first, then
  diff — at every render site: the five `wrappedTranscript` returns and the
  attach view (before `tintBlock`, whose wrap leaves the row's own
  background in charge of the pad cells).
- **Recognition stays tight.** Arbitrary inline spans with other
  backgrounds are never padded — the painter must not second-guess styling
  it does not own. Two findings pin the boundary: chroma-highlighted
  ``` ` ```diff fences are **foreground-only** (monokai emits
  `38;5;148`/`197`/`231` — nothing to pad), and colorize *full* (glamour)
  strips diff backgrounds entirely — a pre-existing gap, out of scope here
  and tracked as a follow-up if ever wanted.

## Consequences

- Diff rows span the full row in the transcript and attach views at every
  render path (cold colorize, warm cache fast path, `colorize off`, balanced
  mode's empty styled span) — pinned by `cmd/milk/diffsurface_test.go`,
  including the no-bleed case: the row after a diff block inherits neither
  colour nor pad.
- Buffers, cache discipline, the plain coordinate space and ACP (which
  strips ANSI upstream) are all unchanged; `internal/diff` itself does not
  move.
- Alternative rejected: padding at emission would bake a viewport width into
  stored content and break the cache/plain-trim/export story. Alternative
  rejected: generalising to *any* background colour would pad styling milk
  doesn't own (markdown tables, model-emitted ANSI).

## Update (2026-10-20, ADR-0072)

Wrapped rows are now self-contained (`wrapRows`), so the painter no longer
carries background state between rows: every row of a wrapped +/- line starts
by re-opening its colours, ends with a reset, and gets the same
close-then-reopen pad. The "open at the row end" branch described above is gone.
