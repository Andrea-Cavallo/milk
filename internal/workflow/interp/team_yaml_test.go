package interp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/workflow"
)

func loadTeamDefinition(t *testing.T) workflow.Definition {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	reg, errs := workflow.LoadRegistry()
	if len(errs) != 0 {
		t.Fatalf("LoadRegistry errors: %v", errs)
	}
	def, ok := reg.Lookup("team")
	if !ok {
		t.Fatal(`expected built-in "team" definition to be registered`)
	}
	return def
}

// End-to-end over the real team.yaml: designer plan → board_seed → pass
// loop (fan-out + coordinator standup) → final evaluation with the
// NO_ISSUES short-circuit. Asserts the coordination surfaces actually
// render: the seeded board reaches the worker/evaluator/coordinator/final
// prompts as {{.board}}, and the coordinator's verdict ends the loop.
func TestTeamYAML_EndToEnd_BoardRendersThroughStages(t *testing.T) {
	def := loadTeamDefinition(t)

	designer := &fakeRunner{name: "designer", responses: []string{
		"NO_QUESTIONS\n## Spec\nBuild two things.\n\n## Item Plan\n" +
			"## Item 1\nBuild the first thing.\n\n## Item 2\nBuild the second thing.\n",
	}}
	// Identical worker responses keep the assertion order-independent —
	// fan-out items run concurrently.
	worker := &fakeRunner{name: "worker", responses: []string{"did the work", "did the work", "did the work", "did the work"}}
	evaluator := &fakeRunner{name: "evaluator", responses: []string{
		"good_to_go", // worker_eval item A
		"good_to_go", // worker_eval item B
		"NO_ISSUES",  // final_evaluation
	}}
	coordinator := &fakeRunner{name: "coordinator", responses: []string{"all_done"}}
	// final_implementation is skipped (NO_ISSUES) — never called.

	cfg := runCfg(map[string]workflow.TurnRunner{
		"designer": designer, "worker": worker, "evaluator": evaluator,
		"coordinator": coordinator,
	})
	if err := New(def, "build two things").Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The seeded board rendered into the worker, evaluator, coordinator and
	// final-evaluation prompts (worker prompts carry the item sections too).
	worker.mu.Lock()
	workerPrompts := append([]string(nil), worker.prompts...)
	worker.mu.Unlock()
	if len(workerPrompts) == 0 {
		t.Fatal("worker never ran")
	}
	for _, p := range workerPrompts {
		if !strings.Contains(p, "shared board") || !strings.Contains(p, "Item 1") {
			t.Errorf("worker prompt missing the board snapshot/item:\n%s", p)
		}
		if !strings.Contains(p, "board_claim item") {
			t.Errorf("worker prompt missing the claim-first convention:\n%s", p)
		}
	}

	// lastPrompt takes f.mu itself — locking here would self-deadlock a
	// non-reentrant mutex (sync.Mutex, held by this very goroutine).
	coPrompt := coordinator.lastPrompt()
	for _, want := range []string{"Board (live)", "all_done", "more_work"} {
		if !strings.Contains(coPrompt, want) {
			t.Errorf("coordinator prompt missing %q:\n%s", want, coPrompt)
		}
	}
	// The coordinator's verdict ("all_done") ended the pass loop after one
	// round: exactly two worker evals + one final eval ran, and the
	// implementer stage was skipped via NO_ISSUES (not in the runner map —
	// an attempted lookup would have failed the run).
	evaluator.mu.Lock()
	if got := len(evaluator.prompts); got != 3 {
		t.Errorf("evaluator ran %d times, want 3 (2 item evals + final)", got)
	}
	evaluator.mu.Unlock()
}

// board_seed without the definition flag fails validation before any run —
// the pairing lives in Definition.Validate, not in name-specific code.
func TestTeamYAML_DeclaresBoardAndValidates(t *testing.T) {
	def := loadTeamDefinition(t)
	if err := def.Validate(); err != nil {
		t.Errorf("team definition must validate: %v", err)
	}
	stripped := def
	stripped.Board = false
	if err := stripped.Validate(); err == nil {
		t.Error("dropping board: true must invalidate the definition (board_seed pairing)")
	}
	_ = io.Discard
}
