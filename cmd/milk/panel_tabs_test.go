package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Tabbed panel docking (ADR-0079): a theme that renders the tab strip docks
// every side panel into one shared slot — the front tab owns the columns,
// the other open panels wait behind their tabs. These tests pin the shared
// slot, the tab-shaped F-key semantics, the tab-bar click, the instant tab
// swap (no animation), and that theme "off" keeps the side-by-side layout.

// withPanelTabs activates the tabbed (supermilk) theme for one test and
// restores whatever theme was active afterwards.
func withPanelTabs(t *testing.T) {
	t.Helper()
	old := th.name
	setTheme("supermilk")
	t.Cleanup(func() { setTheme(old) })
}

// TestTabbedTheme_PanelsShareOneSlot: with two panels open only the front
// tab occupies layout columns — one panel slot wide, one panel in regionAt,
// and the hidden panel still open behind its tab.
func TestTabbedTheme_PanelsShareOneSlot(t *testing.T) {
	withPanelTabs(t)
	m := newTestModelForPanels(t)
	m.width = 200
	m.panelMemory = true
	m.panelTasks = true
	m.activatePanelTab(regionMemory)

	if !m.panelVisible(regionMemory) {
		t.Fatal("expected the front tab (memory) to be visible")
	}
	if m.panelVisible(regionTasks) {
		t.Fatal("expected the tasks panel to wait behind its tab, not sit beside memory")
	}
	if !m.panelRawOpen(regionTasks) {
		t.Fatal("the hidden tab's panel must stay open")
	}
	if got := m.mainWidth(); got != 200-panelSlotWidth {
		t.Errorf("mainWidth=%d, want %d (one shared slot)", got, 200-panelSlotWidth)
	}
	region, _ := m.regionAt(m.mainWidth() + 1)
	if region != regionMemory {
		t.Errorf("panel columns map to %v, want the front tab regionMemory", region)
	}
}

// TestPanelTabsOff_PanelsStaySiblings: without tabs (theme "off") nothing
// about the shared slot applies — two open panels are two visible columns.
func TestPanelTabsOff_PanelsStaySiblings(t *testing.T) {
	m := newTestModelForPanels(t)
	m.width = 200
	m.panelMemory = true
	m.panelTasks = true

	if !m.panelVisible(regionMemory) || !m.panelVisible(regionTasks) {
		t.Fatal("expected both open panels visible without tabs")
	}
	if got := m.mainWidth(); got != 200-2*panelSlotWidth {
		t.Errorf("mainWidth=%d, want %d (two sibling slots)", got, 200-2*panelSlotWidth)
	}
	if region, _ := m.regionAt(m.mainWidth() + panelSlotWidth + 1); region != regionTasks {
		t.Errorf("second slot maps to %v, want regionTasks", region)
	}
}

// TestTabbedTheme_FKeyOnHiddenTabBringsToFront: the toggle is tab-shaped —
// on an open-but-hidden panel the F-key switches to it instead of closing a
// panel the user cannot see; only on the front tab does it close.
func TestTabbedTheme_FKeyOnHiddenTabBringsToFront(t *testing.T) {
	withPanelTabs(t)
	m := newTestModelForPanels(t)
	m.width = 200

	updated, _ := m.handlePanelCmd("memory")
	m = updated.(model)
	updated, _ = m.handlePanelCmd("tasks")
	m = updated.(model)
	if m.panelActive() != regionTasks {
		t.Fatalf("front tab = %v, want regionTasks", m.panelActive())
	}

	// F1 on the hidden memory panel: switch, don't close.
	updated, _ = m.handlePanelCmd("memory")
	m = updated.(model)
	if !m.panelRawOpen(regionMemory) || m.panelActive() != regionMemory {
		t.Fatalf("F-key on a hidden tab must bring it to the front, got open=%v front=%v",
			m.panelRawOpen(regionMemory), m.panelActive())
	}
	if !m.panelRawOpen(regionTasks) {
		t.Fatal("switching tabs must not close the other panel")
	}

	// F1 on the front panel: the plain toggle, closing it — the slot falls
	// back to the tab viewed before (the MRU), like closing a browser tab.
	updated, _ = m.handlePanelCmd("memory")
	m = updated.(model)
	if m.panelRawOpen(regionMemory) {
		t.Fatal("F-key on the front tab must close it")
	}
	if m.panelActive() != regionTasks {
		t.Errorf("front tab = %v after close, want the previously viewed regionTasks", m.panelActive())
	}
}

// TestTabbedTheme_TabBarClickSwitches: a click on a tab label does what that
// tab's F-key does — open the panel when its tab is closed, bring it to the
// front when it is already open.
func TestTabbedTheme_TabBarClickSwitches(t *testing.T) {
	withPanelTabs(t)
	m := layoutTestModel(t, 200, 40)
	m.panelMemory = true
	m.activatePanelTab(regionMemory)
	mw := m.mainWidth()

	clickTab := func(col int) {
		t.Helper()
		// The tab strip is the panel's first content row, on screen row
		// panelRowStart (2) while the panel is scrolled to the top. A click
		// is press+release on the same cell.
		press := mouseEvent{X: mw + col, Y: 2, Button: tea.MouseLeft, Action: mousePress}
		release := mouseEvent{X: mw + col, Y: 2, Button: tea.MouseLeft, Action: mouseRelease}
		updated, _ := m.handleMouse(press.msg())
		m = *updated.(*model)
		updated, _ = m.handleMouse(release.msg())
		m = *updated.(*model)
	}

	clickTab(29) // "git" occupies columns 29-31 of the strip
	if !m.panelGit {
		t.Fatal("clicking a closed tab must open its panel")
	}
	if m.panelActive() != regionGit {
		t.Fatalf("front tab = %v, want regionGit after clicking its tab", m.panelActive())
	}

	clickTab(0) // "memory"
	if m.panelActive() != regionMemory {
		t.Fatalf("front tab = %v, want regionMemory after clicking its tab", m.panelActive())
	}
	if !m.panelGit {
		t.Fatal("switching tabs must leave the other panel open")
	}
}

// TestPanelTabAt_CoversEveryTabLabel: the click hit-test walks the same
// single-space join the strip renders with — every label maps to its own
// region and the gaps between labels map to none.
func TestPanelTabAt_CoversEveryTabLabel(t *testing.T) {
	x := 0
	for _, tab := range panelTabs {
		if got := panelTabAt(x); got != tab.region {
			t.Errorf("column %d (%s): got %v, want %v", x, tab.name, got, tab.region)
		}
		w := len([]rune(tab.name))
		if got := panelTabAt(x + w - 1); got != tab.region {
			t.Errorf("column %d (last of %s): got %v, want %v", x+w-1, tab.name, got, tab.region)
		}
		if got := panelTabAt(x + w); got != regionNone {
			t.Errorf("column %d (gap after %s): got %v, want regionNone", x+w, tab.name, got)
		}
		x += w + 1
	}
	if got := panelTabAt(x); got != regionNone {
		t.Errorf("column %d (past the strip): got %v, want regionNone", x, got)
	}
}

// TestTabbedTheme_TabSwitchAnimatesNothing: switching tabs swaps content
// inside the occupied slot — no slide, no ghost. The slot itself still
// animates: appearing slides in, the last close shrinks out.
func TestTabbedTheme_TabSwitchAnimatesNothing(t *testing.T) {
	withPanelTabs(t)
	oldAnim := animationsEnabled
	animationsEnabled = true
	t.Cleanup(func() { animationsEnabled = oldAnim })

	m := newTestModelForPanels(t)
	m.width, m.height = 200, 40

	// Each step gets a fresh animation state: trackAnimations hands its
	// command out only when it arms the shared tick, and a tick left armed
	// by a previous step would mask every later one.
	step := func(prev model) tea.Cmd {
		m.anim = newAnimState()
		return m.trackAnimations(&prev)
	}

	// First panel: the slot appears — a slide-in must run.
	prev := m
	m.panelMemory = true
	m.activatePanelTab(regionMemory)
	if cmd := step(prev); cmd == nil {
		t.Fatal("opening the first panel must animate (slot appears)")
	}

	// Tab switch: instant.
	prev = m
	m.panelTasks = true
	m.activatePanelTab(regionTasks)
	if cmd := step(prev); cmd != nil {
		t.Fatal("a tab switch must swap in place, not animate")
	}

	// Closing the front tab while another waits: still just a swap.
	prev = m
	m.panelTasks = false
	if cmd := step(prev); cmd != nil {
		t.Fatal("closing the front tab of an occupied slot must swap, not animate")
	}

	// Closing the last panel: the slot disappears — the exit must run.
	prev = m
	m.panelMemory = false
	if cmd := step(prev); cmd == nil {
		t.Fatal("closing the last panel must animate (slot disappears)")
	}
}

// TestTabbedTheme_AutoOpenTakesSlotOnce: content-driven auto-open brings a
// freshly opened panel to the front — but only on the closed→open
// transition; later content for the same already-open panel must never yank
// the tab back from wherever the user has moved on to.
func TestTabbedTheme_AutoOpenTakesSlotOnce(t *testing.T) {
	withPanelTabs(t)
	m := newTestModelForPanels(t)
	m.width = 200
	m.panelMemory = true
	m.activatePanelTab(regionMemory)

	m.autoOpenPanel(regionTasks) // tasks content arrives while memory is front
	if m.panelActive() != regionTasks {
		t.Fatalf("front tab = %v, want the freshly opened regionTasks", m.panelActive())
	}

	m.activatePanelTab(regionMemory) // user goes back to the memory tab
	m.autoOpenPanel(regionTasks)     // more tasks content arrives
	if m.panelActive() != regionMemory {
		t.Fatalf("front tab = %v, want regionMemory (auto-open must not yank an open tab back)", m.panelActive())
	}
}

// TestTabbedTheme_PanelStateRoundTripsFrontTab: the front tab of the shared
// slot is part of the panel preference and comes back across sessions — but
// only for manually chosen panels, never for auto-opened ones (pinned
// together with the open-flag rule by TestPanelState_OnlyManualTogglesPersisted).
func TestTabbedTheme_PanelStateRoundTripsFrontTab(t *testing.T) {
	withPanelTabs(t)
	m := newTestModelForPanels(t)
	m.panelStatePath = filepath.Join(t.TempDir(), "panel-state.json")

	updated, _ := m.handlePanelCmd("memory")
	m = updated.(model)
	updated, _ = m.handlePanelCmd("git")
	m = updated.(model)
	if m.panelActive() != regionGit {
		t.Fatalf("front tab = %v, want regionGit", m.panelActive())
	}
	if err := m.savePanelState(); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(m.panelStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"active":"git"`) {
		t.Errorf("expected the front tab in the state file, got %s", data)
	}

	m2 := newTestModelForPanels(t)
	m2.width = 200 // wide enough for the git panel's layout guard
	m2.panelStatePath = m.panelStatePath
	m2.loadPanelState(m2.panelStatePath)
	if m2.panelActive() != regionGit {
		t.Errorf("front tab after restore = %v, want regionGit", m2.panelActive())
	}
	if !m2.panelVisible(regionGit) {
		t.Error("expected the restored front tab's panel to occupy the shared slot")
	}
	if m2.panelVisible(regionMemory) {
		t.Error("the other restored panel must wait behind its tab")
	}
}
