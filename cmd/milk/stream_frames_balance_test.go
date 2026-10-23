package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/sgr"
)

// Render after EVERY streamed chunk — the end-state test can miss a transient
// or cache-seam state where the label's colour survives into the response.
func TestRepro_EveryFrame(t *testing.T) {
	ttyOn(t)
	paste := "pasted block\n```go\nfunc main(){}"
	resp := "Here is the answer with **bold** and `code` " + strings.Repeat("word ", 20) +
		"\n\n```sh\nls -la\n```\n" + strings.Repeat("tail words ", 10)
	for _, mode := range []ColorizeMode{ColorizeFenced, ColorizeBalanced, ColorizeFull} {
		m := layoutTestModel(t, 44, 24)
		m.colorizeMode = mode

		frame := 0
		check := func(what string) {
			frame++
			render := m.wrappedTranscript()
			for i, row := range strings.Split(render, "\n") {
				if !rowBalanced(row) {
					t.Errorf("[%s] frame %d (%s) row %d unbalanced: %q", mode, frame, what, i, row)
				}
				if strings.Contains(row, "mimo-local:") {
					continue
				}
				var st sgr.State
				st.Scan(row)
				if st.FG == "32" || st.FG == "34" {
					t.Errorf("[%s] frame %d (%s) row %d shows LABEL colour on plain row: %q", mode, frame, what, i, row)
				}
			}
		}

		m.appendTranscript(promptLabel(m.st) + colorizeTokens(paste) + "\n")
		check("echo")
		m.appendTranscriptStreamed(bold(green("mimo-local:")) + " ")
		check("prefix")
		for i := 0; i < len(resp); i += 3 {
			end := min(i+3, len(resp))
			m.appendTranscriptStreamed(resp[i:end])
			check(fmt.Sprintf("chunk@%d", i))
		}
	}
}
