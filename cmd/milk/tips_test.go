package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/updater"
)

// The idle tip line (ADR-0061) is pinned here: the built list is complete
// and non-duplicating, the registry-derived half cannot drift from the
// command registry, the markup renders and truncates as documented, the
// contextual hints outrank the rotation, and the hint region's line count
// is what both View and viewportHeight see.

func TestBuildTipList_CuratedFirstNoDuplicates(t *testing.T) {
	list := buildTipList()
	if len(list) < len(curatedTips) {
		t.Fatalf("buildTipList() = %d tips, want at least the %d curated ones", len(list), len(curatedTips))
	}
	for i, tip := range curatedTips {
		if list[i] != tip {
			t.Errorf("buildTipList()[%d] = %q, want curated tip %q — curated must lead the rotation", i, list[i], tip)
		}
	}
	seen := map[string]bool{}
	for _, tip := range list {
		if seen[tip] {
			t.Errorf("duplicate tip %q in the rotation", tip)
		}
		seen[tip] = true
	}
}

func TestTipCoveredCmds_ExtractedFromCuratedMarkup(t *testing.T) {
	covered := tipCoveredCmds()
	for _, name := range []string{"/panel", "/learn", "/forget", "/escalate", "/think", "/bg", "/export"} {
		if !covered[name] {
			t.Errorf("curated tips name %s but tipCoveredCmds does not mark it covered", name)
		}
	}
	if covered["/tasks"] {
		t.Error("/tasks is not named by any curated tip — must not be marked covered")
	}
}

func TestRegistryTips_SkipHiddenCoveredAndNonTUI(t *testing.T) {
	tips := registryTips()
	byText := strings.Join(tips, "\n")
	// Covered by a curated tip → no derived duplicate.
	if strings.Contains(byText, "{/learn") {
		t.Error("registryTips contains a /learn tip although a curated tip covers /learn")
	}
	// Hidden (deprecation window) → never advertised.
	for _, s := range commandRegistry {
		if s.hidden && strings.Contains(byText, "{"+s.name) {
			t.Errorf("registryTips advertises hidden command %s", s.name)
		}
	}
	// Uncovered commands do get their derived tip, built from registry data.
	uncovered := ""
	for _, s := range commandRegistry {
		if !s.hidden && s.in(hostTUI) && !tipCoveredCmds()[s.name] && s.desc != "" {
			uncovered = s.name
			break
		}
	}
	if uncovered == "" {
		t.Fatal("no uncovered TUI command found — cannot assert derived tips exist")
	}
	if !strings.Contains(byText, "{"+uncovered) {
		t.Errorf("registryTips has no tip for uncovered command %s", uncovered)
	}
}

func TestTipSegments_Markup(t *testing.T) {
	segs := tipSegments(`plain {hot} tail`)
	want := []tipSeg{{s: "plain "}, {s: "hot", hl: true}, {s: " tail"}}
	if len(segs) != len(want) {
		t.Fatalf("tipSegments = %+v, want %+v", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("seg[%d] = %+v, want %+v", i, segs[i], want[i])
		}
	}
	// Stray braces are literal text, never a panic or a lost tail.
	if got := tipSegments("a {b"); strings.Join(plainOf(got), "") != "a {b" || hasHL(got) {
		t.Errorf("unterminated span = %+v", got)
	}
	if got := tipSegments("}x{"); strings.Join(plainOf(got), "") != "}x{" || hasHL(got) {
		t.Errorf("stray braces = %+v", got)
	}
}

// plainOf returns the texts of all segments; hasHL reports whether any
// segment is a highlight span.
func plainOf(segs []tipSeg) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.s
	}
	return out
}

func hasHL(segs []tipSeg) bool {
	for _, s := range segs {
		if s.hl {
			return true
		}
	}
	return false
}

func TestRenderTip_HighlightAndTruncate(t *testing.T) {
	line := renderTip("run {/tasks} now", 80)
	if !strings.Contains(line, yellow("/tasks")) {
		t.Errorf("highlight span not styled yellow: %q", line)
	}
	if !strings.Contains(ansi.Strip(line), "tip") {
		t.Errorf("missing tip label: %q", line)
	}
	// Narrow width: truncated with an ellipsis, never past the budget.
	narrow := renderTip(strings.Repeat("tip text ", 20), 30)
	plain := ansi.Strip(narrow)
	if !strings.HasSuffix(plain, "…") {
		t.Errorf("truncated tip must end in ellipsis: %q", plain)
	}
	if w := rw.StringWidth(plain); w > 30 {
		t.Errorf("truncated tip width = %d, want ≤ 30 (%q)", w, plain)
	}
}

func TestCurrentTip_ContextualPrecedence(t *testing.T) {
	m := newTestModelForPanels(t)
	// No inference agent configured → setup hint outranks the rotation.
	m.hasInferenceAgent = false
	if got := m.currentTip(); !strings.Contains(got, "/config init") {
		t.Errorf("currentTip() = %q, want the /config init setup hint", got)
	}
	// Update available → install hint, fresh with the release tag.
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	m.pendingUpdate = &updater.Release{Tag: "v9.9.9"}
	if got := m.currentTip(); !strings.Contains(got, "v9.9.9") || !strings.Contains(got, "/update install") {
		t.Errorf("currentTip() = %q, want the /update install hint with the tag", got)
	}
	// Nothing urgent → the rotation's current entry.
	m.pendingUpdate = nil
	if got, want := m.currentTip(), buildTipList()[m.tipIdx%len(buildTipList())]; got != want {
		t.Errorf("currentTip() = %q, want rotating tip %q", got, want)
	}
}

func TestCurrentTip_RotationWraps(t *testing.T) {
	m := newTestModelForPanels(t)
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	list := buildTipList()
	m.tipIdx = len(list) + 1
	if got, want := m.currentTip(), list[1]; got != want {
		t.Errorf("rotating past the pool wrapped to %q, want %q", got, want)
	}
}

func TestHintRegion(t *testing.T) {
	m := newTestModelForPanels(t)
	// Default: the tip line occupies exactly one row.
	hints := m.hintRegion()
	if len(hints) != 1 {
		t.Fatalf("hintRegion() = %d lines, want 1 tip line", len(hints))
	}
	// Completion hints win the slot while they are up.
	m.tabHints = []string{"a", "b"}
	if got := m.hintRegion(); len(got) != 2 || got[0] != "a" {
		t.Errorf("hintRegion() with completion hints = %v, want the tabHints", got)
	}
	m.tabHints = nil
	// Tips disabled → no row at all.
	m.showTips = false
	if got := m.hintRegion(); got != nil {
		t.Errorf("hintRegion() with tips off = %v, want nil", got)
	}
	m.showTips = true
	// PTY pane: its layout branch renders no hint rows — neither kind.
	m.ptyPane = &ptyPaneState{}
	if got := m.hintRegion(); got != nil {
		t.Errorf("hintRegion() under the PTY pane = %v, want nil", got)
	}
}

// TestHandleTipsCmd pins the /tips surface: query answers, the toggle, and
// that the toggle flips both the live model and the persisted config
// default (the same saveLocalOrGlobal contract /colorize has).
func TestHandleTipsCmd(t *testing.T) {
	m := newTestModelForPanels(t)
	if !m.showTips {
		t.Fatal("tips must default on")
	}
	mm, _ := m.handleTipsCmd("off")
	m = mm.(model)
	if m.showTips {
		t.Error("/tips off did not hide the line")
	}
	if m.st.cfg.ShowTips == nil || *m.st.cfg.ShowTips {
		t.Error("/tips off did not persist show_tips=false to config")
	}
	mm, _ = m.handleTipsCmd("on")
	m = mm.(model)
	if !m.showTips {
		t.Error("/tips on did not show the line again")
	}
	// Bare query reports state and does not toggle.
	mm, _ = m.handleTipsCmd("")
	m = mm.(model)
	if !m.showTips {
		t.Error("/tips query must not toggle")
	}
	if out := m.transcript.String(); !strings.Contains(out, "tip line: on") {
		t.Errorf("query output = %q, want the current state", out)
	}
}

func TestShowTipsEnabled_Default(t *testing.T) {
	var c config.Config
	if !c.ShowTipsEnabled() {
		t.Error("unset show_tips must default to enabled")
	}
	off := false
	c.ShowTips = &off
	if c.ShowTipsEnabled() {
		t.Error("show_tips=false must disable")
	}
	on := true
	c.ShowTips = &on
	if !c.ShowTipsEnabled() {
		t.Error("show_tips=true must enable")
	}
}
