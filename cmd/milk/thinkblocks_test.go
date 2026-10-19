package main

import (
	"strings"
	"testing"
)

// Thinking-as-a-view tests (ADR-0067, issue #208): blocks live out of band,
// render as fold containers, fold on close, and stay click-toggleable.

func TestThinkBlocks_StreamIntoBlockThenFoldOnClose(t *testing.T) {
	m := newTestModelForPanels(t)
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
	if !strings.Contains(disp, thinkFoldedPrefix+"2 lines") {
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

// Blocks are copied by value (append, slice growth); a value-embedded
// strings.Builder panicked on the second write after such a copy.
func TestThinkBlocks_MultiChunkAndSliceGrowth(t *testing.T) {
	m := newTestModelForPanels(t)
	for run := 0; run < 5; run++ {
		m.appendThinkingStreamed("a")
		m.appendThinkingStreamed("b")
		m.appendThinkingStreamed("c\n")
		m.appendTranscript("answer\n")
	}
	if len(m.thinkBlocks) != 5 {
		t.Fatalf("blocks = %d, want 5", len(m.thinkBlocks))
	}
	for i, b := range m.thinkBlocks {
		if got := b.text(); got != "abc\n" {
			t.Errorf("block %d buf = %q", i, got)
		}
	}
}

// There is no hidden mode: a block always renders its header row — folded
// with its line count once complete. The old one-line placeholder is gone
// for good, and the folded body stays inspectable, not dumped inline.
func TestThinkBlocks_NoHiddenMode(t *testing.T) {
	m := newTestModelForPanels(t)
	m.appendThinkingStreamed("secret\nthoughts\n")
	m.appendTranscript("answer\n") // closes + folds
	disp := m.transcriptDisplay()
	if !strings.Contains(disp, thinkFoldedPrefix+"2 lines") {
		t.Errorf("completed block must render its fold header, got %q", disp)
	}
	if strings.Contains(disp, "[thinking") {
		t.Errorf("no hidden placeholder may render anywhere, got %q", disp)
	}
	if strings.Contains(disp, "secret") {
		t.Errorf("folded body must not render, got %q", disp)
	}
}

// Fold headers are information-only: glyph, line count, fold/streaming
// state — never a key hint (key hints live in /help and the tips line).
func TestThinkBlockHeaders_InfoOnly(t *testing.T) {
	m := newTestModelForPanels(t)
	m.appendThinkingStreamed("one\ntwo\nthree\n")
	disp := m.transcriptDisplay()
	if !strings.Contains(disp, thinkExpandedPrefix+"3 lines · streaming") {
		t.Errorf("streaming header must carry the line count, got %q", disp)
	}
	m.appendTranscript("done\n")
	disp = m.transcriptDisplay()
	if !strings.Contains(disp, thinkFoldedPrefix+"3 lines") {
		t.Errorf("folded header must carry the line count, got %q", disp)
	}
	if strings.Contains(disp, "Ctrl+T") || strings.Contains(disp, "/think") {
		t.Errorf("headers must not carry key hints, got %q", disp)
	}
}

// Ctrl+T toggles the most recent block — the streaming one while reasoning
// is arriving, else the last completed block — and pins it like a header
// click (explicit action wins over the auto-fold rules).
func TestToggleLastThinkBlock_TogglesMostRecent(t *testing.T) {
	m := newTestModelForPanels(t)
	m.appendThinkingStreamed("first\n") // streaming block opens live
	if !m.thinkBlocks[0].expanded {
		t.Fatal("streaming block must open live")
	}
	m = m.toggleLastThinkBlock() // folds the streaming block
	if m.thinkBlocks[0].expanded || !m.thinkBlocks[0].userSet {
		t.Errorf("Ctrl+T must fold and pin the streaming block, got %+v", m.thinkBlocks[0])
	}
	m.appendTranscript("done\n")
	m.appendThinkingStreamed("second\n")
	m = m.toggleLastThinkBlock() // the streaming block again, not block 0
	if m.thinkBlocks[1].expanded {
		t.Error("Ctrl+T must toggle the streaming block, not a completed one")
	}
	m.appendTranscript("done\n")
	m = m.toggleLastThinkBlock() // nothing open → the last block
	if !m.thinkBlocks[1].expanded || !m.thinkBlocks[1].userSet {
		t.Errorf("Ctrl+T with nothing streaming must toggle the last block, got %+v", m.thinkBlocks[1])
	}
	if !m.thinkBlocks[0].userSet || m.thinkBlocks[0].expanded {
		t.Errorf("earlier blocks must be untouched, got %+v", m.thinkBlocks[0])
	}
}

func TestThinkBlocks_ClickTogglesAndPins(t *testing.T) {
	// Wide viewport: no line wraps, so display line indexes and the wrapped
	// coordinate space the mouse handler feeds thinkBlockAtLine coincide.
	m := layoutTestModel(t, 200, 50)
	m.colorizeMode = ColorizeOff
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

// A block pinned open mid-stream stays open past its close: the fold is
// per-block state (userSet wins over auto-fold), not a global mode.
func TestThinkBlocks_PinnedOpenSurvivesClose(t *testing.T) {
	m := newTestModelForPanels(t)
	m.appendThinkingStreamed("watching\n")
	m.toggleThinkBlock(0) // fold
	m.toggleThinkBlock(0) // pin open again
	m.appendTranscript("done\n")
	if !m.thinkBlocks[0].expanded || !m.thinkBlocks[0].userSet {
		t.Fatalf("a block pinned open must stay expanded after close, got %+v", m.thinkBlocks[0])
	}
	if d := m.transcriptDisplay(); !strings.Contains(d, "watching") {
		t.Errorf("pinned body must render, got %q", d)
	}
}

func TestThinkBlockHeaderLabels(t *testing.T) {
	m := newTestModelForPanels(t)
	m.appendThinkingStreamed("one line")
	m.appendTranscript("\ndone\n")
	d := m.transcriptDisplay()
	if !strings.Contains(d, thinkFoldedPrefix+"1 line") {
		t.Errorf("singular label expected, got %q", d)
	}
}

func TestTranscriptDisplay_SplicesBlocksAtAnchors(t *testing.T) {
	m := newTestModelForPanels(t)
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
