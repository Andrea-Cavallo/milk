package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/sgr"
)

// internal/diff's emission, verbatim — these literals pin the format the
// painter recognises (combined fg+bg SGR, per-line reset) so a change on the
// emission side fails here rather than silently losing full-row coverage.
const (
	diffGreenOpen = "\x1b[38;5;119;48;5;22m" // additions: bright green on 256-colour 22
	diffRedOpen   = "\x1b[38;5;203;48;5;52m" // deletions: bright red on 256-colour 52
	diffGreenRow  = diffGreenOpen + "+added line\x1b[0m"
	diffRedRow    = diffRedOpen + "-removed line\x1b[0m"
	diffDimHdr    = "\x1b[2m--- cmd/milk/diff.go\x1b[0m"
	diffDimCtx    = "\x1b[2m context line\x1b[0m"
)

// diffRowShape matches a diff row whose colour pad runs right up to the
// closing reset — the full-row shape: the background covers the row, never
// bare tail cells on the terminal default.
var diffRowShape = regexp.MustCompile(`\x1b\[48;5;(22|52)m *\x1b\[0m$`)

// diffSample is a complete inline-edit result as it lands in the transcript:
// dim header, green addition, red deletion, dim context line.
func diffSample() string {
	return diffDimHdr + "\n" + diffGreenRow + "\n" + diffRedRow + "\n" + diffDimCtx + "\n"
}

// The diff backgrounds are full-row: every +/- row spans the viewport width
// with its colour covering the pad, rows without a diff colour are left
// alone, and the pad never reaches the plain coordinate space (copy,
// hit-testing, selection).
func TestDiffSurface_FullRowCoverage(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	m := layoutTestModel(t, 40, 20)
	m.colorizeMode = ColorizeFenced
	m.appendTranscript("before\n")
	m.appendTranscript(diffSample())
	m.appendTranscript("after\n")
	vw := m.vpWidth()
	rows := strings.Split(m.wrappedTranscript(), "\n")

	seen := map[string]bool{}
	for i, row := range rows {
		plain := strings.TrimRight(ansi.Strip(row), " ")
		switch {
		case strings.Contains(row, "48;5;22"), strings.Contains(row, "48;5;52"):
			seen[plain] = true
			if w := ansi.StringWidth(row); w != vw {
				t.Errorf("diff row %d width = %d, want full-row %d: %q", i, w, vw, row)
			}
			if !diffRowShape.MatchString(row) {
				t.Errorf("diff row %d must end colour-pad-then-reset: %q", i, row)
			}
		default:
			if w := ansi.StringWidth(row); w != utf8.RuneCountInString(ansi.Strip(row)) {
				t.Errorf("row %d off the diff colours must not be padded: %q", i, row)
			}
		}
	}
	if !seen["+added line"] || !seen["-removed line"] {
		t.Errorf("both +/- rows must render, saw %v", seen)
	}
	// The plain space stays free of the render-time pad and escapes.
	for i, line := range m.transcriptPlainLines() {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("plain line %d carries pad spaces: %q", i, line)
		}
	}
	if strings.Contains(strings.Join(m.transcriptPlainLines(), "\n"), "\x1b") {
		t.Error("plain lines must stay escape-free")
	}
}

// A long +/- line wraps across rows: every row of it is self-contained (the
// colour is re-opened at the start of each continuation row and closed at its
// end, wrapRows) and gets the same close-then-reopen pad — and the row *after*
// the diff block inherits nothing: no colour, no pad. This is the bleed case
// the per-line reset exists for.
func TestDiffSurface_WrappedNoBleed(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	m := layoutTestModel(t, 40, 20)
	m.colorizeMode = ColorizeFenced
	long := "+alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma"
	m.appendTranscript(diffGreenOpen + long + "\x1b[0m\n")
	m.appendTranscript("after\n")
	vw := m.vpWidth()
	rows := strings.Split(m.wrappedTranscript(), "\n")

	var diffRows []string
	var after []string
	for _, row := range rows {
		if strings.Contains(row, "48;5;22") {
			diffRows = append(diffRows, row)
		} else if strings.TrimSpace(ansi.Strip(row)) == "after" {
			after = append(after, row)
		}
	}
	if len(diffRows) < 2 {
		t.Fatalf("long diff line must wrap, rows = %d: %q", len(diffRows), rows)
	}
	// Every row, continuations included, is a full-width diff row that closes
	// its own colour: pad inside the background, then a reset.
	for i, row := range diffRows {
		if w := ansi.StringWidth(row); w != vw {
			t.Errorf("diff row %d width = %d, want %d: %q", i, w, vw, row)
		}
		if !diffRowShape.MatchString(row) {
			t.Errorf("diff row %d must end colour-pad-then-reset: %q", i, row)
		}
		if i > 0 && !strings.HasPrefix(row, "\x1b[38;5;119;48;5;22m") {
			t.Errorf("continuation row %d must re-open the diff colours: %q", i, row)
		}
	}
	// No bleed: the following row carries no colour and no pad.
	if len(after) == 0 {
		t.Fatalf("row after the diff block not found in %q", rows)
	}
	for _, row := range after {
		if strings.Contains(row, "48;5;") {
			t.Errorf("diff background bled past the block: %q", row)
		}
		if w := ansi.StringWidth(row); w != utf8.RuneCountInString(ansi.Strip(row)) {
			t.Errorf("row after the diff block must not be padded: %q", row)
		}
	}
}

// The pad rides every render path: cold colorize, the warm cache fast path,
// and colorize off — all three return the same full-row result. (The cache
// stores the untinted wrap; the painter runs on the returned render.)
func TestDiffSurface_CacheAndOffPaths(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	for _, mode := range []ColorizeMode{ColorizeFenced, ColorizeBalanced, ColorizeOff} {
		t.Run(string(mode), func(t *testing.T) {
			m := layoutTestModel(t, 40, 20)
			m.colorizeMode = mode
			m.appendTranscript(diffSample())
			first := m.wrappedTranscript()  // cold render populates the cache
			second := m.wrappedTranscript() // warm fast path (no growth)
			if first != second {
				t.Errorf("warm-cache render differs from cold:\ncold: %q\nwarm: %q", first, second)
			}
			for i, row := range strings.Split(second, "\n") {
				if !strings.Contains(row, "48;5;22") {
					continue
				}
				if w := ansi.StringWidth(row); w != m.vpWidth() {
					t.Errorf("mode %s: diff row %d width = %d, want %d: %q", mode, i, w, m.vpWidth(), row)
				}
			}
		})
	}
}

// Thinking and diff surfaces coexist in one render: each painter only claims
// its own rows, and rows that belong to neither stay unpadded.
func TestDiffSurface_CoexistsWithThinkSurface(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	m := layoutTestModel(t, 40, 20)
	m.colorizeMode = ColorizeFenced
	m.appendThinkingStreamed("considering the edit\n")
	m.appendTranscript("applying\n") // closes + folds the block
	m.toggleThinkBlock(0)            // expand: body rows must carry the think surface
	m.appendTranscript(diffSample())
	vw := m.vpWidth()
	rows := strings.Split(m.wrappedTranscript(), "\n")

	var thinkRows, diffRows, other int
	for _, row := range rows {
		switch {
		case strings.Contains(row, ansiThinkBg):
			thinkRows++
			if w := ansi.StringWidth(row); w != vw {
				t.Errorf("think row not full-width: %q", row)
			}
		case strings.Contains(row, "48;5;22"), strings.Contains(row, "48;5;52"):
			diffRows++
			if w := ansi.StringWidth(row); w != vw {
				t.Errorf("diff row not full-width: %q", row)
			}
		default:
			other++
			if w := ansi.StringWidth(row); w != utf8.RuneCountInString(ansi.Strip(row)) {
				t.Errorf("off-surface row must stay unpadded: %q", row)
			}
		}
	}
	if thinkRows < 2 || diffRows != 2 || other < 3 {
		t.Errorf("row classification: think=%d diff=%d other=%d, want think>=2 diff==2 other>=3", thinkRows, diffRows, other)
	}
}

// Composed with the attach view's block tint (syncAttachedContent's order:
// full-row painters, then tintBlock), nothing widens past the viewport and
// the diff pad survives — tintBlock must re-apply its own background after
// the colour pad's reset without re-padding an already-full row.
func TestDiffSurface_AttachComposition(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	const vw = 40
	content := diffSample() + "plain tail\n"
	tinted := tintFullRows(ansi.Wrap(expandTabsForWrap(content), vw, ""), vw)
	if bg := panelAltBackgroundCode(); bg != "" {
		tinted = tintBlock(tinted, vw, bg)
	}
	for i, row := range strings.Split(tinted, "\n") {
		if w := ansi.StringWidth(row); w > vw {
			t.Errorf("row %d widened past the viewport (%d > %d): %q", i, w, vw, row)
		}
		if strings.Contains(row, "48;5;22") {
			if w := ansi.StringWidth(row); w != vw {
				t.Errorf("diff row %d not full-width after composition: %q", i, row)
			}
		}
	}
}

// The classifier: which rows count as diff rows (and in which colour) is
// decided by sgr.LastBackground against internal/diff's two backgrounds —
// no recognition outside them, and a foreground colour index that happens to
// be 48 or 22 is never mistaken for a background.
func TestDiffRow_Classifier(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"closed green row", diffGreenRow, diffBgGreen},
		{"closed red row", diffRedRow, diffBgRed},
		{"dim row untouched", diffDimCtx, ""},
		{"plain row untouched", "after", ""},
		{"reset after the colour still counts", "\x1b[48;5;22mx\x1b[0my", diffBgGreen},
		{"other background", "\x1b[48;5;99mx\x1b[0m", ""},
		{"fg triplet is not a background", "\x1b[38;5;22mx", ""},
		{"fg index 48 does not hide the diff colour", "\x1b[48;5;22mx\x1b[38;5;48my", diffBgGreen},
		{
			// colorize balanced's line wrap emits the styled span a second
			// time as an empty set+reset pair before the newline.
			"balanced-mode empty span",
			diffGreenRow + "\x1b[38;5;119;48;5;22m\x1b[0m",
			diffBgGreen,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sgr.LastBackground(c.line, isDiffBackground); got != c.want {
				t.Errorf("LastBackground(%q) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}

// Recognition is tight: content without a real diff background comes back
// byte-identical — including content that merely *mentions* the marker as
// plain text (a file containing "48;5;22" is not a diff row).
func TestDiffSurface_RecognitionTight(t *testing.T) {
	cases := map[string]string{
		"no escapes":        "+added line\n-removed line\n",
		"marker as text":    "the string 48;5;22 appears in this file\n",
		"other bg colour":   "\x1b[48;5;100mrow\x1b[0m\n",
		"fg only":           "\x1b[38;5;119m+green fg\x1b[0m\n",
		"empty":             "",
		"plain text marker": "prefix 48;5;52 suffix without any escape\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tintDiffSurface(content, 40); got != content {
				t.Errorf("content changed:\ngot  %q\nwant %q", got, content)
			}
		})
	}
}
