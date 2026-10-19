package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Full-row diff backgrounds (issue #273).
//
// internal/diff renders +/- rows as bright glyphs on a dark background —
// ESC[38;5;119;48;5;22m for additions, ESC[38;5;203;48;5;52m for deletions —
// and closes each row with a reset before the newline. The background thus
// only covers the glyphs: on a short row the rest of the line sits on the
// terminal default, which reads as a truncated colour block. This painter
// reuses the thinking surface's full-row technique (tintThinkSurface, ADR-0067)
// for those rows: after wrapping, every row that carries a diff background is
// padded to the viewport width and the pad lands *inside* the background so
// the colour spans the row.
//
// Scope and invariants (the reasoning is in ADR-0069):
//   - Render-time only, exactly like the think painter: buffers are untouched,
//     the colorize cache keeps storing the untinted wrap, and
//     transcriptPlainLines strips the escapes and trims the pad — copy,
//     selection and hit-testing stay plain.
//   - Emission (internal/diff) is unchanged. Its per-line reset stays exactly
//     where it is; the pad is appended after it and re-opens the same colour
//     just for the pad, so what follows the row is byte-order-identical to
//     before. This matters: a row whose background were left open at the
//     newline would bleed green/red onto the next line (SGR state carries
//     across \n, and a following dim code does not clear the background).
//   - Tight recognition: only the two background colours internal/diff emits
//     (256-colour 22/52) count as diff rows. Arbitrary inline spans with
//     other backgrounds are never padded — the painter must not second-guess
//     styling it does not own. ```diff fences are chroma-highlighted
//     foreground-only (monokai emits 38;5;148/197/231), so there is nothing
//     to pad there; colorize full (glamour) strips diff backgrounds entirely —
//     a pre-existing gap tracked separately, not regressed here.
//   - Wrap continuations: ansi.Wrap splits a long +/- line mid-background, so
//     intermediate rows end with the background still open. Those rows pad
//     inside the open run (the terminal carries it across the row break
//     anyway) and the run stays open for the next continuation row; only the
//     logical line's last row — the one carrying internal/diff's own reset —
//     gets the close-then-reopen pad.

const (
	// diffBgGreen/diffBgRed are the 256-colour background indices behind
	// internal/diff's ansiGreen/ansiRed (diff.go). Recognition matches the
	// colour values, never the sequence bytes, because the emission is a
	// combined fg+bg SGR: ESC[38;5;119;48;5;22m.
	diffBgGreen = "22"
	diffBgRed   = "52"
)

// diffBgMarkers are the substrings that flag content as possibly carrying a
// diff background. Cheap pre-check for tintDiffSurface's fast path; false
// positives only cost a row scan — the row classifier decides, not the
// marker (see sgrDiffBg's param-position handling).
var diffBgMarkers = []string{"48;5;" + diffBgGreen, "48;5;" + diffBgRed}

// sgrDiffBg folds one SGR parameter list into the active diff background
// state: returns the diff colour the row ends in ("" when none). Parameters
// apply left to right — 0/49 clear the background, 48 sets it to the diff
// colour when the triplet names one and to anything else otherwise — and 38
// triplets are consumed whole without touching background state (a colour
// index of 48 inside a foreground triplet, e.g. ESC[38;5;48m, is not a
// background command).
func sgrDiffBg(cur, params string) string {
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "", "0", "49":
			cur = ""
		case "38", "48":
			isBG := parts[i] == "48"
			switch {
			case i+2 < len(parts) && parts[i+1] == "5":
				if isBG {
					if parts[i+2] == diffBgGreen || parts[i+2] == diffBgRed {
						cur = parts[i+2]
					} else {
						cur = "" // another background takes over
					}
				}
				i += 2
			case i+4 < len(parts) && parts[i+1] == "2":
				if isBG {
					cur = ""
				}
				i += 4
			default:
				if isBG {
					cur = ""
				}
			}
		}
	}
	return cur
}

// diffRow classifies one wrapped row for the diff painter: padColor is the
// last diff background active during the row — the inherited open state
// included, "" when the row carries no diff colour at all — and endOpen the
// background state the next row inherits. SGR state persists across the
// newline until a reset says otherwise, exactly as the terminal sees it.
func diffRow(line, open string) (padColor, endOpen string) {
	bg := open
	last := open
	for i := 0; i < len(line); {
		if line[i] != 0x1B || i+1 >= len(line) || line[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		for j < len(line) && (line[j] < 0x40 || line[j] > 0x7E) {
			j++
		}
		if j >= len(line) {
			break // dangling escape at the row end — the rest is opaque
		}
		if line[j] == 'm' {
			bg = sgrDiffBg(bg, line[i+2:j])
			if bg != "" {
				last = bg
			}
		}
		i = j + 1
	}
	return last, bg
}

// tintDiffSurface makes the diff backgrounds full-row ones: every row of the
// already-wrapped render that carries an internal/diff background is padded
// to width cells, with the pad inside the background (the colour spans the
// row, not just the glyphs). Rows without a diff colour are left byte-
// identical. Width-dependent, so — like the think painter — it runs on the
// returned render only and the colorize cache keeps the untinted wrap.
func tintDiffSurface(content string, width int) string {
	if width <= 0 {
		return content
	}
	if !strings.Contains(content, diffBgMarkers[0]) && !strings.Contains(content, diffBgMarkers[1]) {
		return content // neither diff background present — nothing to do
	}
	lines := strings.Split(content, "\n")
	open := "" // diff background open at the row start
	for i, line := range lines {
		pad, next := diffRow(line, open)
		open = next
		if pad == "" {
			continue
		}
		w := ansi.StringWidth(line)
		if w >= width {
			continue // full-width already — nothing to pad
		}
		gap := strings.Repeat(" ", width-w)
		if next != "" {
			// Background still open at the row end (a wrap continuation of a
			// long +/- line): the pad lands inside the open run and the run
			// stays open so the next continuation row inherits it, exactly
			// like the terminal carries the state across the row break.
			lines[i] = line + gap
			continue
		}
		// Row closed its background before the newline (internal/diff's
		// per-line reset): append — never rewrite — so the emission's reset
		// order stays as-is, then reopen the same colour just for the pad and
		// close again, so nothing after the row can inherit the background.
		lines[i] = line + "\033[48;5;" + pad + "m" + gap + ansiReset
	}
	return strings.Join(lines, "\n")
}

// tintFullRows applies the full-row painters to an already-wrapped render:
// the thinking surface (ADR-0067) first, then the diff backgrounds (issue
// #273). Both recognise their rows by SGR state, leave every other row
// untouched, and are width-dependent — so the single call site convention
// holds: applied to the returned render, never to the cached wrap.
func tintFullRows(content string, width int) string {
	return tintDiffSurface(tintThinkSurface(content, width), width)
}
