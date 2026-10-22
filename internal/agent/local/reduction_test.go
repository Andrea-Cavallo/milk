package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/contextreduce"
)

func TestSysPromptBuilderRenderLevelNoneMatchesLegacyConcat(t *testing.T) {
	b := &sysPromptBuilder{
		cwd:      "",
		selfName: "myagent",
		tier:     "standard",
		custom:   "CUSTOM PROMPT",
		guidance: []string{"GUIDANCE-ONE", "GUIDANCE-TWO"},
	}
	got := b.render(contextreduce.LevelNone, contextreduce.DefaultPolicy())
	want := "CUSTOM PROMPT" + "\n\n" +
		buildSystemPrompt("", "myagent", "", false, "standard", false) +
		"\n\n" + "GUIDANCE-ONE" +
		"\n\n" + "GUIDANCE-TWO"
	if got != want {
		t.Fatalf("LevelNone render diverged from legacy concatenation\n got: %q\nwant: %q", got, want)
	}
}

func TestSysPromptBuilderRenderLevels(t *testing.T) {
	b := &sysPromptBuilder{
		cwd:      "",
		selfName: "myagent",
		tier:     "full",
		custom:   "CUSTOM PROMPT",
		guidance: []string{"GUIDANCE-ONE", "GUIDANCE-TWO"},
	}
	p := contextreduce.DefaultPolicy()

	full := b.render(contextreduce.LevelNone, p)
	l1 := b.render(contextreduce.LevelInstructions, p)
	l2 := b.render(contextreduce.LevelToolsCompact, p)
	l3 := b.render(contextreduce.LevelToolsPrune, p)

	for _, s := range []string{"Additional guidance", "GUIDANCE-ONE", "GUIDANCE-TWO"} {
		if !strings.Contains(full, s) {
			t.Errorf("LevelNone prompt missing %q", s)
		}
	}
	// L1: one tier step down (full -> standard) and the optional guidance
	// blocks dropped; the shared rules block stays.
	if strings.Contains(l1, "Additional guidance") {
		t.Error("L1 prompt still contains full-tier extra guidance")
	}
	if strings.Contains(l1, "GUIDANCE-ONE") || strings.Contains(l1, "GUIDANCE-TWO") {
		t.Error("L1 prompt still contains per-turn guidance blocks")
	}
	if !strings.Contains(l1, "Rules:") {
		t.Error("L1 prompt lost the shared rules block after a full->standard step-down")
	}
	// L2: forced minimal tier — the shared rules block is gone too.
	if strings.Contains(l2, "Rules:") {
		t.Error("L2 prompt still contains the shared rules block")
	}
	if strings.Contains(l2, "GUIDANCE-ONE") {
		t.Error("L2 prompt still contains per-turn guidance blocks")
	}
	// Custom prompts are user-authored signal: never dropped.
	for name, s := range map[string]string{"L1": l1, "L2": l2, "L3": l3} {
		if !strings.Contains(s, "CUSTOM PROMPT") {
			t.Errorf("%s prompt dropped the custom prompt", name)
		}
	}
	if l2 != l3 {
		t.Error("L3 instruction side should equal L2 (L3 only adds tool pruning)")
	}
}

func TestSysPromptBuilderRenderStepsDownStandard(t *testing.T) {
	b := &sysPromptBuilder{selfName: "myagent", tier: "standard"}
	p := contextreduce.DefaultPolicy()
	l1 := b.render(contextreduce.LevelInstructions, p)
	if strings.Contains(l1, "Rules:") {
		t.Error("standard agent at L1 should step down to minimal (no rules block)")
	}
	if !strings.Contains(l1, "You are myagent") {
		t.Error("L1 prompt lost the role framing")
	}
}

func TestSysPromptBuilderRenderTrimsProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("AGENTS RULE LINE. ", 200)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &sysPromptBuilder{cwd: dir, selfName: "myagent", tier: "minimal"}
	p := contextreduce.DefaultPolicy()
	p.InstructionsTrimChars = 200

	full := b.render(contextreduce.LevelNone, p)
	if strings.Contains(full, "(truncated)") {
		t.Fatal("LevelNone should not trim the project-instructions block")
	}
	trimmed := b.render(contextreduce.LevelToolsCompact, p)
	if !strings.Contains(trimmed, "(truncated)") {
		t.Error("L2 prompt should head+tail-trim the project-instructions block")
	}
	if len(trimmed) >= len(full) {
		t.Errorf("L2 prompt not smaller than full: %d >= %d", len(trimmed), len(full))
	}
}

func TestReduceTools(t *testing.T) {
	long := strings.Repeat("verbose parameter prose. ", 20)
	mk := func(name, desc string) map[string]any {
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": desc,
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"x": map[string]any{"type": "string", "description": long}},
				},
			},
		}
	}
	tools := []map[string]any{mk("bash", long), mk("read_file", long), mk("grep", long)}
	a := &Agent{usedTools: newToolUseTracker()}

	if got := a.reduceTools(tools, contextreduce.LevelNone); len(got) != 3 {
		t.Fatalf("LevelNone should keep all tools, got %d", len(got))
	}
	compacted := a.reduceTools(tools, contextreduce.LevelToolsCompact)
	if len(compacted) != 3 {
		t.Fatalf("L2 should keep all tools, got %d", len(compacted))
	}
	if d := compacted[0]["function"].(map[string]any)["description"].(string); len(d) > 120 {
		t.Errorf("L2 description not one-lined: %q", d)
	}
	// Input must not be mutated.
	if d := tools[0]["function"].(map[string]any)["description"].(string); d != long {
		t.Error("reduceTools mutated the input tool description")
	}

	a.noteToolUsed("read_file")
	pruned := a.reduceTools(tools, contextreduce.LevelToolsPrune)
	names := map[string]bool{}
	for _, s := range pruned {
		names[toolName(s)] = true
	}
	if !names["read_file"] {
		t.Error("prune dropped a tool observed in use")
	}
	if !names["bash"] {
		t.Error("prune dropped a protected tool")
	}
	if names["grep"] {
		t.Error("prune kept a never-used unprotected tool")
	}
}

func TestToolUseTracker(t *testing.T) {
	var nilTracker *toolUseTracker
	nilTracker.note("bash") // must not panic
	if got := nilTracker.snapshot(); got != nil {
		t.Errorf("nil tracker snapshot = %v, want nil", got)
	}

	tr := newToolUseTracker()
	tr.note("bash")
	tr.note("")
	tr.note("read_file")
	got := tr.snapshot()
	if !got["bash"] || !got["read_file"] || len(got) != 2 {
		t.Errorf("snapshot = %v, want {bash read_file}", got)
	}
	got["bash"] = false
	if !tr.snapshot()["bash"] {
		t.Error("snapshot aliases the internal map")
	}
}

func TestReductionLevel(t *testing.T) {
	p := contextreduce.DefaultPolicy()
	off := p
	off.Enabled = false
	mk := func(pol contextreduce.Policy, denom int, promptTokens int64) *Agent {
		return &Agent{reductionPolicy: pol, pressureDenominator: denom, lastPromptTokens: promptTokens}
	}
	msgs := []Message{{Role: "system", Content: strings.Repeat("x", 4000)}}

	if got := mk(off, 10000, 9000).reductionLevel(msgs, nil); got != contextreduce.LevelNone {
		t.Errorf("disabled policy level = %d, want none", got)
	}
	if got := mk(p, 0, 9000).reductionLevel(msgs, nil); got != contextreduce.LevelNone {
		t.Errorf("zero denominator level = %d, want none", got)
	}
	// Provider-reported path: 9000/10000 = 90% >= 75 -> prune.
	if got := mk(p, 10000, 9000).reductionLevel(msgs, nil); got != contextreduce.LevelToolsPrune {
		t.Errorf("90%% pressure level = %d, want tools_prune", got)
	}
	// Estimate path (no provider data yet): 4000 chars / 4 = 1000 tokens
	// over a 4000-token denominator = 25% -> none.
	if got := mk(p, 4000, 0).reductionLevel(msgs, nil); got != contextreduce.LevelNone {
		t.Errorf("estimated low pressure level = %d, want none", got)
	}
	// Estimate path under pressure: 1000 tokens over a 1200-token
	// denominator = 83% -> prune.
	if got := mk(p, 1200, 0).reductionLevel(msgs, nil); got != contextreduce.LevelToolsPrune {
		t.Errorf("estimated high pressure level = %d, want tools_prune", got)
	}
}

// A single image must not read as hundreds of thousands of tokens: with no
// provider-reported size yet, its base64 length used to count as text and
// pushed the first request of every image turn into tool pruning.
func TestReductionLevel_ImageIsNotCountedAsBase64Text(t *testing.T) {
	a := &Agent{reductionPolicy: contextreduce.DefaultPolicy(), pressureDenominator: 100_000}
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("x", 4000)},
		{Role: "user", ContentParts: []ContentPart{
			{Type: "text", Text: "what is this"},
			{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64," + strings.Repeat("A", 900_000)}},
		}},
	}
	if got := a.reductionLevel(msgs, nil); got != contextreduce.LevelNone {
		t.Errorf("one image drove reduction level to %d, want none", got)
	}
	if got := messagesEstimateBytes(msgs); got > 30_000 {
		t.Errorf("image counted as %d estimate bytes", got)
	}
}
