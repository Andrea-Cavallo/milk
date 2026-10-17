package local

import (
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/contextreduce"
)

// TestBuildSystemPrompt_ContainsDateLine: every tier and role must carry the
// injected current-date line (ADR-0060) — including "minimal", which is the
// tier the context-reduction ladder bottoms out at, and the cwd == "" path
// used by single-prompt runs without a working directory.
func TestBuildSystemPrompt_ContainsDateLine(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	want := "Current date: " + today

	tiers := []string{"", "minimal", "standard", "full"}
	for _, tier := range tiers {
		for _, tc := range []struct {
			label          string
			cwd            string
			workflow       bool
			escalationName string
		}{
			{"primary-cwd", "/tmp/repo", false, ""},
			{"primary-no-cwd", "", false, ""},
			{"workflow", "/tmp/repo", true, ""},
			{"escalation", "/tmp/repo", false, "mimo-pro"},
		} {
			t.Run(tc.label+"/"+tier, func(t *testing.T) {
				got := buildSystemPrompt(tc.cwd, "mimo-local", tc.escalationName, tc.workflow, tier, false)
				if !strings.Contains(got, want) {
					t.Errorf("prompt (tier=%q, %s) missing %q", tier, tc.label, want)
				}
			})
		}
	}
}

// TestBuildSystemPromptOpts_DateLineSurvivesReduction: the instruction-layer
// reduction (tier step-down + AGENTS.md trimming) must not shed the date —
// it belongs to the prompt core, not the sheddable guidance layer.
func TestBuildSystemPromptOpts_DateLineSurvivesReduction(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	want := "Current date: " + today

	// LevelToolsCompact forces tier=minimal and trims the project-instructions
	// block; render must still carry the date line.
	spb := &sysPromptBuilder{
		cwd:      t.TempDir(),
		selfName: "mimo-local",
		tier:     "standard",
	}
	got := spb.render(contextreduce.LevelToolsCompact, contextreduce.Policy{InstructionsTrimChars: 64})
	if !strings.Contains(got, want) {
		t.Errorf("reduced prompt (LevelToolsCompact) missing %q", want)
	}
}

// TestBuildSystemPrompt_DateLineIsDateGranularity mirrors DateLine's contract
// at the prompt layer: the rendered prompt may not embed a time-of-day
// reading (cache-stable — no HH:MM between the date and the zone clause).
func TestBuildSystemPrompt_DateLineIsDateGranularity(t *testing.T) {
	prompt := buildSystemPrompt("", "mimo-local", "", false, "standard", false)
	idx := strings.Index(prompt, "Current date: ")
	if idx < 0 {
		t.Fatal("prompt missing current date line")
	}
	line := prompt[idx:]
	if end := strings.Index(line, "\n"); end >= 0 {
		line = line[:end]
	}
	i := strings.Index(line, ", local time zone")
	if i < 0 {
		t.Fatalf("date line missing zone clause: %q", line)
	}
	// The segment "2026-10-17 (Saturday)" must be time-free; colons only
	// appear later, inside the UTC offset.
	if strings.Contains(line[len("Current date: "):i], ":") {
		t.Errorf("date line contains a time-of-day reading: %q", line)
	}
}
