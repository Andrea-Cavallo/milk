package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/agent/local"
)

func TestBuildBackgroundPanelLines_Empty(t *testing.T) {
	lines := buildBackgroundPanelLines(nil, nil, backgroundPanelInner)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(none)") {
		t.Errorf("expected a (none) placeholder for an empty job list, got %q", joined)
	}
}

func TestBuildBackgroundPanelLines_ShowsEachJobWithinWidthBudget(t *testing.T) {
	jobs := []local.Job{
		{ID: "job_1", Label: "terrain & mesher deep dive", Status: local.JobRunning, StartedAt: time.Now().Add(-30 * time.Second)},
		{ID: "job_2", Label: "combat & AI deep dive", Status: local.JobCompleted, StartedAt: time.Now().Add(-90 * time.Second), EndedAt: time.Now().Add(-10 * time.Second)},
		{ID: "job_3", Label: "a needlessly long label that will not fit in the panel width at all", Status: local.JobFailed, StartedAt: time.Now().Add(-5 * time.Second), EndedAt: time.Now()},
	}
	lines := buildBackgroundPanelLines(jobs, nil, backgroundPanelInner)
	joined := strings.Join(lines, "\n")

	for _, want := range []string{"terrain", "combat", "a needlessly long"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected panel content to mention %q, got:\n%s", want, joined)
		}
	}
	for _, line := range lines {
		if w := runeWidthNoANSI(line); w > backgroundPanelInner {
			t.Errorf("line exceeds panel width %d (got %d): %q", backgroundPanelInner, w, line)
		}
	}
}

func runeWidthNoANSI(s string) int {
	return len([]rune(ansi.Strip(s)))
}

// A shell that was killed on request has ended the way dev servers and
// headless browsers normally do: it gets a neutral badge, not the red ✗
// reserved for shells that failed on their own.
func TestBackgroundShellStatusBadge(t *testing.T) {
	old := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = old })
	tests := []struct {
		name string
		info local.ShellInfo
		want string
	}{
		{"running", local.ShellInfo{Running: true}, green("▶")},
		{"stopping", local.ShellInfo{Running: true, Killed: true}, yellow("■")},
		{"killed and exited", local.ShellInfo{Finished: true, Killed: true, ExitCode: -1}, dim("■")},
		{"clean exit", local.ShellInfo{Finished: true}, dim("✓")},
		{"failed on its own", local.ShellInfo{Finished: true, ExitCode: 1}, red("✗")},
		{"exec error", local.ShellInfo{Finished: true, Err: errors.New("boom")}, red("✗")},
	}
	for _, tt := range tests {
		if got := backgroundShellStatusBadge(tt.info); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
