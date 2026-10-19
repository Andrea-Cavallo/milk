package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scoutme/milk/internal/agent/local"
)

const backgroundPanelWidth = 33 // 32 inner + 1 right scrollbar column
const backgroundPanelInner = 32

// renderBackgroundPanel returns a vertical panel string of exactly h lines
// and backgroundPanelInner columns (scrollbar rendered separately by
// renderBackgroundPanelScrollbar), listing every spawn_background_agent job
// (ADR-0043) and every background shell (ADR-0063/0064) this session has —
// agent- and user-initiated alike. One merged panel for both kinds of
// background work (ADR-0064): same rows, same click-to-attach, same /bg
// surface. Body shared with every other side panel — see renderSidePanel.
func (m *model) renderBackgroundPanel(h int) string {
	return m.renderSidePanel(regionBackground, h)
}

// renderBackgroundPanelScrollbar returns a 1-column string of h lines: a dim
// │ track with a ▌ thumb when the panel content overflows, or a blank column
// otherwise. Shared with every other side panel — see renderSidePanelScrollbar.
func (m *model) renderBackgroundPanelScrollbar(h int) string {
	return m.renderSidePanelScrollbar(regionBackground, h)
}

// bgRowKind is a background-panel layout row's click target.
type bgRowKind int

const (
	bgRowPlain bgRowKind = iota // title, blank, section header, "(none)"
	bgRowJob
	bgRowShell
)

// bgPanelRow is one line of the background panel's layout: its rendered text
// plus what a click on it maps to. Renderer and click handler share this
// single layout (ADR-0047's recompute-on-click rule) so the line→target
// mapping can never drift from what is drawn.
type bgPanelRow struct {
	text     string
	kind     bgRowKind
	jobIdx   int // valid when kind == bgRowJob
	shellIdx int // valid when kind == bgRowShell
}

// backgroundPanelLayout builds the panel's rows — title, blank, an "agents"
// section when jobs exist, a "shells" section when shells exist (ADR-0064:
// one merged panel), and "(none)" when there is nothing to show. Job rows
// keep their Jobs() order, shell rows their BackgroundShells() order.
func backgroundPanelLayout(jobs []local.Job, shells []local.ShellInfo, inner int) []bgPanelRow {
	rows := []bgPanelRow{
		{text: panelTitleLine("background", regionBackground, inner)},
		{text: ""},
	}
	if len(jobs) == 0 && len(shells) == 0 {
		rows = append(rows, bgPanelRow{text: dim("(none)")})
		return rows
	}
	if len(jobs) > 0 {
		rows = append(rows, bgPanelRow{text: dim(fmt.Sprintf("agents (%d)", len(jobs)))})
		for i, j := range jobs {
			rows = append(rows, bgPanelRow{text: renderBackgroundJobLine(j, inner), kind: bgRowJob, jobIdx: i})
		}
	}
	if len(shells) > 0 {
		if len(jobs) > 0 {
			rows = append(rows, bgPanelRow{text: ""})
		}
		rows = append(rows, bgPanelRow{text: dim(fmt.Sprintf("shells (%d)", len(shells)))})
		for i, s := range shells {
			rows = append(rows, bgPanelRow{text: renderBackgroundShellLine(s, inner), kind: bgRowShell, shellIdx: i})
		}
	}
	return rows
}

func buildBackgroundPanelLines(jobs []local.Job, shells []local.ShellInfo, inner int) []string {
	rows := backgroundPanelLayout(jobs, shells, inner)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = r.text
	}
	return lines
}

func renderBackgroundJobLine(j local.Job, inner int) string {
	badge := backgroundJobStatusBadge(j.Status)
	return fitBackgroundRowLine(badge, j.Label, backgroundJobElapsed(j), inner)
}

func renderBackgroundShellLine(s local.ShellInfo, inner int) string {
	badge := backgroundShellStatusBadge(s)
	return fitBackgroundRowLine(badge, s.Command, elapsedShort(s.StartedAt, s.FinishedAt), inner)
}

// fitBackgroundRowLine assembles a badge + truncated label + dim elapsed
// suffix into exactly inner columns.
func fitBackgroundRowLine(badge, label, elapsed string, inner int) string {
	suffix := fmt.Sprintf(" %s", dim(elapsed))
	suffixW := utf8.RuneCountInString(elapsed) + 1
	prefix := fmt.Sprintf("  %s ", badge)
	prefixW := utf8.RuneCountInString(stripANSI(prefix))
	maxLabelW := inner - prefixW - suffixW
	if maxLabelW < 4 {
		maxLabelW = 4
	}
	labelRunes := []rune(label)
	if len(labelRunes) > maxLabelW {
		label = string(labelRunes[:maxLabelW-1]) + "…"
	}
	line := prefix + label
	lineW := utf8.RuneCountInString(stripANSI(line))
	// suffix (the elapsed badge) already starts with its own leading space,
	// so pad may legitimately be 0 when the label fills its entire budget —
	// only clamp the negative case (shouldn't happen given maxLabelW above,
	// but truncation math is fiddly enough to guard anyway).
	pad := inner - lineW - suffixW
	if pad < 0 {
		pad = 0
	}
	return line + strings.Repeat(" ", pad) + suffix
}

func backgroundJobStatusBadge(status local.JobStatus) string {
	switch status {
	case local.JobCompleted:
		return dim("✓")
	case local.JobFailed:
		return red("✗")
	default: // running
		return green("▶")
	}
}

// backgroundShellStatusBadge is backgroundJobStatusBadge's shell counterpart
// (ADR-0064): ▶ running, ■ stopping (killed but not yet exited), ✓ clean
// exit, ✗ anything else (non-zero exit, exec error, killed).
func backgroundShellStatusBadge(s local.ShellInfo) string {
	switch {
	case s.Running && s.Killed:
		return yellow("■")
	case s.Running:
		return green("▶")
	case s.Err != nil || s.ExitCode != 0 || s.Killed:
		return red("✗")
	default:
		return dim("✓")
	}
}

// backgroundJobElapsed returns a short "12s"/"3m" duration string: time
// since start for a running job, or total run time for a finished one.
func backgroundJobElapsed(j local.Job) string {
	return elapsedShort(j.StartedAt, j.EndedAt)
}

// elapsedShort formats end-start as a short "12s"/"3m"/"2h" badge; a zero
// end means "still running" and counts up to now.
func elapsedShort(start, end time.Time) string {
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
