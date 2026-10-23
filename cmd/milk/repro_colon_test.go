package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/sgr"
)

// Terminal-true check: a terminal carries SGR across \n. If any row leaves
// state open, the NEXT row starts painted in it — the bleed the user sees.
// Also brute-forces wrap widths (narrow panels hard-split long labels) and
// ':' -terminated lines, with and without an active selection.
func TestRepro_ColonLabelWidths(t *testing.T) {
	ttyOn(t)
	responses := map[string]string{
		"plain":       "simple answer here",
		"colon-lines": "Steps:\nfirst:\nsecond: do it\nthird: done",
		"long":        strings.Repeat("wrap me around the panel width ", 8),
	}
	for name, resp := range responses {
		for _, agentName := range []string{"mimo-local", "mimo-pro-local"} {
			for _, mode := range []ColorizeMode{ColorizeFenced, ColorizeBalanced, ColorizeFull} {
				for w := 10; w <= 60; w++ {
					for _, withSel := range []bool{false, true} {
						m := layoutTestModel(t, w, 40)
						m.colorizeMode = mode
						m.appendTranscript(promptLabel(m.st) + colorizeTokens("paste a line:\nand another:") + "\n")
						m.appendTranscriptStreamed(bold(green(agentName+":")) + " ")
						m.appendTranscriptStreamed(resp + "\n")
						if withSel { // select across the label row
							m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol = 1, 0, 3, 10
						}
						render := m.wrappedTranscript()
						// terminal-carry check
						var st sgr.State
						for i, row := range strings.Split(render, "\n") {
							before := st
							st = sgr.State{}
							st.Scan(row)
							// row begins with the state the previous row left open;
							// SelfContain re-establishes it — but it must CLOSE it.
							if st.Active() {
								_ = before
								t.Fatalf("[%s|%s|%s|w=%d|sel=%v] row %d leaves state open (bleeds into next row): %q\nfull=%q",
									name, agentName, mode, w, withSel, i, row, render)
							}
						}
					}
				}
			}
		}
	}
}
