package interp

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/workflow"
	"github.com/scoutme/milk/internal/workflow/board"
)

// funcRunner answers each call with fn's result.
type funcRunner struct {
	name string
	fn   func(ctx context.Context, prompt string) (string, error)
}

func (f *funcRunner) Name() string { return f.name }
func (f *funcRunner) Run(ctx context.Context, prompt string, _ io.Writer) (string, error) {
	return f.fn(ctx, prompt)
}

// itemOf extracts the item number from a board identity such as "w·Item-3".
func itemOf(t *testing.T, ctx context.Context) int {
	t.Helper()
	id := board.IdentityFrom(ctx)
	n, err := strconv.Atoi(id[strings.LastIndex(id, "-")+1:])
	if err != nil {
		t.Errorf("no item in identity %q", id)
	}
	return n
}

// An item starts the moment its own dependencies finish — it does not wait
// for unrelated items that happen to share an earlier dependency "wave".
func TestExecParallelGroup_DependentStartsWhenItsOwnDependenciesFinish(t *testing.T) {
	releaseSlow := make(chan struct{})
	started3 := make(chan struct{})
	var once sync.Once
	worker := &funcRunner{name: "w", fn: func(_ context.Context, prompt string) (string, error) {
		switch strings.TrimSpace(prompt) {
		case "item 2":
			<-releaseSlow // the slow sibling of item 1
		case "item 3":
			once.Do(func() { close(started3) })
		}
		return "ok", nil
	}}
	def := workflow.Definition{
		Name: "t",
		Vars: map[string]string{"plan": "## Item 1\na\n\n## Item 2\nb\n\n## Item 3 (depends_on: 1)\nc\n"},
		Stages: []workflow.Stage{{
			ID: "fan", Kind: workflow.StageKindParallelGroup, Over: "Item", From: "plan", MaxConcurrency: 3,
			Body: []workflow.Stage{{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "item {{.item}}"}},
		}},
	}
	done := make(chan error, 1)
	go func() {
		done <- New(def, "task").Run(context.Background(), runCfg(map[string]workflow.TurnRunner{"w": worker}))
	}()
	select {
	case <-started3:
	case <-time.After(5 * time.Second):
		t.Fatal("item 3 (depends only on item 1) never started while the unrelated item 2 was still running")
	}
	close(releaseSlow)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// An item whose dependency failed still runs (one item's error never blocks
// its dependents), and the failure lands in the aggregate.
func TestExecParallelGroup_FailedDependencyStillReleasesDependent(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	worker := &funcRunner{name: "w", fn: func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		ran = append(ran, strings.TrimSpace(prompt))
		mu.Unlock()
		if strings.TrimSpace(prompt) == "item 1" {
			return "", fmt.Errorf("boom")
		}
		return "ok", nil
	}}
	def := workflow.Definition{
		Name: "t",
		Vars: map[string]string{"plan": "## Item 1\na\n\n## Item 2 (depends_on: 1)\nb\n"},
		Stages: []workflow.Stage{{
			ID: "fan", Kind: workflow.StageKindParallelGroup, Over: "Item", From: "plan", MaxConcurrency: 2, SaveAs: "results",
			Body: []workflow.Stage{{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "item {{.item}}"}},
		}},
	}
	if err := New(def, "task").Run(context.Background(), runCfg(map[string]workflow.TurnRunner{"w": worker})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ran) != 2 || ran[0] != "item 1" || ran[1] != "item 2" {
		t.Errorf("ran %v, want item 1 then item 2", ran)
	}
}

// A repeated fan-out (outer retry loop) with skip_if_board_status re-runs only
// the items the checker reopened; the aggregate keeps every other item's last
// result, so a later stage still sees all of them.
func TestExecParallelGroup_SkipIfBoardStatusRerunsOnlyReopenedItems(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	worker := &funcRunner{name: "w", fn: func(ctx context.Context, _ string) (string, error) {
		idx := itemOf(t, ctx)
		b := board.FromContext(ctx)
		if _, ok := b.Claim(idx, board.IdentityFrom(ctx)); !ok {
			return "SKIPPED", nil
		}
		b.Update(idx, board.StatusDone, "")
		mu.Lock()
		calls = append(calls, fmt.Sprintf("item%d", idx))
		n := len(calls)
		mu.Unlock()
		return fmt.Sprintf("out-item%d-call%d", idx, n), nil
	}}
	checks := 0
	checker := &funcRunner{name: "c", fn: func(ctx context.Context, _ string) (string, error) {
		checks++
		if checks == 1 {
			board.FromContext(ctx).Update(2, board.StatusTodo, "") // reality differs: reopen item 2
			return "more_work", nil
		}
		return "all_done", nil
	}}
	final := &boardProbeRunner{}
	def := workflow.Definition{
		Name:  "t",
		Board: true,
		Vars:  map[string]string{"plan": "## Item 1\na\n\n## Item 2\nb\n\n## Item 3\nc\n"},
		Stages: []workflow.Stage{
			{ID: "seed", Kind: workflow.StageKindBoardSeed, Over: "Item", From: "plan"},
			{ID: "passes", Kind: workflow.StageKindLoop, MaxIterations: 3, IterationVar: "pass", Body: []workflow.Stage{
				{ID: "fan", Kind: workflow.StageKindParallelGroup, Over: "Item", From: "plan", MaxConcurrency: 3,
					SaveAs: "results", SkipIfBoardStatus: []string{"done"},
					Body: []workflow.Stage{{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "go", SaveAs: "out"}}},
				{ID: "standup", Kind: workflow.StageKindAgentTurn, Role: "c", Prompt: "check",
					Verdict: map[string]workflow.VerdictRule{"all_done": {Action: "break"}, "more_work": {Action: "retry"}}},
			}},
			{ID: "final", Kind: workflow.StageKindAgentTurn, Role: "f", Prompt: "{{range .results}}[{{.Index}}:{{.Status}}:{{.Output}}]{{end}}"},
		},
	}
	cfg := runCfg(map[string]workflow.TurnRunner{"w": worker, "c": checker, "f": final})
	if err := New(def, "task").Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	perItem := map[string]int{}
	for _, c := range calls {
		perItem[c]++
	}
	if len(calls) != 4 || perItem["item1"] != 1 || perItem["item2"] != 2 || perItem["item3"] != 1 {
		t.Fatalf("worker calls = %v, want each item once in pass 1 and only item 2 again in pass 2", calls)
	}
	prompt := final.lastPrompt()
	for _, want := range []string{"[1::out-item1-call", "[3::out-item3-call"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("final prompt lost a settled item's result (%q):\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "[2::out-item2-call4]") {
		t.Errorf("final prompt must carry item 2's pass-2 result:\n%s", prompt)
	}
}

// A refinement retry of a worker that already marked its item done works:
// the evaluator's retry verdict reopens the item (board_status: todo), so the
// retried worker's claim is accepted instead of refused by its own done item.
func TestVerdictBoardStatus_ReopensItemSoRetryCanReclaim(t *testing.T) {
	var claims []bool
	worker := &funcRunner{name: "w", fn: func(ctx context.Context, _ string) (string, error) {
		idx := itemOf(t, ctx)
		b := board.FromContext(ctx)
		_, ok := b.Claim(idx, board.IdentityFrom(ctx))
		claims = append(claims, ok)
		b.Update(idx, board.StatusDone, "") // done before evaluation, like the live run
		return "work", nil
	}}
	evals := 0
	evaluator := &funcRunner{name: "e", fn: func(context.Context, string) (string, error) {
		evals++
		if evals == 1 {
			return "needs_refinement", nil
		}
		return "good_to_go", nil
	}}
	def := workflow.Definition{
		Name:  "t",
		Board: true,
		Vars:  map[string]string{"plan": "## Item 1\na\n"},
		Stages: []workflow.Stage{
			{ID: "seed", Kind: workflow.StageKindBoardSeed, Over: "Item", From: "plan"},
			{ID: "fan", Kind: workflow.StageKindParallelGroup, Over: "Item", From: "plan", MaxConcurrency: 1, Body: []workflow.Stage{
				{ID: "attempts", Kind: workflow.StageKindLoop, MaxIterations: 3, IterationVar: "attempt", Body: []workflow.Stage{
					{ID: "work", Kind: workflow.StageKindAgentTurn, Role: "w", Prompt: "go", SaveAs: "out"},
					{ID: "eval", Kind: workflow.StageKindAgentTurn, Role: "e", Prompt: "check", Verdict: map[string]workflow.VerdictRule{
						"good_to_go":       {Action: "break"},
						"needs_refinement": {Action: "retry", BoardStatus: "todo"},
					}},
				}},
			}},
		},
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := New(def, "task").Run(context.Background(), runCfg(map[string]workflow.TurnRunner{"w": worker, "e": evaluator})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(claims) != 2 || !claims[0] || !claims[1] {
		t.Errorf("claims = %v, want both the first attempt and the refinement retry accepted", claims)
	}
}
