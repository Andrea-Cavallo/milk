package main

import (
	"strconv"
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

// TestRenderTip_ShortcutRef pins the tips visualization contract: every
// rendered tip line references the browsing shortcut (tipNavRef), and on a
// narrow terminal the reference yields before the tip text does.
func TestRenderTip_ShortcutRef(t *testing.T) {
	plain := ansi.Strip(renderTip("run {/tasks} now", 120))
	if !strings.Contains(plain, tipNavRef) {
		t.Errorf("tip line %q must reference the browse shortcut %q", plain, tipNavRef)
	}
	if w := rw.StringWidth(plain); w > 120 {
		t.Errorf("tip line width = %d, want ≤ 120 (%q)", w, plain)
	}
	narrow := ansi.Strip(renderTip("run {/tasks} now", 30))
	if strings.Contains(narrow, tipNavRef) {
		t.Errorf("narrow tip line %q must drop the shortcut reference first", narrow)
	}
	if !strings.Contains(narrow, "run /tasks now") {
		t.Errorf("narrow tip line %q lost the tip text", narrow)
	}
	if w := rw.StringWidth(narrow); w > 30 {
		t.Errorf("narrow tip line width = %d, want ≤ 30 (%q)", w, narrow)
	}
}

// TestTipSeek_WrapAndPin pins the manual-browse core behind /tips
// next|prev and Alt+Up/Down: wrap-around stepping, the pin that keeps a
// browsed tip on the line while a contextual hint is live, and the
// cycling-timer restart every move must hand back to tea.Program.
func TestTipSeek_WrapAndPin(t *testing.T) {
	m := newTestModelForPanels(t)
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	list := buildTipList()
	if len(list) < 2 {
		t.Fatal("need at least two tips to step through")
	}
	m.tipIdx = len(list) - 1
	if cmd := m.tipSeek(1); cmd == nil {
		t.Error("tipSeek must return the cycling-timer restart command")
	}
	if m.tipIdx != 0 {
		t.Errorf("tipSeek(1) at the end wrapped to %d, want 0", m.tipIdx)
	}
	m.tipSeek(-1)
	if m.tipIdx != len(list)-1 {
		t.Errorf("tipSeek(-1) at the start wrapped to %d, want %d", m.tipIdx, len(list)-1)
	}
	if !m.tipPinned {
		t.Error("a manual browse must pin the rotation entry")
	}
	// Pinned: the contextual hint cannot displace the browsed tip.
	m.hasInferenceAgent = false
	if got, want := m.currentTip(), list[m.tipIdx%len(list)]; got != want {
		t.Errorf("currentTip() while browsing = %q, want the browsed tip %q", got, want)
	}
	// Unpinned: contextual precedence is back.
	m.tipPinned = false
	if got := m.currentTip(); !strings.Contains(got, "/config init") {
		t.Errorf("currentTip() unpinned = %q, want the contextual /config init hint", got)
	}
}

// TestTipBrowse_RestartsCyclingTimer pins that a manual browse resets the
// rotation countdown: the move bumps tipsGen and re-arms a fresh tick, so
// the tick scheduled before the browse is dropped (stale gen) instead of
// cutting the browse short — while a current-gen tick advances as always.
func TestTipBrowse_RestartsCyclingTimer(t *testing.T) {
	m := newTestModelForPanels(t)
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	stale := tipsTickMsg{gen: m.tipsGen} // the tick in flight before browsing

	m.tipSeek(1)
	if m.tipsGen == stale.gen {
		t.Fatal("a manual browse must bump tipsGen — the old tick would still fire")
	}
	// The pre-browse tick is stale now: dropped without advancing.
	mm, _ := m.updateInner(stale)
	m = mm.(model)
	if m.tipIdx != 1 || !m.tipPinned {
		t.Errorf("stale tick: tipIdx=%d pin=%v, want the browse untouched at 1 and pinned", m.tipIdx, m.tipPinned)
	}
	// The restarted tick is current: the normal advance happens.
	mm, _ = m.updateInner(tipsTickMsg{gen: m.tipsGen})
	m = mm.(model)
	if m.tipIdx != 2 {
		t.Errorf("current tick: tipIdx=%d, want the advance to 2", m.tipIdx)
	}
	// A pick restarts the same way.
	gen := m.tipsGen
	ok, cmd := m.tipPick(5)
	if !ok || cmd == nil {
		t.Fatalf("tipPick(5) = ok=%v cmd=%v, want a jump with a restart command", ok, cmd)
	}
	if m.tipsGen == gen {
		t.Error("tipPick must bump tipsGen too")
	}
}

// TestTipsTickMsg_PinsAndRotation pins that the automatic advance ends a
// manual browse (contextual hints win again) while a busy model freezes
// both the rotation and the pin — browsing while a turn runs must not snap
// back mid-read.
func TestTipsTickMsg_PinsAndRotation(t *testing.T) {
	m := newTestModelForPanels(t)
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	m.tipSeek(3)
	mm, _ := m.updateInner(tipsTickMsg{gen: m.tipsGen})
	m = mm.(model)
	if m.tipPinned {
		t.Error("the automatic advance must clear the manual-browse pin")
	}
	if m.tipIdx != 4 {
		t.Errorf("tipIdx after idle tick = %d, want 4", m.tipIdx)
	}
	m.tipIdx, m.tipPinned = 3, true
	m.busy = true
	mm, _ = m.updateInner(tipsTickMsg{gen: m.tipsGen})
	m = mm.(model)
	if m.tipIdx != 3 || !m.tipPinned {
		t.Errorf("busy tick: tipIdx=%d pin=%v, want frozen at 3 and still pinned", m.tipIdx, m.tipPinned)
	}
}

// TestTipPick pins the numbered jump behind /tips pick <n>: in-range jumps
// (and pin), out-of-range refusals.
func TestTipPick(t *testing.T) {
	m := newTestModelForPanels(t)
	list := buildTipList()
	if ok, _ := m.tipPick(3); !ok || m.tipIdx != 2 {
		t.Errorf("tipPick(3) set tipIdx=%d, want 2", m.tipIdx)
	}
	if !m.tipPinned {
		t.Error("tipPick must pin the picked tip")
	}
	if ok, _ := m.tipPick(0); ok {
		t.Error("tipPick(0) must refuse — tips are numbered from 1")
	}
	if ok, _ := m.tipPick(len(list) + 1); ok {
		t.Error("tipPick past the pool must refuse")
	}
}

// TestSearchTips pins the /tips search matching: case-insensitive
// substring over the raw tip markup (highlighted spans included), indices
// into the built pool, nothing on a miss.
func TestSearchTips(t *testing.T) {
	hits := searchTips("ctrl")
	if len(hits) == 0 {
		t.Fatal(`searchTips("ctrl") found nothing — curated tips name Ctrl keys`)
	}
	list := buildTipList()
	for _, i := range hits {
		if !strings.Contains(strings.ToLower(list[i]), "ctrl") {
			t.Errorf("searchTips returned %d (%q) — no match inside", i, list[i])
		}
	}
	if len(searchTips("CTRL")) != len(hits) {
		t.Error("searchTips must match case-insensitively")
	}
	if got := searchTips("zzz-not-a-tip"); len(got) != 0 {
		t.Errorf("searchTips miss = %v, want empty", got)
	}
}

// TestHandleTipsCmd_Browse pins the /tips browsing surface: next/prev
// step the rotation (quietly while the line is visible, echoing the tip to
// the transcript while it is hidden), pick lists and jumps, search lists,
// and unknown verbs get the usage line.
func TestHandleTipsCmd_Browse(t *testing.T) {
	m := newTestModelForPanels(t)
	m.hasInferenceAgent = true
	m.agents.localAvail = true
	list := buildTipList()
	total := len(list)

	mm, _ := m.handleTipsCmd("next")
	m = mm.(model)
	if m.tipIdx != 1 {
		t.Errorf("/tips next set tipIdx=%d, want 1", m.tipIdx)
	}
	mm, _ = m.handleTipsCmd("prev")
	m = mm.(model)
	if m.tipIdx != 0 {
		t.Errorf("/tips prev set tipIdx=%d, want 0", m.tipIdx)
	}
	// Line visible: the changed line is the feedback, no transcript noise.
	if got := m.transcript.String(); strings.Contains(got, "tip 1/") {
		t.Errorf("visible-line browse must stay out of the transcript: %q", got)
	}

	// Line hidden: the browse echoes the tip so it is never invisible.
	m.showTips = false
	mm, _ = m.handleTipsCmd("next")
	m = mm.(model)
	if !strings.Contains(ansi.Strip(m.transcript.String()), "tip 2/"+strconv.Itoa(total)+":") {
		t.Errorf("hidden-line browse must echo the tip, transcript = %q", m.transcript.String())
	}
	m.showTips = true

	// pick (bare): the numbered catalog with the current entry starred.
	mm, _ = m.handleTipsCmd("pick")
	m = mm.(model)
	out := ansi.Strip(m.transcript.String())
	if !strings.Contains(out, strconv.Itoa(total)+" tips") {
		t.Errorf("pick catalog must state the pool size, got %q", out)
	}
	if !strings.Contains(out, "*") {
		t.Errorf("pick catalog must star the current tip, got %q", out)
	}

	// pick <n>: jumps to the tip and echoes it.
	mm, _ = m.handleTipsCmd("pick " + strconv.Itoa(total))
	m = mm.(model)
	if m.tipIdx != total-1 {
		t.Errorf("pick %d set tipIdx=%d, want %d", total, m.tipIdx, total-1)
	}
	if !strings.Contains(ansi.Strip(m.transcript.String()), "tip "+strconv.Itoa(total)+"/"+strconv.Itoa(total)+":") {
		t.Errorf("pick must echo the chosen tip, transcript = %q", m.transcript.String())
	}

	// Out-of-range numbers are refused with the pool size.
	mm, _ = m.handleTipsCmd("pick 99999")
	m = mm.(model)
	if !strings.Contains(ansi.Strip(m.transcript.String()), "no tip 99999") {
		t.Errorf("out-of-range pick must refuse, transcript = %q", m.transcript.String())
	}

	// search lists matches (plural header) without moving the rotation.
	mm, _ = m.handleTipsCmd("search ctrl")
	m = mm.(model)
	if !strings.Contains(ansi.Strip(m.transcript.String()), `match "ctrl"`) {
		t.Errorf("search must list its matches, transcript = %q", m.transcript.String())
	}

	// pick <text>: a query matching exactly one tip jumps to it.
	uniq := -1
	for i, tip := range list {
		if len(searchTips(tip)) == 1 {
			uniq = i
			break
		}
	}
	if uniq < 0 {
		t.Fatal("no uniquely-identifying tip text in the pool")
	}
	mm, _ = m.handleTipsCmd("pick " + list[uniq])
	m = mm.(model)
	if m.tipIdx != uniq {
		t.Errorf(`pick <text> set tipIdx=%d, want the unique match %d`, m.tipIdx, uniq)
	}

	// A multi-match pick lists candidates instead of guessing.
	mm, _ = m.handleTipsCmd("pick e")
	m = mm.(model)
	if len(searchTips("e")) < 2 {
		t.Fatal(`"e" must match several tips for this assertion`)
	}
	if !strings.Contains(ansi.Strip(m.transcript.String()), `match "e"`) {
		t.Errorf("multi-match pick must list candidates, transcript = %q", m.transcript.String())
	}

	// Unknown verbs (and empty queries) get the usage line.
	mm, _ = m.handleTipsCmd("frobnicate")
	m = mm.(model)
	if !strings.Contains(ansi.Strip(m.transcript.String()), "usage: /tips") {
		t.Errorf("unknown verb must render usage, transcript = %q", m.transcript.String())
	}
}

// TestRegistryTips_IncludeHint pins that derived tips advertise the
// argument grammar: "{/name <hint>} — desc" — the /server gap that started
// this (a hinted command's tip must show how to type it).
func TestRegistryTips_IncludeHint(t *testing.T) {
	byText := strings.Join(registryTips(), "\n")
	for _, s := range commandRegistry {
		if s.hidden || !s.in(hostTUI) || s.hint == "" || tipCoveredCmds()[s.name] {
			continue
		}
		want := "{" + s.name + " " + s.hint + "}"
		if !strings.Contains(byText, want) {
			t.Errorf("registryTips must render %s for %s (hint %q)", want, s.name, s.hint)
		}
	}
}

// TestHelpDocumentsTipBrowsing pins discoverability: the browsing verbs
// and the Alt+Up/Down shortcuts show up in /help, next to the tip line's
// own shortcut reference (tipNavRef).
func TestHelpDocumentsTipBrowsing(t *testing.T) {
	help := buildInteractiveHelp()
	for _, want := range []string{"/tips next", "/tips prev", "/tips pick", "/tips search", "Alt+Up", "Alt+Down"} {
		if !strings.Contains(help, want) {
			t.Errorf("interactiveHelp must document %q", want)
		}
	}
}
