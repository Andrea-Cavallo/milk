package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/session"
	"github.com/scoutme/milk/internal/workflow"
	"github.com/scoutme/milk/internal/workflow/interp"
)

func TestDefaultRoleValues(t *testing.T) {
	def := workflow.Definition{Roles: []string{"designer", "generator"}}
	got := defaultRoleValues(def, map[string]string{"designer": "mimo", "extra": "ignored"})
	if got["designer"] != "mimo" || got["generator"] != workflow.AliasEscalation || len(got) != 2 {
		t.Errorf("defaultRoleValues = %v", got)
	}
}

func TestStateApplyProgressKeepsLaunchFields(t *testing.T) {
	st := &workflow.State{AgentMap: map[string]string{"writer": "mimo"}, StageTree: &workflow.StageNode{}}
	st.ApplyProgress(workflow.ProgressMsg{WorkflowName: "pair", Task: "t", WorkflowID: 3, Role: "writer"})
	if st.WorkflowName != "pair" || st.Task != "t" || st.WorkflowID != 3 || st.Role != "writer" || !st.Generic {
		t.Errorf("progress not applied: %+v", st)
	}
	if st.AgentMap["writer"] != "mimo" || st.StageTree == nil {
		t.Error("launch-time AgentMap/StageTree were clobbered")
	}
}

func TestLoadSavedWorkflowOutcomes(t *testing.T) {
	sandboxMilkHome(t)
	stateDir := stateDirForTest(t)
	sess := &session.Session{ID: "core-saved-session"}

	if _, err := loadSavedWorkflow(sess); !errors.Is(err, errNoSavedWorkflow) {
		t.Fatalf("empty session: err = %v, want errNoSavedWorkflow", err)
	}

	path := workflow.InterpCheckpointPath(stateDir, sess.ID, 0)
	if err := interp.SaveCheckpoint(path, &interp.Checkpoint{DefinitionName: "no-such-workflow", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSavedWorkflow(sess); err == nil || !strings.Contains(err.Error(), "no longer registered") {
		t.Fatalf("unregistered definition: err = %v", err)
	}

	if err := interp.SaveCheckpoint(path, &interp.Checkpoint{DefinitionName: "pair", Task: "build it"}); err != nil {
		t.Fatal(err)
	}
	saved, err := loadSavedWorkflow(sess)
	if err != nil {
		t.Fatalf("resumable checkpoint: %v", err)
	}
	if saved.ID != 0 || saved.Def.Name != "pair" || saved.Checkpoint.Task != "build it" {
		t.Errorf("saved = %+v", saved)
	}
	for _, role := range saved.Def.Roles {
		if saved.RoleValues()[role] != workflow.AliasEscalation {
			t.Errorf("role %q should default to the escalation alias, got %q", role, saved.RoleValues()[role])
		}
	}

	if err := interp.SaveCheckpoint(path, &interp.Checkpoint{DefinitionName: "pair", Task: "x", Done: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSavedWorkflow(sess); !errors.Is(err, errWorkflowDone) {
		t.Errorf("done checkpoint: err = %v, want errWorkflowDone", err)
	}
}

func TestClearSavedWorkflowRenamesNotDeletes(t *testing.T) {
	sandboxMilkHome(t)
	stateDir := stateDirForTest(t)
	sessID := "core-clear-session"
	path := workflow.InterpCheckpointPath(stateDir, sessID, 2)
	if err := interp.SaveCheckpoint(path, &interp.Checkpoint{DefinitionName: "pair", Task: "x"}); err != nil {
		t.Fatal(err)
	}

	got, err := clearSavedWorkflow(stateDir, sessID, 2, workflow.WorkflowKindInterp)
	if err != nil || got != path {
		t.Fatalf("clear = %q, %v", got, err)
	}
	if cp, _ := interp.LoadCheckpoint(path); cp != nil {
		t.Error("checkpoint still at its original path")
	}
	if cp, _ := interp.LoadCheckpoint(path + ".cleared"); cp == nil {
		t.Error("checkpoint was deleted instead of renamed to .cleared")
	}
	// Clearing twice is harmless.
	if _, err := clearSavedWorkflow(stateDir, sessID, 2, workflow.WorkflowKindInterp); err != nil {
		t.Errorf("second clear: %v", err)
	}
}
