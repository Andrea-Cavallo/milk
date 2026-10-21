package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/sgr"
)

func ttyOn(t *testing.T) {
	t.Helper()
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
}

// rowBalanced reports whether a row opens and closes its own styling: nothing
// is left active at its end.
func rowBalanced(row string) bool {
	var st sgr.State
	st.Scan(row)
	return !st.Active()
}

// A styled line that wraps renders every row with its own styling, so a
// viewport window that starts on a continuation row (the user scrolled
// mid-line) still shows it dim.
func TestWrappedTranscript_StyledLineRowsAreSelfContained(t *testing.T) {
	ttyOn(t)
	m := layoutTestModel(t, 40, 20)
	m.colorizeMode = ColorizeFenced
	m.appendTranscript("before\n")
	m.appendTranscript(dim("⚙ bash: " + strings.Repeat("alpha beta gamma ", 8)))
	m.appendTranscript("\nafter\n")

	var dimRows int
	for i, row := range strings.Split(m.wrappedTranscript(), "\n") {
		if !rowBalanced(row) {
			t.Errorf("row %d leaves styling open: %q", i, row)
		}
		if strings.Contains(ansi.Strip(row), "alpha") || strings.Contains(ansi.Strip(row), "gamma") {
			dimRows++
			if !strings.HasPrefix(row, ansiDim) {
				t.Errorf("wrapped row %d does not open dim: %q", i, row)
			}
		}
	}
	if dimRows < 3 {
		t.Fatalf("expected the dim line to wrap over several rows, got %d", dimRows)
	}
}

// The same holds on the streaming fast path, where fresh text is wrapped on
// its own and appended after the cached prefix.
func TestWrappedTranscript_FastPathSuffixIsSelfContained(t *testing.T) {
	ttyOn(t)
	m := layoutTestModel(t, 30, 20)
	m.colorizeMode = ColorizeFenced
	m.appendTranscript("first line\n")
	m.wrappedTranscript() // fill the cache
	m.appendTranscript(dim(strings.Repeat("streamed words ", 6)) + "\n")
	for i, row := range strings.Split(m.wrappedTranscript(), "\n") {
		if !rowBalanced(row) {
			t.Errorf("row %d leaves styling open: %q", i, row)
		}
	}
}

// A fold header too long for the viewport wraps; its continuation rows keep
// both the surface background and the header's dim.
func TestThinkHeader_WrappedRowsKeepSurfaceAndDim(t *testing.T) {
	ttyOn(t)
	m := layoutTestModel(t, 20, 20)
	m.colorizeMode = ColorizeFenced
	m.appendThinkingStreamed("one\ntwo\nthree\n")
	rows := strings.Split(m.wrappedTranscript(), "\n")
	header := rows[:2] // "▸ thinking · 3" / "lines" — the folded header wraps
	for i, row := range header {
		var st sgr.State
		dimOnSurface := false
		sgr.Walk(row, func(params string) {
			st.Apply(params)
			if st.Dim() && st.BG == thinkBgParams {
				dimOnSurface = true
			}
		})
		if !dimOnSurface {
			t.Errorf("header row %d is not dim on the thinking surface: %q", i, row)
		}
		if w := ansi.StringWidth(row); w != m.vpWidth() {
			t.Errorf("header row %d width = %d, want full-row %d", i, w, m.vpWidth())
		}
	}
	if !strings.Contains(ansi.Strip(header[1]), "lines") {
		t.Fatalf("header did not wrap as expected: %q", header)
	}
}

// Selecting text highlights it without flattening the rows' own styling: the
// diff and thinking backgrounds under the selection survive.
func TestSelectionHighlight_KeepsRowStyling(t *testing.T) {
	ttyOn(t)
	m := layoutTestModel(t, 40, 20)
	m.colorizeMode = ColorizeFenced
	m.appendTranscript("before\n")
	m.appendTranscript(diffSample())
	m.appendThinkingStreamed("some reasoning\n")

	plain := strings.Split(m.wrappedTranscript(), "\n")
	line := -1
	for i, row := range plain {
		if strings.Contains(row, "+added line") {
			line = i
		}
	}
	if line < 0 {
		t.Fatal("diff row not found")
	}
	m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol = 0, 0, len(plain)-1, 10

	rows := strings.Split(m.wrappedTranscript(), "\n")
	if got := rows[line]; !strings.Contains(got, "48;5;22") || !strings.Contains(got, selectionOn) {
		t.Errorf("selected diff row lost its background or highlight: %q", got)
	}
	var thinkRows int
	for _, row := range rows {
		if strings.Contains(row, thinkBgParams) {
			thinkRows++
			if !strings.Contains(row, selectionOn) {
				t.Errorf("selected think row not highlighted: %q", row)
			}
		}
	}
	if thinkRows == 0 {
		t.Error("think surface lost under selection")
	}
	for i, row := range rows {
		if !rowBalanced(row) {
			t.Errorf("row %d leaves styling open: %q", i, row)
		}
	}
}
