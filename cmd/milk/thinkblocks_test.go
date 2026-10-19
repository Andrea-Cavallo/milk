package main

import (
	"strings"
	"testing"
)

// Thinking-as-a-view tests (ADR-0067, issue #208): blocks live out of band,
// render as fold containers, fold on close, and stay click-toggleable.

func TestThinkBlocks_StreamIntoBlockThenFoldOnClose(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = true
	m.appendThinkingStreamed("first\nsecond\n")
	disp := m.transcriptDisplay()
	if !strings.Contains(disp, thinkExpandedPrefix+"2 lines · streaming") {
		t.Errorf("streaming block must render an open header, got %q", disp)
	}
	if !strings.Contains(disp, "first\nsecond") {
		t.Errorf("streaming block body must be visible while open, got %q", disp)
	}
	// Regular content closes the block: it folds to its header and the body
	// is gone from the display (inspectable via expand, not dumped inline).
	m.appendTranscript("the answer\n")
	disp = m.transcriptDisplay()
	if !strings.Contains(disp, thinkFoldedPrefix+"2 lines · Ctrl+T") {
		t.Errorf("completed block must fold to its header, got %q", disp)
	}
	if strings.Contains(disp, "first") || strings.Contains(disp, "second") {
		t.Errorf("folded body must not render, got %q", disp)
	}
	// The transcript buffer itself holds only regular text — no dim escapes,
	// no reasoning.
	if got := m.transcript.String(); got != "the answer\n" {
		t.Errorf("transcript buffer = %q", got)
	}
}

func TestThinkBlocks_PlaceholderWhenHidden(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = false
	m.appendThinkingStreamed("secret\nthoughts\n")
	disp := m.transcriptDisplay()
	if !strings.Contains(disp, thinkHiddenPlaceholder) {
		t.Errorf("hidden block must render the placeholder, got %q", disp)
	}
	if strings.Contains(disp, "secret") {
		t.Errorf("hidden reasoning must not render, got %q", disp)
	}
	// Toggling on is retroactive: the block appears as a fold (still
	// collapsed unless pinned), never as inline dim text.
	m.showThinking = true
	m.applyThinkVisibility()
	disp = m.transcriptDisplay()
	if !strings.Contains(disp, thinkFoldedPrefix) && !strings.Contains(disp, thinkExpandedPrefix) {
		t.Errorf("toggling on must reveal the fold container, got %q", disp)
	}
}

func TestThinkBlocks_ClickTogglesAndPins(t *testing.T) {
	// Wide viewport: no line wraps, so display line indexes and the wrapped
	// coordinate space the mouse handler feeds thinkBlockAtLine coincide.
	m := layoutTestModel(t, 200, 50)
	m.colorizeMode = ColorizeOff
	m.showThinking = true
	m.appendThinkingStreamed("a\nb\n")
	m.appendTranscript("done\n") // closes + folds the block
	disp := m.transcriptDisplay()
	lines := strings.Split(disp, "\n")
	if !isThinkHeaderLine(lines[0]) {
		t.Fatalf("first line must be the fold header, got %q", lines[0])
	}
	if idx := m.thinkBlockAtLine(0); idx != 0 {
		t.Fatalf("header click maps to block %d, want 0", idx)
	}
	if idx := m.thinkBlockAtLine(1); idx != -1 {
		t.Errorf("body line click maps to block %d, want -1", idx)
	}
	if idx := m.thinkBlockAtLine(99); idx != -1 {
		t.Errorf("out-of-range click maps to block %d, want -1", idx)
	}
	m.toggleThinkBlock(0)
	if !m.thinkBlocks[0].expanded || !m.thinkBlocks[0].userSet {
		t.Fatalf("click must expand and pin, got %+v", m.thinkBlocks[0])
	}
	if d := m.transcriptDisplay(); !strings.Contains(d, "a\nb") {
		t.Errorf("expanded body must render, got %q", d)
	}
	// Pinned state survives a fold-on-close of a later streaming block.
	m.appendThinkingStreamed("more\n")
	m.appendTranscript("again\n")
	if !m.thinkBlocks[0].expanded {
		t.Error("pinned expansion must survive later closes")
	}
	if m.thinkBlocks[1].expanded {
		t.Error("unpinned completed block must fold")
	}
	// A click on the second header toggles the second block, not the first.
	disp = m.transcriptDisplay()
	lines = strings.Split(disp, "\n")
	second := -1
	for i, l := range lines {
		if isThinkHeaderLine(l) && i > 0 {
			second = i
			break
		}
	}
	if second < 0 {
		t.Fatalf("second header not found in %q", disp)
	}
	if idx := m.thinkBlockAtLine(second); idx != 1 {
		t.Errorf("second header maps to block %d, want 1", idx)
	}
}

func TestThinkBlocks_PromotedReasoningDropped(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = true
	m.appendThinkingStreamed("turn one thoughts\n")
	m.appendTranscript("turn one answer\n")
	m.thinkBlocksTurnStart = len(m.thinkBlocks) // dispatchAgent boundary
	m.appendThinkingStreamed("turn two thoughts\n")
	if len(m.thinkBlocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(m.thinkBlocks))
	}
	m.dropThinkBlocksFrom(m.thinkBlocksTurnStart)
	if len(m.thinkBlocks) != 1 {
		t.Errorf("promotion must drop only the current turn's blocks, got %d", len(m.thinkBlocks))
	}
	if d := m.transcriptDisplay(); strings.Contains(d, "turn two thoughts") {
		t.Errorf("promoted reasoning must not render twice, got %q", d)
	}
}

func TestThinkBlocks_VisibilityFollowsGlobalToggle(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = false
	m.appendThinkingStreamed("quiet\n") // opens folded/hidden
	m.appendTranscript("ok\n")
	m.appendThinkingStreamed("live\n") // streaming block, hidden for now
	if m.thinkBlocks[1].expanded {
		t.Error("block must not open while visibility is off")
	}
	m.showThinking = true
	m.applyThinkVisibility()
	if !m.thinkBlocks[1].expanded {
		t.Error("streaming block must open for live watching when visibility turns on")
	}
	if m.thinkBlocks[0].expanded {
		t.Error("completed block must stay folded")
	}
	m.showThinking = false
	m.applyThinkVisibility()
	if d := m.transcriptDisplay(); strings.Contains(d, "quiet") || strings.Contains(d, "live") {
		t.Errorf("hidden reasoning must not render, got %q", d)
	}
}

func TestThinkBlockHeaderLabels(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = true
	m.appendThinkingStreamed("one line")
	m.appendTranscript("\ndone\n")
	d := m.transcriptDisplay()
	if !strings.Contains(d, thinkFoldedPrefix+"1 line · Ctrl+T") {
		t.Errorf("singular label expected, got %q", d)
	}
}

func TestTranscriptDisplay_SplicesBlocksAtAnchors(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = true
	m.appendTranscript("one\n")
	m.appendThinkingStreamed("a\nb\n")
	m.appendTranscript("two\n")
	m.appendThinkingStreamed("c\n")
	m.appendTranscript("three\n")
	d := m.transcriptDisplay()
	iOne := strings.Index(d, "one\n")
	iHdr2 := strings.Index(d, thinkFoldedPrefix+"2 lines")
	iTwo := strings.Index(d, "two\n")
	iHdr1 := strings.Index(d, thinkFoldedPrefix+"1 line")
	iThree := strings.Index(d, "three\n")
	if !(iOne >= 0 && iHdr2 > iOne && iTwo > iHdr2 && iHdr1 > iTwo && iThree > iHdr1) {
		t.Errorf("block splice order wrong:\n%s", d)
	}
}

func TestThinkBlocks_ExpandCollapseAll(t *testing.T) {
	m := newTestModelForPanels(t)
	m.showThinking = true
	m.appendThinkingStreamed("one\n")
	m.appendTranscript("a\n")
	m.appendThinkingStreamed("two\n")
	m.appendTranscript("b\n")
	m.setAllThinkBlocks(true)
	for i := range m.thinkBlocks {
		if !m.thinkBlocks[i].expanded || !m.thinkBlocks[i].userSet {
			t.Errorf("block %d not pinned open: %+v", i, m.thinkBlocks[i])
		}
	}
	m.setAllThinkBlocks(false)
	for i := range m.thinkBlocks {
		if m.thinkBlocks[i].expanded {
			t.Errorf("block %d still open after collapse-all", i)
		}
	}
}

func TestStartThinkView_ShowsAllBodies(t *testing.T) {
	m := newTestModelForPanels(t)
	// Nothing yet: no attach, just a toast.
	empty, cmd := m.startThinkView()
	if cmd != nil || empty.attached != nil {
		t.Errorf("empty session must not attach, got cmd=%v attached=%v", cmd != nil, empty.attached != nil)
	}
	m.showThinking = true
	m.appendThinkingStreamed("first thought\n")
	m.appendTranscript("a\n")
	m.appendThinkingStreamed("second thought\n")
	m.appendTranscript("b\n")
	m2, cmd := m.startThinkView()
	if cmd == nil {
		t.Fatal("expected an attach refresh cmd")
	}
	if m2.attached == nil || m2.attached.kind != attachPreview {
		t.Fatalf("attached = %+v, want a preview attach", m2.attached)
	}
	snap := m2.attached.buf.Snapshot()
	for _, want := range []string{"first thought", "second thought"} {
		if !strings.Contains(snap, want) {
			t.Errorf("thinking view missing %q:\n%s", want, snap)
		}
	}
}
