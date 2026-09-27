package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/workflow"
)

// TestWorkflowChunkMsg_GoesToLiveBufferNotTranscript is the regression test
// for the bug ADR-0047 fixes: streamed workflow-stage output must land in
// the workflow's own live buffer (viewable via attach), never in the main
// session transcript.
func TestWorkflowChunkMsg_GoesToLiveBufferNotTranscript(t *testing.T) {
	m := testModel()
	m.workflowState = &workflow.State{WorkflowName: "dev", Role: "designer"}

	const chunk = "some very distinctive streamed stage output"
	updated, _ := m.Update(workflow.WorkflowChunkMsg{Text: chunk})
	mm := updated.(model)

	if strings.Contains(mm.transcript.String(), chunk) {
		t.Fatalf("workflow chunk leaked into main transcript: %s", mm.transcript.String())
	}
	if got := mm.workflowState.LiveBuffer().Snapshot(); got != chunk {
		t.Fatalf("workflowState.Live.Snapshot() = %q, want %q", got, chunk)
	}
}

// TestWorkflowChunkMsg_NilWorkflowStateIsSafe guards against a nil
// m.workflowState (shouldn't happen given reportProgress always precedes a
// stage's Turn call, but the handler must not panic if it ever does).
func TestWorkflowChunkMsg_NilWorkflowStateIsSafe(t *testing.T) {
	m := testModel()
	m.workflowState = nil

	updated, _ := m.Update(workflow.WorkflowChunkMsg{Text: "chunk"})
	mm := updated.(model)

	if strings.Contains(mm.transcript.String(), "chunk") {
		t.Fatalf("workflow chunk leaked into main transcript: %s", mm.transcript.String())
	}
}
