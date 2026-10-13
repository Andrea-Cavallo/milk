package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/memory"
)

// memStateWith opens a store whose global.json holds one non-core (promoted)
// percept — RecordGlobal makes core ones, which never go stale.
func memStateWith(t *testing.T, id, content string) *interactiveState {
	t.Helper()
	dir := t.TempDir()
	seed := `{"percepts":[{"id":"` + id + `","content":"` + content + `","producer":"primary","w":0.5,"roles":{},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"engrams":[],"edges":[]}`
	if err := os.WriteFile(filepath.Join(dir, "global.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.NewStoreParams(dir, "sess", memory.Params{GlobalArchiveAfterDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	return &interactiveState{mem: mem}
}

func memStateEmpty(t *testing.T) *interactiveState {
	t.Helper()
	mem, err := memory.NewStoreParams(t.TempDir(), "sess", memory.Params{GlobalArchiveAfterDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	return &interactiveState{mem: mem}
}

func TestExecMemory_StaleArchiveRestoreOnAnEmptyStore(t *testing.T) {
	st := memStateEmpty(t)
	if out := execMemory("stale", st); !strings.Contains(out, "no non-core global percepts") {
		t.Errorf("stale on empty store: %q", out)
	}
	if out := execMemory("archive", st); !strings.Contains(out, "archive is empty") {
		t.Errorf("archive on empty store: %q", out)
	}
	if out := execMemory("restore", st); !strings.Contains(out, "usage: /memory restore") {
		t.Errorf("restore without id: %q", out)
	}
	if out := execMemory("restore abc123", st); !strings.Contains(out, "cannot restore") {
		t.Errorf("restore of an unknown id: %q", out)
	}
}

func TestExecMemoryStale_ListsGlobalPerceptsWithTimeLeft(t *testing.T) {
	st := memStateWith(t, "11111111-aaaa-bbbb-cccc-000000000002", "the staging cluster lives in eu central")
	got := execMemory("stale", st)
	if !strings.Contains(got, "#11111111") || !strings.Contains(got, "days left") || !strings.Contains(got, "staging cluster") {
		t.Errorf("stale listing: %q", got)
	}
}

// Percepts injected into a prompt are in use: injection restarts their idle
// clock, so a fact that keeps being relevant is never archived as stale.
func TestPerceptsForAgent_InjectedPerceptsCountAsUsed(t *testing.T) {
	st := memStateWith(t, "22222222-aaaa-bbbb-cccc-000000000003", "the build uses task and go test")
	if got := perceptsForAgent(config.Config{}, st.mem, "how do I run the build", false); len(got) == 0 {
		t.Fatal("expected the percept to be injected")
	}
	entries := st.mem.Stale()
	if len(entries) != 1 || entries[0].Percept.LastUsedAt.IsZero() {
		t.Errorf("an injected percept was not marked as used: %+v", entries)
	}
}
