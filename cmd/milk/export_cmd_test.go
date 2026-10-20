package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/session"
)

// issue277State builds the minimal state execExport reads, with reasoning
// stored on the assistant turn (issue #277).
func issue277State() *interactiveState {
	s := &session.Session{
		ID:        "exp-1",
		CreatedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		State:     session.StateRouting,
	}
	s.History = []session.Turn{
		{Role: session.RoleUser, Content: "do the thing", Timestamp: time.Date(2026, 10, 20, 12, 0, 1, 0, time.UTC)},
		{Role: session.RoleAssistant, Agent: session.AgentLocal, Content: "done",
			Thinking: "let me think about it\nstep by step", Timestamp: time.Date(2026, 10, 20, 12, 0, 2, 0, time.UTC)},
	}
	return &interactiveState{sess: s}
}

// Reasoning is in the default export and out with either flag spelling —
// wherever it sits in the arguments.
func TestExecExport_ReasoningFlag(t *testing.T) {
	cases := []struct {
		name, sub string
		json      bool // how the format marks reasoning: fence vs field
		want      bool
	}{
		{"default text", "", false, true},
		{"default json", "json", true, true},
		{"text flag", "--no-reasoning", false, false},
		{"text bare flag", "no-reasoning", false, false},
		{"json flag", "json --no-reasoning", true, false},
		{"json flag first", "--no-reasoning json", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := execExport(c.sub, issue277State())
			if strings.Contains(out, "export error") || strings.Contains(out, "usage:") {
				t.Fatalf("unexpected error: %s", out)
			}
			marker := "<reasoning>"
			if c.json {
				marker = `"thinking"`
			}
			present := strings.Contains(out, marker) && strings.Contains(out, "step by step")
			if c.want && !present {
				t.Errorf("reasoning must be present by default (marker %q):\n%.400s", marker, out)
			}
			if !c.want {
				if strings.Contains(out, "<reasoning>") || strings.Contains(out, `"thinking"`) || strings.Contains(out, "step by step") {
					t.Errorf("reasoning must be stripped by the flag:\n%.400s", out)
				}
			}
			if !strings.Contains(out, "done") {
				t.Errorf("reply content missing:\n%.400s", out)
			}
		})
	}
}

// A path argument keeps its exact bytes (double spaces included) when no
// flag is present; with the flag the path still resolves after stripping.
func TestExecExport_PathPreservedAndFlagStripped(t *testing.T) {
	dir := t.TempDir()

	withSpace := filepath.Join(dir, "exp  out.txt")
	if out := execExport(withSpace, issue277State()); !strings.Contains(out, "exported to") {
		t.Fatalf("plain path export failed: %s", out)
	}
	b, err := os.ReadFile(withSpace)
	if err != nil {
		t.Fatalf("file must keep its exact (double-space) name: %v", err)
	}
	if !strings.Contains(string(b), "<reasoning>") {
		t.Error("file export must include reasoning by default")
	}
	if strings.Contains(string(b), "\033") {
		t.Error("file export must stay ANSI-free")
	}

	flagPath := filepath.Join(dir, "quiet.txt")
	if out := execExport("--no-reasoning "+flagPath, issue277State()); !strings.Contains(out, "exported to") {
		t.Fatalf("flag+path export failed: %s", out)
	}
	b2, err := os.ReadFile(flagPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "<reasoning>") || strings.Contains(string(b2), "step by step") {
		t.Error("flag must strip reasoning from file exports too")
	}
	if !strings.Contains(string(b2), "done") {
		t.Error("reply content must survive stripping")
	}
}
