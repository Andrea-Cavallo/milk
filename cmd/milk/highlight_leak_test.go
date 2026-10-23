package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/sgr"
)

// forceTTY turns the paint helpers on: the ANSI-emitting wrappers are no-ops
// without a TTY, and the bug under test lives entirely in emitted escapes.
func forceTTY(t *testing.T) {
	t.Helper()
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
}

// stateAt returns the rendition active at the first occurrence of substr in
// out, i.e. what the terminal would paint substr with.
func stateAt(t *testing.T, out, substr string) sgr.State {
	t.Helper()
	idx := strings.Index(out, substr)
	if idx < 0 {
		t.Fatalf("substring %q missing from render", substr)
	}
	var st sgr.State
	st.Scan(out[:idx])
	return st
}

// TestBalanced_LabelRowTailNotPainted pins the screenshot bug: the turn label
// ("mimo-pro-local:" in bold blue) shares a transcript row with the response's
// first line. The label span opens AND closes within that row — it is not the
// row's outer context, so the response text after it must stay default.
// Balanced mode's applyInlineMarkdown used to adopt the label as the line's
// "own context" and re-inject it after the label's own resets, repainting the
// first response line bold+blue (rows stayed balanced, which is why the
// balance-only regression tests never caught it).
func TestBalanced_LabelRowTailNotPainted(t *testing.T) {
	forceTTY(t)
	for _, mode := range []ColorizeMode{ColorizeBalanced} {
		for _, label := range []string{
			bold(blue("mimo-pro-local:")) + " ",
			bold(green("mimo-local:")) + " ",
		} {
			raw := label + "My take, plainly:\nMake the warning idle-refreshed: don't add a hard kill.\n\n"
			out := colorizeSingle(raw, mode)
			if st := stateAt(t, out, "My take"); st.Active() {
				t.Errorf("label %q: first response line painted %q — label span leaked into the row tail", label, st.String())
			}
			if st := stateAt(t, out, "Make the warning"); st.Active() {
				t.Errorf("label %q: following line painted %q — must stay default", label, st.String())
			}
		}
	}
}

// TestBalanced_UserEchoRowTailNotPainted: same bug class on the user-echo row
// (promptLabel + input text).
func TestBalanced_UserEchoRowTailNotPainted(t *testing.T) {
	forceTTY(t)
	raw := promptLabel(&interactiveState{}) + colorizeTokens("paste a line:") + "\n"
	out := colorizeSingle(raw, ColorizeBalanced)
	if st := stateAt(t, out, "paste a line"); st.Active() {
		t.Errorf("echo tail painted %q — prompt-label span leaked into the row", st.String())
	}
}

// TestBalanced_RowOwningTintsStillReInjected guards the over-correction: a
// leading dim (annotation rows) or background (diff rows) IS outer context and
// must still be re-injected after inner spans' resets — the reason
// injectANSIAfterResets exists.
func TestBalanced_RowOwningTintsStillReInjected(t *testing.T) {
	forceTTY(t)
	// Dim annotation row containing an inline-code span (emits its own reset).
	dimRow := dim("[milk]") + " see `foo.go` here\n"
	out := colorizeSingle(dimRow, ColorizeBalanced)
	if st := stateAt(t, out, "see"); st.Active() && !st.Dim() {
		t.Errorf("annotation body painted %q — dim context lost", st.String())
	}
	// Diff row: background opened at line start and kept to EOL, with a bold
	// span inline whose reset must not cut the background.
	diffRow := "\x1b[38;5;119;48;5;22m+ **bold** here\n"
	out2 := colorizeSingle(diffRow, ColorizeBalanced)
	if st := stateAt(t, out2, "here"); st.BG == "" {
		t.Errorf("diff row tail lost its background (state %q)", st.String())
	}
}
