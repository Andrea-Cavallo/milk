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

// duplicateToolCallServer returns a non-identical tool call on request 2 (so
// the doom-loop gate's *consecutive*-match counter never reaches its
// threshold), then repeats request 1's exact call on request 3 — triggering
// the duplicate-tool-call detector (which tracks "ever executed this turn",
// not just consecutive) in isolation from the doom-loop gate. Request 4
// returns a final response.
func duplicateToolCallServer() (*httptest.Server, *int32) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch count {
		case 1, 3:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc","function":{"name":"create_task","arguments":"{\"title\":\"a\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		case 2:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc","function":{"name":"create_task","arguments":"{\"title\":\"b\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		default:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv, &requests
}

// TestDuplicateToolCall_RecoversWithoutTerminating establishes the baseline
// behavior the analysis in docs/internal/analysis/session-2026-10-01-primary-overload-analysis.md
// relied on: a single repeated tool call crops the looping tail, injects a
// recovery nudge, and lets the turn continue to completion — it must not
// terminate after only one recovery (duplicateToolMaxRecovery is 5).
func TestDuplicateToolCall_RecoversWithoutTerminating(t *testing.T) {
	srv, requests := duplicateToolCallServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	sess := &session.Session{}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "done") {
		t.Errorf("expected the turn to recover and complete normally, got %q", last.Content)
	}
	if got := atomic.LoadInt32(requests); got != 4 {
		t.Errorf("expected exactly 4 requests (1 unique + 1 unique + 1 duplicate + 1 final), got %d", got)
	}
}

// TestDuplicateToolCall_SessionIDIsLoggingOnly guards the sessionID parameter
// threaded into loopRecoveryAction for observability (session-tagged
// recovery logs) — it must never change the recovery outcome itself. A real
// (non-empty) session ID must behave identically to the ""-ID baseline above.
func TestDuplicateToolCall_SessionIDIsLoggingOnly(t *testing.T) {
	srv, requests := duplicateToolCallServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	sess := &session.Session{ID: "sess-with-a-real-id"}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "done") {
		t.Errorf("expected the turn to recover and complete normally, got %q", last.Content)
	}
	if got := atomic.LoadInt32(requests); got != 4 {
		t.Errorf("expected exactly 4 requests, got %d", got)
	}
}

// A duplicate call is detected before it is appended to msgs, so nothing in
// msgs is looping: the recovery nudge must not delete the turn's earlier work.
func TestDuplicateToolCall_RecoveryKeepsEarlierToolWork(t *testing.T) {
	srv, _ := duplicateToolCallServer()
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	var out strings.Builder
	history, err := agent.Run(context.Background(), nil, "do the thing", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var toolIterations, nudges int
	for _, m := range history {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			toolIterations++
		}
		if m.Role == "user" && strings.Contains(m.Content, recoveryDuplicateToolMild[:60]) {
			nudges++
		}
	}
	if toolIterations != 2 || nudges != 1 {
		t.Fatalf("want both executed iterations kept and one nudge, got iterations=%d nudges=%d", toolIterations, nudges)
	}
}

func TestResetExecutedKeysAfterStateChange(t *testing.T) {
	call := func(name, args string) toolCall {
		return toolCall{Function: toolCallFunction{Name: name, Arguments: args}}
	}
	build := call("bash", `{"command":"go build ./..."}`)
	edit := call("edit_file", `{"path":"a.go"}`)
	key := func(c toolCall) string { return c.Function.Name + "\x00" + c.Function.Arguments }

	// A pure read-only batch changes nothing and must not forget anything.
	keys := map[string]bool{key(build): true}
	counts := map[string]int{"list_dir\x00{}": 2}
	resetExecutedKeysAfterStateChange(keys, counts, []toolCall{call("list_dir", `{}`)})
	if !keys[key(build)] || counts["list_dir\x00{}"] != 2 {
		t.Fatal("a read-only batch must not forget earlier calls")
	}

	// A bash call may have changed the world (git checkout, gh pr merge, …):
	// earlier calls are no longer loop evidence (issue #256), and read-only
	// counts reset with them.
	resetExecutedKeysAfterStateChange(keys, counts, []toolCall{call("bash", `{"command":"ls"}`)})
	if keys[key(build)] {
		t.Error("a bash batch must forget earlier calls — bash may change state")
	}
	if counts["list_dir\x00{}"] != 0 {
		t.Error("a bash batch must reset the read-only counts too")
	}

	// A file mutation behaves the same, and the mutating batch's own calls
	// stay so an immediate identical re-issue is still a duplicate.
	keys[key(edit)] = true
	resetExecutedKeysAfterStateChange(keys, counts, []toolCall{edit})
	if !keys[key(edit)] {
		t.Error("the mutating batch's own call must stay, so an immediate identical edit is still a duplicate")
	}
}

// TestIsReadOnlyTool_Classification guards the shared read/write
// classification (issue #256): parallel-safe ⊂ read-only, the widened
// read-only names are in, and mutating tools are out.
func TestIsReadOnlyTool_Classification(t *testing.T) {
	for _, name := range []string{"read_file", "list_dir", "grep", "find_files", "bash_output",
		"web_fetch", "get_session_context", "status_background_agent", "current_need"} {
		if !isReadOnlyTool(name) {
			t.Errorf("isReadOnlyTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"bash", "edit_file", "multiedit", "write_file", "delete_file", "move_file",
		"create_task", "record_memory", "spawn_background_agent", "mcp__x"} {
		if isReadOnlyTool(name) {
			t.Errorf("isReadOnlyTool(%q) = true, want false", name)
		}
	}
	// Everything parallel-safe must be read-only (the barrier model's set is
	// a subset of the detector's, so the two can't drift apart silently).
	for _, name := range []string{"read_file", "find_files", "bash_output", "get_session_context"} {
		if !isParallelSafeTool(name) {
			t.Errorf("isParallelSafeTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"web_fetch", "open_file", "current_need", "wait_background_agent"} {
		if isReadOnlyTool(name) && isParallelSafeTool(name) {
			t.Errorf("isParallelSafeTool(%q) = true, want false (read-only but not concurrency-safe)", name)
		}
	}
}

// scriptedCall is one tool call in a scriptedBatchServer batch.
type scriptedCall struct{ name, args string }

// scriptedBatchServer emits one tool-call batch per request in order; the
// request after the last batch returns a final "done" response. The counter
// reports how many model requests were served.
func scriptedBatchServer(batches [][]scriptedCall) (*httptest.Server, *int32) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := int(atomic.AddInt32(&requests, 1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		if count < len(batches) {
			for j, c := range batches[count] {
				args := strings.ReplaceAll(c.args, `"`, `\"`)
				fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"tc%d","function":{"name":"%s","arguments":"%s"}}]}}]}`+"\n\n", j, j, c.name, args)
			}
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv, &requests
}

func countDuplicateNudges(history []Message) int {
	var nudges int
	for _, m := range history {
		if m.Role == "user" && strings.Contains(m.Content, recoveryDuplicateToolMild[:60]) {
			nudges++
		}
	}
	return nudges
}

// TestDuplicateToolCall_BashStateChangeBetweenChecksDoesNotNudge is the
// issue #256 regression: the same pre-check batch (bash inspection + reads)
// legitimately repeats once per unit of work, with state-changing bash calls
// in between. No occurrence is a duplicate — nothing is stuck, the world
// changed between repeats. The old detector nudged at the second occurrence
// (bash and the read tools were all tracked, and bash never reset the
// memory), which derailed the turn.
func TestDuplicateToolCall_BashStateChangeBetweenChecksDoesNotNudge(t *testing.T) {
	check := []scriptedCall{
		{"bash", `{"command":"echo check"}`},
		{"find_files", `{"pattern":"*.go"}`},
		{"bash_output", `{"shell_id":"s999"}`},
	}
	srv, requests := scriptedBatchServer([][]scriptedCall{
		check,
		{{"bash", `{"command":"echo act-1"}`}},
		check,
		{{"bash", `{"command":"echo act-2"}`}},
		check,
	})
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent.skipPerms = true
	var out strings.Builder
	history, err := agent.Run(context.Background(), nil, "land three PRs", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := countDuplicateNudges(history); n != 0 {
		t.Fatalf("legitimate re-checks across bash state changes must not nudge, got %d nudges", n)
	}
	if got := atomic.LoadInt32(requests); got != 6 {
		t.Errorf("expected 6 requests (5 batches + final), got %d", got)
	}
	if last := history[len(history)-1].Content; !strings.Contains(last, "done") {
		t.Errorf("expected the turn to complete normally, got %q", last)
	}
}

// TestDuplicateToolCall_TrueRepeatStillNudges is the other half of the issue
// #256 acceptance: a literal repeat with nothing in between must still be
// caught (bash included — the batch's own keys survive the reset).
func TestDuplicateToolCall_TrueRepeatStillNudges(t *testing.T) {
	batch := []scriptedCall{{"bash", `{"command":"echo once"}`}}
	srv, requests := scriptedBatchServer([][]scriptedCall{batch, batch})
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent.skipPerms = true
	var out strings.Builder
	history, err := agent.Run(context.Background(), nil, "do the thing", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := countDuplicateNudges(history); n != 1 {
		t.Fatalf("want exactly one duplicate nudge, got %d", n)
	}
	if got := atomic.LoadInt32(requests); got != 3 {
		t.Errorf("expected 3 requests (2 batches + final), got %d", got)
	}
}

// TestDuplicateToolCall_ReadOnlyRepeatThreshold guards the pure-read
// threshold (issue #256): read-only batches may repeat — interleaved with
// other reads — up to readOnlyRepeatLimit identical executions, and only the
// next one is flagged.
func TestDuplicateToolCall_ReadOnlyRepeatThreshold(t *testing.T) {
	r1 := []scriptedCall{{"list_dir", `{"path":"."}`}}
	r2 := []scriptedCall{{"grep", `{"pattern":"zzz-no-match","path":"."}`}}
	var batches [][]scriptedCall
	for i := 0; i < readOnlyRepeatLimit; i++ {
		batches = append(batches, r1, r2)
	}
	batches = append(batches, r1) // occurrence readOnlyRepeatLimit+1: the flagged one
	srv, requests := scriptedBatchServer(batches)
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 20})
	agent.skipPerms = true
	var out strings.Builder
	history, err := agent.Run(context.Background(), nil, "read around", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := countDuplicateNudges(history); n != 1 {
		t.Fatalf("want exactly one duplicate nudge (the readOnlyRepeatLimit+1 occurrence), got %d", n)
	}
	if got := atomic.LoadInt32(requests); got != int32(len(batches))+1 {
		t.Errorf("expected %d requests (%d batches + final), got %d", len(batches)+1, len(batches), got)
	}
}
