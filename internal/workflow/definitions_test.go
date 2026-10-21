package workflow

import (
	"strings"
	"testing"
)

func TestDefinition_Validate_MissingName(t *testing.T) {
	d := Definition{Stages: []Stage{{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p"}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestDefinition_Validate_NoStages(t *testing.T) {
	d := Definition{Name: "x"}
	if err := d.Validate(); err == nil {
		t.Error("expected error for no stages")
	}
}

func TestDefinition_Validate_AgentTurnMissingRoleOrPrompt(t *testing.T) {
	cases := []Stage{
		{ID: "a", Kind: StageKindAgentTurn, Prompt: "p"},
		{ID: "a", Kind: StageKindAgentTurn, Role: "r"},
	}
	for _, s := range cases {
		d := Definition{Name: "x", Stages: []Stage{s}}
		if err := d.Validate(); err == nil {
			t.Errorf("expected error for stage %+v", s)
		}
	}
}

func TestDefinition_Validate_RunUnlessFieldsMustBePaired(t *testing.T) {
	cases := []Stage{
		{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p", RunUnlessMarkerIn: "x"},
		{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p", RunUnlessContains: "x"},
	}
	for _, s := range cases {
		d := Definition{Name: "x", Stages: []Stage{s}}
		if err := d.Validate(); err == nil {
			t.Errorf("expected error for one-sided run_unless_* on stage %+v", s)
		}
	}
}

func TestDefinition_Validate_UnknownEmptyOutputFallback(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{
		{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p", EmptyOutputFallback: "bogus"},
	}}
	if err := d.Validate(); err == nil {
		t.Error("expected error for unknown empty_output_fallback")
	}
}

func TestDefinition_Validate_LoopNeedsOverOrMaxIterations(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{
		ID: "loop1", Kind: StageKindLoop,
		Body: []Stage{{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p"}},
	}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error: loop with neither over nor max_iterations")
	}
}

func TestDefinition_Validate_BoundedLoopRequiresVerdictOnLastBodyStage(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{
		ID: "loop1", Kind: StageKindLoop, MaxIterations: 3,
		Body: []Stage{{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p"}},
	}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error: max_iterations-bounded loop body missing verdict rules")
	}
}

func TestDefinition_Validate_ValidBoundedLoop(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{
		ID: "loop1", Kind: StageKindLoop, MaxIterations: 3,
		Body: []Stage{{
			ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p",
			Verdict: map[string]VerdictRule{"good_to_go": {Action: "break"}},
		}},
	}}}
	if err := d.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefinition_Validate_ParallelGroupNeedsOver(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{
		ID: "pg", Kind: StageKindParallelGroup,
		Body: []Stage{{ID: "a", Kind: StageKindAgentTurn, Role: "r", Prompt: "p"}},
	}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error: parallel_group missing over")
	}
}

func TestDefinition_Validate_UnknownKind(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{ID: "a", Kind: "not_a_kind"}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error for unknown stage kind")
	}
}

func TestDefinition_Validate_MissingID(t *testing.T) {
	d := Definition{Name: "x", Stages: []Stage{{Kind: StageKindAgentTurn, Role: "r", Prompt: "p"}}}
	if err := d.Validate(); err == nil {
		t.Error("expected error for stage missing id")
	}
}

func TestDefinitionHasParallelGroup(t *testing.T) {
	plain := Definition{Stages: []Stage{{ID: "a", Kind: StageKindAgentTurn}}}
	nested := Definition{Stages: []Stage{{ID: "l", Kind: StageKindLoop, Body: []Stage{{ID: "pg", Kind: StageKindParallelGroup}}}}}
	if plain.HasParallelGroup() || !nested.HasParallelGroup() {
		t.Errorf("plain=%v nested=%v", plain.HasParallelGroup(), nested.HasParallelGroup())
	}
}

// Issue #197: board_seed is a real stage kind whose contract is bound to
// the definition-level board flag — the interpreter never special-cases a
// workflow by name, so validation is where the pairing is enforced.
func TestValidate_BoardSeedContract(t *testing.T) {
	seed := Stage{ID: "seed", Kind: StageKindBoardSeed, Over: "Item", From: "plan"}

	// board_seed without board: true → error.
	d := Definition{Name: "t", Stages: []Stage{seed}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "board: true") {
		t.Errorf("seed without board flag: err = %v, want the board: true hint", err)
	}

	// With the flag → valid.
	d = Definition{Name: "t", Board: true, Stages: []Stage{seed}}
	if err := d.Validate(); err != nil {
		t.Errorf("seed with board flag: %v", err)
	}

	// Missing over/from → error even with the flag.
	d = Definition{Name: "t", Board: true, Stages: []Stage{{ID: "s", Kind: StageKindBoardSeed}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "over and from") {
		t.Errorf("incomplete seed: err = %v, want over/from requirement", err)
	}

	// Nested body also counts toward the pairing.
	d = Definition{Name: "t", Stages: []Stage{{
		ID: "l", Kind: StageKindLoop, Over: "Item", From: "plan",
		Body: []Stage{seed},
	}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "board: true") {
		t.Errorf("nested seed without board flag: err = %v", err)
	}
}

// board_status / skip_if_board_status are generic board features: they need a
// board, board_status needs a current item (a parallel_group body), and the
// statuses must exist (and be settable, for board_status).
func TestDefinition_Validate_BoardFields(t *testing.T) {
	item := func(rule VerdictRule) Stage {
		return Stage{ID: "fan", Kind: StageKindParallelGroup, Over: "Item", Body: []Stage{{
			ID: "eval", Kind: StageKindAgentTurn, Role: "e", Prompt: "p",
			Verdict: map[string]VerdictRule{"retry_me": rule},
		}}}
	}
	def := func(board bool, stages ...Stage) Definition {
		return Definition{Name: "d", Board: board, Stages: stages}
	}
	tests := []struct {
		name    string
		d       Definition
		wantErr string
	}{
		{"valid reopen", def(true, item(VerdictRule{Action: "retry", BoardStatus: "todo"})), ""},
		{"board_status without board", def(false, item(VerdictRule{Action: "retry", BoardStatus: "todo"})), "requires \"board: true\""},
		{"board_status outside an item", def(true, Stage{ID: "e", Kind: StageKindAgentTurn, Role: "e", Prompt: "p",
			Verdict: map[string]VerdictRule{"x": {Action: "retry", BoardStatus: "todo"}}}), "inside a parallel_group"},
		{"claimed is not settable", def(true, item(VerdictRule{Action: "retry", BoardStatus: "claimed"})), "not settable"},
		{"unknown status", def(true, item(VerdictRule{Action: "retry", BoardStatus: "nope"})), "not settable"},
		{"skip filter valid", def(true, Stage{ID: "fan", Kind: StageKindParallelGroup, Over: "Item", SkipIfBoardStatus: []string{"done"},
			Body: []Stage{{ID: "w", Kind: StageKindAgentTurn, Role: "w", Prompt: "p"}}}), ""},
		{"skip filter without board", def(false, Stage{ID: "fan", Kind: StageKindParallelGroup, Over: "Item", SkipIfBoardStatus: []string{"done"},
			Body: []Stage{{ID: "w", Kind: StageKindAgentTurn, Role: "w", Prompt: "p"}}}), "requires \"board: true\""},
		{"skip filter unknown status", def(true, Stage{ID: "fan", Kind: StageKindParallelGroup, Over: "Item", SkipIfBoardStatus: []string{"finished"},
			Body: []Stage{{ID: "w", Kind: StageKindAgentTurn, Role: "w", Prompt: "p"}}}), "unknown board status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.d.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
