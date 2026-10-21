package interp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/scoutme/milk/internal/workflow"
	"github.com/scoutme/milk/internal/workflow/board"
)

// boardProbeRunner records what each call saw on its ctx (board presence +
// acting identity) alongside the rendered prompt.
type boardProbeRunner struct {
	mu         sync.Mutex
	prompts    []string
	identities []string
	errs       []error
}

func (p *boardProbeRunner) Name() string { return "probe" }

func (p *boardProbeRunner) Run(ctx context.Context, prompt string, _ io.Writer) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prompts = append(p.prompts, prompt)
	p.identities = append(p.identities, board.IdentityFrom(ctx))
	idx := len(p.prompts) - 1
	if idx < len(p.errs) && p.errs[idx] != nil {
		return "", p.errs[idx]
	}
	return "ok", nil
}

func (p *boardProbeRunner) lastPrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.prompts) == 0 {
		return ""
	}
	return p.prompts[len(p.prompts)-1]
}

// boardDef: seed from a declared plan, then one agent_turn rendering the
// injected board.
func boardDef() workflow.Definition {
	return workflow.Definition{
		Name:  "t",
		Board: true,
		Vars: map[string]string{
			"plan": "## Item 1 (depends_on: 0)\nparser work\n## Item 2 (depends_on: 1)\ncli work",
		},
		Stages: []workflow.Stage{
			{ID: "seed", Kind: workflow.StageKindBoardSeed, Over: "Item", From: "plan"},
			{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "board now:\n{{.board}}", SaveAs: "out"},
		},
	}
}

// A board run seeds items from the plan, renders them into the turn prompt
// as {{.board}}, and writes the sidecar next to the checkpoint so a resume
// restores live state (a claim written to the sidecar between runs must be
// visible on the next run's prompt).
func TestBoard_SeedInjectsAndPersists(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "s.workflow.interp.json")

	p1 := &boardProbeRunner{}
	if err := New(boardDef(), "task").WithCheckpoint(cp).Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": p1})); err != nil {
		t.Fatalf("run1: %v", err)
	}
	prompt := p1.lastPrompt()
	for _, want := range []string{"Item 1 — parser", "Item 2 — cli", "(depends on 1)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("run1 prompt missing %q:\n%s", want, prompt)
		}
	}

	// Sidecar exists beside the checkpoint with the seeded items.
	raw, err := os.ReadFile(board.BoardPath(cp))
	if err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	var doc struct {
		Items []board.Item `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Items) != 2 {
		t.Fatalf("sidecar items = %+v (%v), want 2", doc.Items, err)
	}

	// Someone claims item 1 out-of-band (a teammate's tool call between
	// runs); the next run must load and render it — not re-seed over it.
	doc.Items[0].Status = board.StatusClaimed
	doc.Items[0].ClaimedBy = "worker·Item-1"
	mutated, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(board.BoardPath(cp), mutated, 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := &boardProbeRunner{}
	if err := New(boardDef(), "task").WithCheckpoint(cp).Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": p2})); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if got := p2.lastPrompt(); !strings.Contains(got, "(by worker·Item-1)") || !strings.Contains(got, "[c] Item 1") {
		t.Errorf("resume lost the claim:\n%s", got)
	}
}

// When the sidecar is deleted but the checkpoint still holds the
// board_seed leaf (a run that failed after seeding), loadBoard rebuilds the
// items from that value — the seed stage itself replays as done.
func TestBoard_DeletedSidecarRebuiltFromTrace(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "s.workflow.interp.json")

	// First runner call (the "work" turn) fails right after the seed leaf
	// was recorded: trace has the seed, the checkpoint is not Done.
	fail := &boardProbeRunner{errs: []error{errors.New("boom")}}
	def := boardDef()
	def.Stages = append(def.Stages, workflow.Stage{
		ID: "later", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "and then", SaveAs: "out2",
	})
	if err := New(def, "task").WithCheckpoint(cp).Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": fail})); err == nil {
		t.Fatal("expected the run to fail at the last stage")
	}
	if err := os.Remove(board.BoardPath(cp)); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	p := &boardProbeRunner{}
	if err := New(def, "task").WithCheckpoint(cp).Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": p})); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	p.mu.Lock()
	prompts := append([]string(nil), p.prompts...)
	p.mu.Unlock()
	var boardPrompt string
	for _, got := range prompts {
		if strings.Contains(got, "board now:") {
			boardPrompt = got
			break
		}
	}
	if !strings.Contains(boardPrompt, "Item 1 — parser") || !strings.Contains(boardPrompt, "Item 2 — cli") {
		t.Errorf("items must rebuild from the seed leaf after sidecar loss:\n%v", prompts)
	}
}

// Fan-out items each act under their own identity ("role·Item-N") while
// sharing the one board — claims and findings stay attributable.
func TestBoard_ParallelItemsCarryIdentity(t *testing.T) {
	def := workflow.Definition{
		Name:  "t",
		Board: true,
		Vars: map[string]string{
			"plan": "## Item 1\na\n## Item 2\nb\n## Item 3\nc",
		},
		Stages: []workflow.Stage{
			{ID: "seed", Kind: workflow.StageKindBoardSeed, Over: "Item", From: "plan"},
			{
				ID: "fanout", Kind: workflow.StageKindParallelGroup, Over: "Item", From: "plan",
				Body: []workflow.Stage{
					{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "{{.board}}"},
				},
			},
		},
	}
	p := &boardProbeRunner{}
	if err := New(def, "task").Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": p})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	p.mu.Lock()
	ids := append([]string(nil), p.identities...)
	p.mu.Unlock()
	sort.Strings(ids)
	want := []string{"w·Item-1", "w·Item-2", "w·Item-3"}
	if len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Errorf("identities = %v, want %v", ids, want)
	}
}

// A plain workflow (no board) still renders {{.board}} as empty — the
// missingkey=zero template contract — and carries no board on the ctx.
func TestBoard_NoBoardRunRendersEmpty(t *testing.T) {
	def := workflow.Definition{
		Name:   "t",
		Stages: []workflow.Stage{{ID: "w", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "board:{{.board}}"}},
	}
	p := &boardProbeRunner{}
	if err := New(def, "task").Run(context.Background(),
		runCfg(map[string]workflow.TurnRunner{"w": p})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := p.lastPrompt(); got != "board:" {
		t.Errorf("prompt = %q, want the empty-board rendering", got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.identities[0] != "" {
		t.Errorf("no board ⇒ no identity, got %q", p.identities[0])
	}
}

func TestCleanBoardLabel(t *testing.T) {
	tests := map[string]string{
		"**Engine core, input, collision, app shell.**": "Engine core, input, collision, app shell.",
		"- **UI**: HUD, menus":                          "UI: HUD, menus",
		"1. Build `src/core/engine.js`":                 "Build src/core/engine.js",
		"plain label":                                   "plain label",
		"  spaced  ":                                    "spaced",
		"snake_case __init__ stays":                     "snake_case __init__ stays",
	}
	for in, want := range tests {
		if got := cleanBoardLabel(in); got != want {
			t.Errorf("cleanBoardLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
