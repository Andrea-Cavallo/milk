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
