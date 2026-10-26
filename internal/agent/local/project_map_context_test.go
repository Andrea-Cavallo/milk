package local

// Pushed project-structure context (project_map_context, ADR-0081): the
// block the Run loop appends to the system prompt around project_map.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/contextreduce"
)

// projectMapContextFixture is projectMapFixture plus a manifest, so the
// framing header carries the project identity.
func projectMapContextFixture(t *testing.T) string {
	t.Helper()
	root := projectMapFixture(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestProjectMapContextBlock_Minimal(t *testing.T) {
	root := projectMapContextFixture(t)
	blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapMinimal, true)

	for _, want := range []string{
		"Project orientation", "project: example.com/thing (go)", "top level:",
		"pkg1/ (2 files)", "project_map",
	} {
		if !strings.Contains(blk, want) {
			t.Errorf("minimal block missing %q\n---\n%s", want, blk)
		}
	}
	// minimal pushes shape, never symbols
	if strings.Contains(blk, "func ") || strings.Contains(blk, "type ") {
		t.Errorf("minimal block leaked symbols:\n%s", blk)
	}
}

func TestProjectMapContextBlock_MinimalToolUnregisteredHasNoPointer(t *testing.T) {
	root := projectMapContextFixture(t)
	blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapMinimal, false)
	if strings.Contains(blk, "project_map") {
		t.Errorf("block advertises project_map to an agent without the tool:\n%s", blk)
	}
}

func TestProjectMapContextBlock_DefaultModeIsMinimal(t *testing.T) {
	root := projectMapContextFixture(t)
	def := projectMapContextBlock(context.Background(), root, root, "", true)
	min := projectMapContextBlock(context.Background(), root, root, config.ProjectMapMinimal, true)
	if def != min {
		t.Errorf("default mode must equal minimal:\ndefault:\n%s\nminimal:\n%s", def, min)
	}
}

func TestProjectMapContextBlock_Full(t *testing.T) {
	root := projectMapContextFixture(t)
	blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapFull, true)

	for _, want := range []string{
		"Project map (", "func Load(path string) (*Config, error)", "type Config struct",
	} {
		if !strings.Contains(blk, want) {
			t.Errorf("full block missing %q\n---\n%s", want, blk)
		}
	}
	if strings.Contains(blk, "BODY_SENTINEL") {
		t.Errorf("full block leaked a body:\n%s", blk)
	}
	if len(blk) > projectMapDefaultMaxChars+200 { // budget + short header
		t.Errorf("full block over budget: %d chars", len(blk))
	}
}

func TestProjectMapContextBlock_OffAndEmpty(t *testing.T) {
	root := projectMapContextFixture(t)
	if blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapOff, true); blk != "" {
		t.Errorf("off must push nothing, got:\n%s", blk)
	}
	empty := t.TempDir()
	for _, mode := range []config.ProjectMapMode{"", config.ProjectMapMinimal, config.ProjectMapFull} {
		if blk := projectMapContextBlock(context.Background(), empty, empty, mode, true); blk != "" {
			t.Errorf("empty dir with mode %q must push nothing, got:\n%s", mode, blk)
		}
	}
}

func TestProjectMapContextBlock_MinimalTreeTruncates(t *testing.T) {
	projectMapTestIsolate(t)
	root := t.TempDir()
	for i := 0; i < 60; i++ {
		name := filepath.Join(root, "f"+string(rune('a'+i/26))+"x"+string(rune('a'+i%26))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapMinimal, true)
	if !strings.Contains(blk, "…+36 more") {
		t.Errorf("top-level tree not truncated at 24 entries:\n%s", blk)
	}
	if len(blk) > projectMapMinimalBlockMaxChars+100 {
		t.Errorf("minimal block over budget: %d chars", len(blk))
	}
}

func TestProjectMapContextBlock_DropsWithGuidanceUnderReduction(t *testing.T) {
	root := projectMapContextFixture(t)
	blk := projectMapContextBlock(context.Background(), root, root, config.ProjectMapMinimal, true)
	b := &sysPromptBuilder{cwd: root, guidance: []string{blk}}
	p := contextreduce.Policy{}

	if !strings.Contains(b.render(contextreduce.LevelNone, p), blk) {
		t.Error("block missing at LevelNone")
	}
	if strings.Contains(b.render(contextreduce.LevelInstructions, p), blk) {
		t.Error("block must be shed with the guidance layers at LevelInstructions")
	}
}

func TestCloneForBackground_CopiesProjectMapContext(t *testing.T) {
	a := &Agent{projectMapContext: config.ProjectMapFull}
	cp := a.cloneForBackground()
	if cp.projectMapContext != config.ProjectMapFull {
		t.Errorf("clone lost projectMapContext: %q", cp.projectMapContext)
	}
}

// Ensure the mode wire format round-trips through an AgentConfig decode
// (the field-level bool aliases live in internal/config's own tests).
func TestProjectMapContext_AgentConfigDecode(t *testing.T) {
	var ac config.AgentConfig
	if err := json.Unmarshal([]byte(`{"name":"x","project_map_context":"full"}`), &ac); err != nil {
		t.Fatal(err)
	}
	if ac.ProjectMapContext != config.ProjectMapFull {
		t.Errorf("decoded %q", ac.ProjectMapContext)
	}
}
