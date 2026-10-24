package main

import (
	"testing"
)

// The live ↓~ estimate and the tok/s clock are for what the model produced.
// The agent-name label milk prints before the request goes out is neither.
func TestLiveEstimate_PrefixIsNotModelOutput(t *testing.T) {
	m := newTestModelForPanels(t)
	m.busy = true

	next, _ := m.Update(prefixChunkMsg{text: "\x1b[1m\x1b[32mmimo-local:\x1b[0m "})
	m = next.(model)
	if m.currentTurnChars != 0 {
		t.Errorf("the prefix counted as %d output chars", m.currentTurnChars)
	}
	if !m.turnFirstOutputAt.IsZero() {
		t.Error("the prefix started the tok/s clock before the model produced anything")
	}

	next, _ = m.Update(chunkMsg{text: "hello"})
	m = next.(model)
	if m.currentTurnChars != 5 {
		t.Errorf("model text: got %d chars, want 5", m.currentTurnChars)
	}
	if m.turnFirstOutputAt.IsZero() {
		t.Error("the first model chunk must start the tok/s clock")
	}
}

// Styling codes are not model output either.
func TestLiveEstimate_ANSIIsNotCounted(t *testing.T) {
	m := newTestModelForPanels(t)
	m.busy = true
	next, _ := m.Update(chunkMsg{text: "\x1b[33m» read_file\x1b[0m"})
	m = next.(model)
	if want := int64(len("» read_file")); m.currentTurnChars != want {
		t.Errorf("got %d chars, want %d (visible text only)", m.currentTurnChars, want)
	}
}

func TestVisibleChars(t *testing.T) {
	if got := visibleChars("plain"); got != 5 {
		t.Errorf("plain: %d", got)
	}
	if got := visibleChars("\x1b[1mbold\x1b[0m"); got != 4 {
		t.Errorf("styled: %d", got)
	}
}
