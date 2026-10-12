package main

import (
	"reflect"
	"testing"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/tasks"
)

// TestWireLocalAgent_AppliesEveryCallback pins the issue #188 contract: the
// one shared wiring place must actually reach the agent's hook fields, so the
// TUI, ACP and agent-as-tool runners cannot silently drift apart (the
// divergence that left tool-agents with a nil ask — never prompting at all).
// Field names are local.Agent's internals on purpose: if that struct renames
// its hooks, this pin fails and the wiring gets re-checked against them.
func TestWireLocalAgent_AppliesEveryCallback(t *testing.T) {
	taskSt, err := tasks.New(t.TempDir(), "sess")
	if err != nil {
		t.Fatalf("tasks.New: %v", err)
	}
	w := localAgentWiring{
		permStore:           &local.PermStore{},
		permAsk:             func(string, string) bool { return true },
		bgPermAsk:           func(string, string, string) bool { return true },
		safetyAsk:           func(string, string, string) local.SafetyOutcome { return local.SafetyApproved },
		skipPerms:           func() bool { return false },
		onOpenFile:          func(string) error { return nil },
		onRetract:           func(string, string) {},
		onToolUse:           func(string, string, string, map[string]any) {},
		onToolResult:        func(string, string, string, bool) {},
		onThinking:          func(string) {},
		onReasoningPromoted: func() {},
		taskStore:           taskSt,
		mgr:                 local.NewManager(t.Context(), 1),
	}

	wired := wireLocalAgent(local.New("", ""), w)
	v := reflect.ValueOf(wired).Elem()
	for _, field := range []string{
		"skipPermsFn", "bgPermAsk", "safetyAsk", "permStore", "permAsk",
		"onOpenFile", "onRetract", "onToolUse", "onToolResult", "onThinking",
		"onReasoningPromoted", "taskStore", "backgroundManager",
	} {
		f := v.FieldByName(field)
		if !f.IsValid() {
			t.Fatalf("local.Agent has no field %q — re-check wireLocalAgent against the agent's hooks", field)
		}
		if f.IsNil() {
			t.Errorf("wireLocalAgent left %s nil — the host callback never reached the agent", field)
		}
	}
}

// TestWireLocalAgent_NilAndZeroWiring: no host, no agent — wireLocalAgent
// must pass both through unchanged rather than inventing defaults.
func TestWireLocalAgent_NilAndZeroWiring(t *testing.T) {
	if got := wireLocalAgent(nil, localAgentWiring{}); got != nil {
		t.Errorf("wireLocalAgent(nil) = %v, want nil", got)
	}
	a := local.New("", "")
	if got := wireLocalAgent(a, localAgentWiring{}); got == nil {
		t.Error("wireLocalAgent with zero wiring must return the agent untouched")
	}
	v := reflect.ValueOf(wireLocalAgent(local.New("", ""), localAgentWiring{})).Elem()
	if !v.FieldByName("permAsk").IsNil() {
		t.Error("zero wiring must not install a permission ask")
	}
	if !v.FieldByName("backgroundManager").IsNil() {
		t.Error("zero wiring must not install a background manager")
	}
}
