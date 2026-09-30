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

// canned server: returns the same "create_task" tool call for the first n
// requests, then a plain final text response afterward.
func doomLoopServer(n int32) (*httptest.Server, *int32) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if count <= n {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc","function":{"name":"create_task","arguments":"{\"title\":\"same\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv, &requests
}

func TestDoomLoopGate_InteractiveApprove_ContinuesAndResets(t *testing.T) {
	srv, requests := doomLoopServer(3)
	defer srv.Close()

	var asked int32
	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent = agent.WithPermissions(nil, func(tool, summary string) bool {
		atomic.AddInt32(&asked, 1)
		if tool != "doom_loop" {
			t.Errorf("expected the ask to be for tool %q, got %q", "doom_loop", tool)
		}
		return true // approve — let the loop continue
	})
	sess := &session.Session{}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if atomic.LoadInt32(&asked) != 1 {
		t.Errorf("expected exactly one doom_loop ask, got %d", asked)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "done") {
		t.Errorf("expected the turn to complete normally after approval, got %q", last.Content)
	}
	if got := atomic.LoadInt32(requests); got != 4 {
		t.Errorf("expected 4 requests (3 identical + 1 final), got %d", got)
	}
}

func TestDoomLoopGate_InteractiveDeny_Terminates(t *testing.T) {
	srv, requests := doomLoopServer(10) // would keep repeating well past 3 if not stopped
	defer srv.Close()

	var asked int32
	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent = agent.WithPermissions(nil, func(tool, summary string) bool {
		atomic.AddInt32(&asked, 1)
		return false // deny
	})
	sess := &session.Session{}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if atomic.LoadInt32(&asked) != 1 {
		t.Errorf("expected exactly one doom_loop ask, got %d", asked)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "the user declined to let it continue") {
		t.Errorf("expected a user-declined termination message, got %q", last.Content)
	}
	if got := atomic.LoadInt32(requests); got != 3 {
		t.Errorf("expected exactly 3 requests before stopping, got %d", got)
	}
}

func TestDoomLoopGate_BackgroundJob_FailsClosedWithoutAsking(t *testing.T) {
	srv, requests := doomLoopServer(10)
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent = agent.WithPermissions(nil, func(tool, summary string) bool {
		t.Fatal("permAsk must not be called for a background job — there's no one to ask")
		return false
	})
	agent.jobID = "test-job"
	sess := &session.Session{}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "this context has no way to ask for confirmation") {
		t.Errorf("expected a fail-closed termination message, got %q", last.Content)
	}
	if got := atomic.LoadInt32(requests); got != 3 {
		t.Errorf("expected exactly 3 requests before stopping, got %d", got)
	}
}

func TestDoomLoopGate_DifferentToolCalls_NeverFires(t *testing.T) {
	// Each request returns a DIFFERENT tool call, then a final response —
	// the doom-loop gate must never fire when calls aren't identical.
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if count <= 3 {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc","function":{"name":"create_task","arguments":"{\"title\":\"task-%d\"}"}}]}}]}`+"\n\n", count)
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 15})
	agent = agent.WithPermissions(nil, func(tool, summary string) bool {
		t.Fatal("permAsk must not be called when tool calls differ across iterations")
		return false
	})
	sess := &session.Session{}
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, sess, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	last := history[len(history)-1]
	if !strings.Contains(last.Content, "done") {
		t.Errorf("expected the turn to complete normally, got %q", last.Content)
	}
}
