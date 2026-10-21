package main

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/sgr"
)

// wrapRows word-wraps styled text to width and returns it as self-contained
// rows: a style that the wrap splits across rows is re-opened on each
// continuation row and closed at each row's end (sgr.SelfContain). Everything
// that later treats rows individually — the viewport window starting on a
// continuation row, per-row background tints, selection, truncation — can
// then rely on each row carrying its own styling, with no state tracking of
// its own. Every wrap of styled transcript content goes through here.
func wrapRows(s string, width int) string {
	return sgr.SelfContain(ansi.Wrap(expandTabsForWrap(s), width, ""))
}
