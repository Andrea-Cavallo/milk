package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/tasks"
)

const tasksPanelWidth = 33 // 32 inner + 1 right scrollbar column
const tasksPanelInner = 32

// renderTasksPanel returns a vertical panel string of exactly h lines and
// tasksPanelInner columns (scrollbar is rendered separately by
// renderTasksPanelScrollbar). Body shared with every other side panel — see
// renderSidePanel.
func (m *model) renderTasksPanel(h int) string {
	return m.renderSidePanel(regionTasks, h)
}

// renderTasksPanelScrollbar returns a 1-column string of h lines: a dim │
// track with a ▌ thumb when the panel content overflows, or a blank column
// otherwise. Shared with every other side panel — see renderSidePanelScrollbar.
func (m *model) renderTasksPanelScrollbar(h int) string {
	return m.renderSidePanelScrollbar(regionTasks, h)
}

// taskPanelRows returns the rendered panel lines *and* a parallel ids slice
// (one ID per line: the task ID, or "" for non-task rows), mirroring
// memoryPanelRows. Rendering and hit testing share rows so expanding a title
// cannot shift click targets.
func taskPanelRows(ts *tasks.Store, inner int, expanded string) ([]string, []string) {
	var lines []string
	var ids []string

	addLine := func(s string) {
		lines = append(lines, s)
		ids = append(ids, "")
	}
	addTask := func(t tasks.Task) {
		start := len(lines)
		if expanded == t.ID {
			addExpandedTaskLines(&lines, t, inner)
		} else {
			lines = append(lines, renderTaskLine(t, inner))
		}
		for range lines[start:] {
			ids = append(ids, t.ID)
		}
	}

	// Title
	addLine(panelTitleLine("tasks", regionTasks, inner))
	addLine(dim("F2 close · double-click expand"))

	if ts == nil {
		addLine(dim("(unavailable)"))
		return lines, ids
	}

	sessionTasks, _ := ts.List(tasks.ListOpts{})
	globalTasks, _ := ts.List(tasks.ListOpts{IncludeGlobal: true})

	// Separate global-only tasks (those not in session).
	sessionIDs := map[string]bool{}
	for _, t := range sessionTasks {
		sessionIDs[t.ID] = true
	}
	var globalOnly []tasks.Task
	for _, t := range globalTasks {
		if !sessionIDs[t.ID] {
			globalOnly = append(globalOnly, t)
		}
	}

	addLine(stylePanelSection.Render("SESSION"))
	if len(sessionTasks) == 0 {
		addLine(dim("  (none)"))
	}
	for _, t := range sessionTasks {
		addTask(t)
	}

	addLine("")
	addLine(stylePanelSection.Render("GLOBAL"))
	if len(globalOnly) == 0 {
		addLine(dim("  (none)"))
	}
	for _, t := range globalOnly {
		addTask(t)
	}

	return lines, ids
}

// addExpandedTaskLines renders a task's full title wrapped across as many
// lines as it needs — the in-place expansion counterpart of the collapsed
// one-line ellipsis (memory-panel semantics: a different line budget on the
// same wrap, no separate detail view).
func addExpandedTaskLines(lines *[]string, t tasks.Task, inner int) {
	prefix := fmt.Sprintf("  %s ", taskStatusBadge(t.Status))
	prefixW := utf8.RuneCountInString(ansi.Strip(prefix))
	firstW, contW := taskWrapWidths(t, inner)
	for i, w := range wordWrap(t.Title, firstW, contW, utf8.RuneCountInString(t.Title)+1) {
		if i == 0 {
			*lines = append(*lines, prefix+w)
		} else {
			*lines = append(*lines, strings.Repeat(" ", prefixW)+w)
		}
	}
}

// taskWrapWidths returns the wrap widths (display cells) for a task title: the
// first line shares the column with the status badge, continuation lines are
// indented to the title column. Shared by the collapsed and expanded render
// paths; rows and IDs come from the same taskPanelRows call, so render and hit
// test can never wrap at different points and hand out misaligned IDs
// (issue #212's lesson).
func taskWrapWidths(t tasks.Task, inner int) (firstW, contW int) {
	prefixW := utf8.RuneCountInString(ansi.Strip(fmt.Sprintf("  %s ", taskStatusBadge(t.Status))))
	w := inner - prefixW
	if w < 4 {
		w = 4
	}
	return w, w
}

func renderTaskLine(t tasks.Task, inner int) string {
	prefix := fmt.Sprintf("  %s ", taskStatusBadge(t.Status))
	title := t.Title
	maxTitleW, _ := taskWrapWidths(t, inner)
	titleRunes := []rune(title)
	if len(titleRunes) > maxTitleW {
		title = string(titleRunes[:maxTitleW-1]) + "…"
	}
	return prefix + title
}

func taskStatusBadge(status string) string {
	switch status {
	case tasks.StatusDone:
		return dim("✓")
	case tasks.StatusInProgress:
		return green("▶")
	case tasks.StatusBlocked:
		return red("✗")
	default: // pending
		return dim("○")
	}
}
