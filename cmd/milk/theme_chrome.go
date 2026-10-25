package main

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// panelTabs is the tab bar of a tabbed theme (ADR-0079), left to right:
// one tab per side panel — the strip is both the panel's title row and the
// only navigation between the tabs of the shared panel slot, so the render
// (panelTabStrip) and the click hit-test (panelTabAt) share this one table.
var panelTabs = []struct {
	region panelRegion
	name   string
}{
	{regionMemory, "memory"}, {regionTasks, "tasks"}, {regionBackground, "jobs"},
	{regionWorkflow, "flow"}, {regionProject, "files"}, {regionGit, "git"},
}

// panelTabStrip is the themed title row of the side panel: the panel tabs,
// the open one in the accent colour and underlined. On a tabbed theme the
// panels share one slot, so this strip is the single tab bar over it.
func panelTabStrip(region panelRegion, inner int) string {
	var b strings.Builder
	for i, t := range panelTabs {
		if i > 0 {
			b.WriteString(" ")
		}
		if t.region == region {
			b.WriteString(colorize(t.name, ansiBold+"\033[4m"+tkAccent.sgrFg()))
		} else {
			b.WriteString(colorize(t.name, tkMuted.sgrFg()))
		}
	}
	return ansi.Truncate(b.String(), inner, "")
}

// panelTabAt maps a column offset within the strip (0-based from the tab
// bar's left edge) to the tab whose label covers it — regionNone on a gap or
// past the strip. Walks the same single-space join panelTabStrip renders
// with, so the click target can never drift from what is drawn.
func panelTabAt(col int) panelRegion {
	x := 0
	for _, t := range panelTabs {
		w := len([]rune(t.name))
		if col >= x && col < x+w {
			return t.region
		}
		x += w + 1
	}
	return regionNone
}

// handleThemeCmd handles /theme [off|supermilk]: switches the theme live,
// pushes its colours into the terminal and persists the choice.
func (m model) handleThemeCmd(arg string) (tea.Model, tea.Cmd) {
	if arg == "" {
		m.appendTranscript(fmt.Sprintf("%s theme: %s  (use /theme off|supermilk)\n", milkTag(), bold(th.name)))
		return m, nil
	}
	if _, ok := themes[strings.ToLower(arg)]; !ok {
		m.appendTranscript(milkTag() + " usage: /theme [off|supermilk]\n")
		return m, nil
	}
	if th.modern {
		restoreTerminalColors()
	}
	setTheme(arg)
	applyThemeTerminal()
	m.st.cfg.Theme = th.name
	// Tabs are exclusive (ADR-0079): arriving from a sibling (theme "off")
	// layout, only the front panel survives — the rest would be hidden-open
	// siblings under a tab bar that cannot show them side by side.
	m.enforceTabbedPanelExclusivity()
	m.colorizeForce = true
	m.viewportDirty = true
	if err := saveLocalOrGlobal(m.st.cfg, activeConfigDir(m.st)); err != nil {
		m.appendTranscript(fmt.Sprintf("%s theme: %s (config save failed: %v)\n", milkTag(), bold(th.name), err))
	} else {
		m.notify("theme: "+th.name, "/theme")
	}
	m.syncLayout()
	// The skin paints the terminal background, so the detected light/dark
	// follows the theme: ask again once the colors changed (switching back to
	// "off" must not keep the cream skin's "light" verdict on a dark terminal).
	return m, tea.Batch(tea.ClearScreen, tea.RequestBackgroundColor)
}

// toolRowRe matches a tool-activity row as the agents emit it: "» name: summary"
// inside whatever dim/colour codes wrap it. "⚙" still matches rows seeded from
// sessions written before ADR-0075.
var toolRowRe = regexp.MustCompile(`^(?:\x1b\[[0-9;]*m)*[⚙»] ([^:\x1b]+?)(?:: (.*?))?(?:\x1b\[[0-9;]*m)*$`)

// modernToolRows restyles tool-activity rows for a skin: the marker becomes a
// small accent glyph, the tool name is bold muted ink, the summary muted.
// Render-only; the transcript buffer is untouched.
func modernToolRows(content string) string {
	if !strings.Contains(content, "⚙ ") && !strings.Contains(content, "» ") {
		return content
	}
	rows := strings.Split(content, "\n")
	for i, row := range rows {
		m := toolRowRe.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		out := colorize("▸", tkAccent.sgrFg()) + " " + colorize(m[1], ansiBold+tkMuted.sgrFg())
		if m[2] != "" {
			out += "  " + colorize(m[2], tkMuted.sgrFg())
		}
		rows[i] = out
	}
	return strings.Join(rows, "\n")
}
