package local

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/scoutme/milk/internal/session"
)

// alternatingDuplicateToolServer alternates between two distinct "unknown
// tool" names starting at request 3, so every call from request 3 onward is
// a *duplicate* of an earlier one (triggering the duplicate-tool-call
// detector every time) while never repeating the *same* call twice in a
// row (so the doom-loop gate's consecutive-match counter never reaches its
// own, unrelated threshold — this isolates the aggregate recovery counter
// this test is actually about).
func alternatingDuplicateToolServer() (*httptest.Server, *int32) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		name := "x-task-a"
		if n%2 == 0 {
			name = "x-task-b"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc%d","function":{"name":%q,"arguments":"{}"}}]}}]}`+"\n\n", n, name)
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv, &requests
}

// TestEscalateAfterRecoveries_Fires drives the aggregate recovery counter
// (duplicate-tool-call detections only, for a deterministic repro) past
// escalateAfterRecoveries and asserts Run returns an *EscalationSignal
// instead of continuing to nudge.
func TestEscalateAfterRecoveries_Fires(t *testing.T) {
	srv, requests := alternatingDuplicateToolServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").
		WithMemConfig(MemConfig{MaxToolIterations: 20}).
		WithEscalateAfterRecoveries(3)
	sess := &session.Session{ID: "escalate-after-recoveries"}
	var out strings.Builder

	_, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	esc, ok := err.(*EscalationSignal)
	if !ok {
		t.Fatalf("expected an *EscalationSignal once the aggregate recovery count reached 3, got %v (%T)", err, err)
	}
	if !strings.Contains(esc.Reason, "3 loop recoveries") {
		t.Errorf("expected the reason to cite the recovery count, got %q", esc.Reason)
	}
	// requests 1,2 are the first (unique) calls; 3,4,5 are duplicates
	// (recovery counts 1,2,3) — escalation should fire right on the 3rd,
	// without a 6th request.
	if got := atomic.LoadInt32(requests); got != 5 {
		t.Errorf("expected exactly 5 requests (2 unique + 3 duplicates, escalating on the 3rd), got %d", got)
	}
}

// TestEscalateAfterRecoveries_BelowThreshold_BehavesAsBefore confirms a turn
// that never crosses the threshold sees no behavior change at all: it keeps
// recovering via crop+nudge and completes normally.
func TestEscalateAfterRecoveries_BelowThreshold_BehavesAsBefore(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n <= 2 {
			name := "x-task-a"
			if n == 2 {
				name = "x-task-b"
			}
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc%d","function":{"name":%q,"arguments":"{}"}}]}}]}`+"\n\n", n, name)
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else if n == 3 {
			// One duplicate (recovery count 1) — below the threshold of 3.
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc3","function":{"name":"x-task-a","arguments":"{}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").
		WithMemConfig(MemConfig{MaxToolIterations: 20}).
		WithEscalateAfterRecoveries(3)
	sess := &session.Session{ID: "below-threshold"}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "done") {
		t.Errorf("expected the turn to recover and complete normally, got %q", last.Content)
	}
}

// TestEscalateAfterRecoveries_WorkflowRole_NeverEscalates confirms a
// workflow step never escalates even past the aggregate threshold — there is
// no handling for EscalationSignal in the workflow interpreter today (see
// canEscalate's doc comment), so it must keep today's crop/nudge behavior.
func TestEscalateAfterRecoveries_WorkflowRole_NeverEscalates(t *testing.T) {
	srv, requests := alternatingDuplicateToolServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").
		WithMemConfig(MemConfig{MaxToolIterations: 20}).
		WithEscalateAfterRecoveries(3).
		AsWorkflowExecutor()
	sess := &session.Session{ID: "workflow-role"}
	var out strings.Builder

	_, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if _, ok := err.(*EscalationSignal); ok {
		t.Fatalf("workflow-role turn must never escalate, got %v", err)
	}
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}
	// The duplicate-tool-call ladder's own max recovery (5) terminates
	// before the server's unbounded alternation would run forever — confirm
	// it eventually stopped rather than hitting MaxToolIterations.
	if got := atomic.LoadInt32(requests); got >= 20 {
		t.Errorf("expected the duplicate-tool-call ladder's own termination to stop the turn well before MaxToolIterations, got %d requests", got)
	}
}

// TestEscalateAfterRecoveries_ToolAgentRole_NeverEscalates mirrors the
// workflow-role case for a stateless tool-agent call (RunToolCall) — no
// session/runner exists behind it to escalate into (see canEscalate).
func TestEscalateAfterRecoveries_ToolAgentRole_NeverEscalates(t *testing.T) {
	srv, requests := alternatingDuplicateToolServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").
		WithMemConfig(MemConfig{MaxToolIterations: 20}).
		WithEscalateAfterRecoveries(3).
		WithToolAgentRole()
	sess := &session.Session{ID: "tool-agent-role"}
	var out strings.Builder

	_, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if _, ok := err.(*EscalationSignal); ok {
		t.Fatalf("tool-agent-role turn must never escalate, got %v", err)
	}
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(requests); got >= 20 {
		t.Errorf("expected the duplicate-tool-call ladder's own termination to stop the turn well before MaxToolIterations, got %d requests", got)
	}
}

// TestEscalateAfterRecoveries_Disabled_NeverEscalates confirms a <= 0
// threshold fully disables the forced-escalation path, regardless of how
// many recoveries accumulate.
func TestEscalateAfterRecoveries_Disabled_NeverEscalates(t *testing.T) {
	srv, requests := alternatingDuplicateToolServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").
		WithMemConfig(MemConfig{MaxToolIterations: 20}).
		WithEscalateAfterRecoveries(0)
	sess := &session.Session{ID: "disabled"}
	var out strings.Builder

	_, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if _, ok := err.(*EscalationSignal); ok {
		t.Fatalf("expected no escalation with the feature disabled, got %v", err)
	}
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(requests); got >= 20 {
		t.Errorf("expected the duplicate-tool-call ladder's own termination to stop the turn well before MaxToolIterations, got %d requests", got)
	}
}
