package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// countTicks feeds animation ticks until the model stops asking for more,
// returning the number of ticks it took.
func countTicks(t *testing.T, m model) int {
	t.Helper()
	for i := 1; i <= 600; i++ {
		next, cmd := m.Update(animTickMsg{})
		m = next.(model)
		if cmd == nil {
			return i
		}
	}
	t.Fatal("animation never settled")
	return -1
}

// TestPanelSlide_ClosingLeftmostPanelGetsAGhost: the exit animation used to
// be gated on the closing panel being the right-most one — every other close
// popped instantly. Closing the left-most panel must animate too (and, since
// a left-most close shifts nobody, it must not fabricate a glide for the
// neighbours).
func TestPanelSlide_ClosingLeftmostPanelGetsAGhost(t *testing.T) {
	withAnimations(t)
	withTTY(t)
	m := layoutTestModel(t, 140, 40)
	m.panelMemory = true
	m.panelTasks = true
	m.syncLayout()

	prev := m
	m.panelMemory = false
	m.syncLayout()
	if cmd := m.trackAnimations(&prev); cmd == nil {
		t.Fatal("closing a non-rightmost panel must start its slide-out")
	}
	pa := m.anim.panels[regionMemory]
	if pa == nil || pa.ghost == nil {
		t.Fatal("the closing leftmost panel keeps a ghost of its last frame")
	}
	if pa.move {
		t.Error("the closer shrinks out, it does not glide")
	}
	if nb := m.anim.panels[regionTasks]; nb != nil {
		t.Errorf("closing the leftmost panel moves nobody, got an unexpected glide for tasks: %+v", nb)
	}
	m = runTicks(t, m)
	if len(m.anim.panels) != 0 {
		t.Error("the ghost is gone once it has slid out")
	}
}

// TestPanelSlide_ClosingShiftsLeftPanelsWithAGlide: the panel layout is
// right-anchored, so closing the RIGHT panel makes the panel to its LEFT jump
// one column. It must glide there instead — full-width ghost at the old
// column, static slot blanked — and everything must settle.
func TestPanelSlide_ClosingShiftsLeftPanelsWithAGlide(t *testing.T) {
	withAnimations(t)
	withTTY(t)
	m := layoutTestModel(t, 140, 40)
	m.panelMemory = true
	m.panelTasks = true
	m.syncLayout()

	prev := m
	oldMemoryX := prev.panelX(regionMemory)
	m.panelTasks = false
	m.syncLayout()
	if cmd := m.trackAnimations(&prev); cmd == nil {
		t.Fatal("closing the rightmost panel must start the transitions")
	}
	pa := m.anim.panels[regionMemory]
	if pa == nil {
		t.Fatal("the panel left of the closer must glide to its new column")
	}
	if !pa.move || pa.travel != panelSlotWidth || pa.ghost == nil {
		t.Fatalf("glide entry wrong: move=%v travel=%d ghostSet=%v", pa.move, pa.travel, pa.ghost != nil)
	}
	if pa.ghostX != oldMemoryX {
		t.Errorf("ghost must start at the old column: got %d want %d", pa.ghostX, oldMemoryX)
	}
	if !m.anim.isMoving(regionMemory) {
		t.Error("joinPanel must treat the gliding panel as moving (static slot blanked)")
	}
	// Mid-flight frame: both ghosts are on screen — the gliding panel at its
	// old column and the closer shrinking out — while the static slots stay
	// clean.
	frame := m.viewString()
	if !strings.Contains(ansi.Strip(frame), "memory") {
		t.Errorf("mid-flight frame must show the gliding panel's ghost:\n%s", ansi.Strip(frame))
	}
	m = runTicks(t, m)
	if len(m.anim.panels) != 0 {
		t.Error("the glide and the shrink-out must settle")
	}
	settled := m.viewString()
	if !strings.Contains(ansi.Strip(settled), "memory") {
		t.Errorf("settled frame must show the panel in its new column:\n%s", ansi.Strip(settled))
	}
}

// TestPanelSlide_OpenGlidesLeftNeighbours: the layout is right-anchored, so
// opening a panel shifts every open panel left of it one column LEFT — those
// must glide into position exactly like they do on close, not teleport.
func TestPanelSlide_OpenGlidesLeftNeighbours(t *testing.T) {
	withAnimations(t)
	withTTY(t)
	m := layoutTestModel(t, 140, 40)
	m.panelMemory = true
	m.syncLayout()

	prev := m
	m.panelTasks = true // renders right of memory → memory shifts one column left
	m.syncLayout()
	if cmd := m.trackAnimations(&prev); cmd == nil {
		t.Fatal("opening a panel must start the slide")
	}
	opener, ok := m.anim.panels[regionTasks]
	if !ok || opener.move || opener.ghost != nil {
		t.Fatalf("the opening panel keeps its in-slot slide, got %+v", opener)
	}
	mp, ok := m.anim.panels[regionMemory]
	if !ok || !mp.move || mp.travel != -panelSlotWidth || mp.ghost == nil {
		t.Fatalf("memory must glide one column left (travel=%d), got %+v", -panelSlotWidth, mp)
	}
	if mp.ghostX != prev.panelX(regionMemory) {
		t.Errorf("glide starts from memory's old column %d, got %d", prev.panelX(regionMemory), mp.ghostX)
	}
	if !m.anim.isMoving(regionMemory) {
		t.Error("memory's static slot must be blank while it glides")
	}
	m = runTicks(t, m)
	if len(m.anim.panels) != 0 {
		t.Error("both slides must settle")
	}
}

// TestPanelSlide_ExitSettlesQuickly pins the retuned springs (frequency 24,
// settle 0.04 / snap 0.12): the whole exit must be over in well under a
// second of frames — the old 9 / 0.01 / 0.01 combination crawled.
func TestPanelSlide_ExitSettlesQuickly(t *testing.T) {
	withAnimations(t)
	withTTY(t)
	m := layoutTestModel(t, 140, 40)
	m.panelTasks = true
	m.syncLayout()

	prev := m
	m.panelTasks = false
	m.syncLayout()
	if cmd := m.trackAnimations(&prev); cmd == nil {
		t.Fatal("close must start the slide-out")
	}
	frames := countTicks(t, m)
	t.Logf("exit settled in %d frames (%.0f ms at %d fps)", frames, float64(frames)*1000/animFPS, animFPS)
	if frames > 30 {
		t.Errorf("exit too slow: %d frames (> %.0f ms)", frames, 30*1000.0/animFPS)
	}
}
