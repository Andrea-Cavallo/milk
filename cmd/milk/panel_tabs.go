package main

// Tabbed panel docking (ADR-0079): a theme whose panel title row is a tab
// strip (theme_chrome.go's panelTabStrip — exactly the "modern" skins) means
// the panels to be tabs of *one* shared slot, not six sibling columns each
// wearing the same six tabs. Six tab labels over six stacked panels is what a
// tab bar means; six copies of the strip over six side-by-side panels is not.
//
//   - one slot: at most one panel occupies layout columns — the front tab
//     (panelActive). The other open panels keep their flags, scroll offsets
//     and content behind their tabs.
//   - switching: F1-F6 / /panel <name> on a hidden tab brings it to the
//     front (on the front tab they still close it), and a click on a tab
//     label does the same as its F-key. Content-driven auto-open takes the
//     slot only on a real closed→open transition, so live content for an
//     already-open panel (workflow chunks, task updates) never yanks the tab
//     back from wherever the user is.
//   - closing the front tab hands the slot to the most recently viewed other
//     open panel (panelTabMRU) — like closing a browser tab.
//
// Without tabs (theme "off") nothing here applies: panels stay siblings,
// joined left-to-right in panelRenderOrder, and every predicate below
// degenerates to the plain open flag.

// panelTabsActive reports whether the active theme renders the panel tab
// strip and therefore docks panels into one shared slot. Tied to th.modern
// because that is exactly the condition panelTitleLine renders the strip
// under — the tab bar and the shared slot can never disagree.
func panelTabsActive() bool { return th.modern }

// panelRawOpen reports a panel's raw open flag: no width guards, no tab
// filter. This is the persisted state (ADR-0078) and what notifications and
// manual overrides reason about.
func (m *model) panelRawOpen(region panelRegion) bool {
	p := m.panelOpenPtr(region)
	return p != nil && *p
}

// panelWidth is a region's rendered slot width (body + scrollbar column),
// one place for the per-region constants so layout sums and hit tests can
// never disagree.
func panelWidth(region panelRegion) int {
	switch region {
	case regionMemory:
		return memoryPanelWidth
	case regionTasks:
		return tasksPanelWidth
	case regionBackground:
		return backgroundPanelWidth
	case regionWorkflow:
		return workflowPanelWidth
	case regionProject:
		return projectPanelWidth
	case regionGit:
		return gitPanelWidth
	}
	return 0
}

// activatePanelTab brings region's tab to the front of the shared slot by
// pushing it onto the most-recently-viewed stack. The stack is rebuilt into
// a fresh slice every time: the model travels by value copy through Update,
// and sharing a backing array with the previous copy would let two renders
// reorder each other's history.
func (m *model) activatePanelTab(region panelRegion) {
	if region == regionNone {
		return
	}
	mru := make([]panelRegion, 0, len(m.panelTabMRU)+1)
	mru = append(mru, region)
	for _, r := range m.panelTabMRU {
		if r != region {
			mru = append(mru, r)
		}
	}
	m.panelTabMRU = mru
}

// panelActive is the panel in front of the shared slot: the most recently
// viewed tab whose panel is actually open (raw flag — every layout predicate
// derives from this, so it must not consult the width guards, which derive
// from it in turn), falling back to panelRenderOrder for state restored or
// seeded without tab history. regionNone when no panel is open at all.
func (m *model) panelActive() panelRegion {
	for _, r := range m.panelTabMRU {
		if m.panelRawOpen(r) {
			return r
		}
	}
	for _, r := range panelRenderOrder {
		if m.panelRawOpen(r) {
			return r
		}
	}
	return regionNone
}

// panelInLayout is the layout membership test the width sums derive from:
// raw open flag plus the tabbed front-tab filter, with no width guards —
// those derive from this, so asking them here would recurse.
func (m *model) panelInLayout(region panelRegion) bool {
	if !m.panelRawOpen(region) {
		return false
	}
	return !panelTabsActive() || region == m.panelActive()
}

// panelVisible reports whether a panel occupies layout columns right now:
// open and width-permitted (panelOpen), and on a tabbed theme the front tab.
// Everything that measures or joins the layout (mainWidth, regionAt,
// viewBody, panelX, panelAltBackground, the animation tracker) asks this;
// panelOpen stays the plain "is this panel open" for state and notifications.
func (m *model) panelVisible(region panelRegion) bool {
	if !m.panelOpen(region) {
		return false
	}
	return !panelTabsActive() || region == m.panelActive()
}

// anyPanelVisible reports whether the panel slot is occupied at all — the
// animation tracker uses it to tell a slot appearing/disappearing (slide in,
// shrink out) apart from a tab swap inside an occupied slot (instant).
func (m *model) anyPanelVisible() bool {
	for _, r := range panelRenderOrder {
		if m.panelVisible(r) {
			return true
		}
	}
	return false
}
