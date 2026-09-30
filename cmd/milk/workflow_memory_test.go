package main

import (
	"context"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/oversight"
)

// TestWorkflowTurnRunner_UseMemory_InjectsPerceptsOnlyWhenOptedIn verifies
// the per-stage memory opt-in (Stage.UseMemory -> SetUseMemoryForNextCall):
// percepts are only fetched and passed to Execute when a call was flagged,
// and never otherwise — matching workflow roles' isolated-by-default design.
func TestWorkflowTurnRunner_UseMemory_InjectsPerceptsOnlyWhenOptedIn(t *testing.T) {
	store, err := memory.NewStore(t.TempDir(), "test-session")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.Record(context.Background(), "the user prefers tabs over spaces", memory.ProducerUser, "", memory.Roles{}, false); err != nil {
		t.Fatalf("Record: %v", err)
	}

	inner := &fakeTurnRunner{}
	wtr := &workflowTurnRunner{
		inner:    inner,
		role:     RoleWorkflow,
		roleName: "generator",
		notifier: oversight.Noop{},
		mem:      store,
		cfg:      config.Config{},
	}

	// Not opted in (default): no percepts passed.
	if _, err := wtr.Run(context.Background(), "go", nil); err != nil {
		t.Fatalf("Run (opted out): %v", err)
	}
	// Opted in: percepts fetched and passed. The prompt shares a token
	// ("tabs") with the recorded percept so the default relevance gate
	// (config.Config{}'s PerceptRelevanceGate defaults to enabled) doesn't
	// filter it back out.
	wtr.SetUseMemoryForNextCall(true)
	if _, err := wtr.Run(context.Background(), "should this use tabs or spaces?", nil); err != nil {
		t.Fatalf("Run (opted in): %v", err)
	}
	// Opted back out: no percepts passed again — the flag isn't sticky
	// beyond the call it was set for.
	wtr.SetUseMemoryForNextCall(false)
	if _, err := wtr.Run(context.Background(), "go a third time", nil); err != nil {
		t.Fatalf("Run (opted out again): %v", err)
	}

	if len(inner.perceptsCalls) != 3 {
		t.Fatalf("expected 3 Execute calls, got %d", len(inner.perceptsCalls))
	}
	if len(inner.perceptsCalls[0]) != 0 {
		t.Errorf("call 1 (opted out): expected no percepts, got %v", inner.perceptsCalls[0])
	}
	if len(inner.perceptsCalls[1]) == 0 {
		t.Errorf("call 2 (opted in): expected percepts to be injected, got none")
	}
	if len(inner.perceptsCalls[2]) != 0 {
		t.Errorf("call 3 (opted out again): expected no percepts, got %v", inner.perceptsCalls[2])
	}
}
