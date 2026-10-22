package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/sgr"
)

// pasteShapes are the input shapes a real paste can deliver. The report under
// test: "after last ansi styling fixes the color bleeds from turn prefix"
// (reproduced with /paste or a multi-line paste).
func pasteShapes() map[string]string {
	return map[string]string{
		"plain-multiline": "first line of paste\nsecond line\nthird line",
		"unclosed-fence":  "notes:\n```go\nfunc main() {}",
		"closed-fence":    "```sh\necho hi\n```\nafter the fence",
		"crlf":            "win line one\r\nwin line two\r\nwin line three",
		"diff-like":       "+ added line\n- removed line\n context line",
		"prompt-in-paste": "❯ she pasted a shell prompt\n$ ls -la",
		"slash-paste":     "/paste\n/agents look at this",
		"long-line":       strings.Repeat("supercalifragilistic word ", 12),
		"markdown-bold":   "**bold claim** and _emph_ end",
		"literal-esc":     "shows ansi like \\x1b[31mred\\x1b[0m in text",
	}
}

func assertRowsBalanced(t *testing.T, surface, render string) {
	t.Helper()
	for i, row := range strings.Split(render, "\n") {
		if !rowBalanced(row) {
			t.Errorf("[%s] row %d leaves styling open: %q", surface, i, row)
		}
	}
}

// The transcript path: user echo (colorizeTokens) + turn prefix
// (bold(green/blue) agent label) + streamed chunks, rendered through
// wrappedTranscript in every colorize mode.
func TestRepro_TurnPrefixBleed_Transcript(t *testing.T) {
	ttyOn(t)
	for name, paste := range pasteShapes() {
		for _, mode := range []ColorizeMode{ColorizeFenced, ColorizeBalanced, ColorizeFull} {
			m := layoutTestModel(t, 40, 20)
			m.colorizeMode = mode

			// Turn 1: pasted user input echoed exactly as submitInput does.
			m.appendTranscript(promptLabel(m.st) + colorizeTokens(paste) + "\n")
			m.appendTranscriptStreamed(bold(green("mimo-local:")) + " ")
			resp := "Acknowledged the pasted block. " + strings.Repeat("some response words ", 10)
			for i := 0; i < len(resp); i += 5 {
				end := i + 5
				if end > len(resp) {
					end = len(resp)
				}
				m.appendTranscriptStreamed(resp[i:end])
			}
			m.appendTranscriptStreamed("\n\n")

			// Turn 2: plain input, escalation prefix — exercises the
			// turnColorCache indexing across turns.
			m.appendTranscript(promptLabel(m.st) + colorizeTokens("and now a plain turn") + "\n")
			m.appendTranscriptStreamed(bold(blue("mimo-pro-local:")) + " ")
			m.appendTranscriptStreamed(strings.Repeat("reply words here ", 8) + "\n")

			render := m.wrappedTranscript()
			assertRowsBalanced(t, name+"/"+string(mode)+"/transcript", render)

			// No row outside the prefix label may open the prefix's colours.
			for i, row := range strings.Split(render, "\n") {
				if strings.Contains(row, "mimo-local:") || strings.Contains(row, "mimo-pro-local:") {
					continue
				}
				var st sgr.State
				st.Scan(row)
				if st.FG == "32" || st.FG == "34" {
					t.Errorf("[%s/%s] row %d opens prefix colour on a plain row: %q", name, mode, i, row)
				}
			}
		}
	}
}

// The input box path: colorizeInput over the textarea view (line 0 carries the
// ❯ turn prefix), including continuation lines and a pasted shell prompt.
func TestRepro_TurnPrefixBleed_InputBox(t *testing.T) {
	ttyOn(t)
	for name, paste := range pasteShapes() {
		m := layoutTestModel(t, 40, 20)
		m.colorizeMode = ColorizeFenced
		m.ta.SetValue(paste)
		m.refreshPrompt()
		view := m.ta.View()
		got := m.colorizeInput(view)
		assertRowsBalanced(t, name+"/input", got)
	}
}
