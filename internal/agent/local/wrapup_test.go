package local

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scoutme/milk/internal/session"
)

// TestRun_TurnWrapUpForcesClosingSummary pins the opt-in turn deadline's
// graceful path (issue #305): when the wrap-up signal fires, the next
// iteration boundary injects deadlineWrapUpReminder and disables tools, and
// the turn ends with a model-authored closing summary instead of continuing
// to call tools (or being cancelled mid-work).
func TestRun_TurnWrapUpForcesClosingSummary(t *testing.T) {
	var n atomic.Int32
	var mu sync.Mutex
	var bodies []string
	var sawTools []bool
	wrapCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		i := n.Add(1)
		mu.Lock()
		bodies = append(bodies, body)
		sawTools = append(sawTools, strings.Contains(body, `"tools"`))
		mu.Unlock()
		if i == 1 {
			// One normal tool iteration, then the deadline fires (the
			// watchdog closes the wrap-up channel mid-turn).
			close(wrapCh)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(body, `"tools"`) {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc1","function":{"name":"read_file","arguments":"{\"path\":\"/nonexistent/x\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"closing summary: did X, Y remains"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var activity atomic.Int32
	ctx := WithActivityNotify(context.Background(), func() { activity.Add(1) })
	ctx = WithTurnWrapUp(ctx, wrapCh)

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 40})
	agent.skipPerms = true
	var out strings.Builder

	if _, err := agent.Run(ctx, nil, "do the task", &out, &session.Session{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := n.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2 (one tool round, one forced closing summary)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawTools[0] {
		t.Errorf("request 1 should carry tools, got: %s", bodies[0][:min(200, len(bodies[0]))])
	}
	if sawTools[1] {
		t.Errorf("request 2 must go out with tools disabled, got: %s", bodies[1][:min(200, len(bodies[1]))])
	}
	if !strings.Contains(bodies[1], "TURN DEADLINE REACHED") {
		t.Errorf("request 2 must carry the deadline wrap-up reminder, got: %s", bodies[1][:min(300, len(bodies[1]))])
	}
	if !strings.Contains(out.String(), "closing summary") {
		t.Errorf("out = %q, want the model's closing summary", out.String())
	}
	if activity.Load() < 2 {
		t.Errorf("activity notifications = %d, want >= 2 (iteration boundaries/tool activity)", activity.Load())
	}
}

// TestRun_WrapUpIgnoredWhenUnset: no wrap-up signal on ctx behaves exactly as
// before — the plain tool loop runs to its budget/final response (the hook is
// opt-in plumbing, not a behavior change).
func TestRun_WrapUpIgnoredWhenUnset(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if i < 3 {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc%d","function":{"name":"read_file","arguments":"{\"path\":\"/nonexistent/f%d\"}"}}]}}]}`+"\n\n", i, i)
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 40})
	agent.skipPerms = true
	var out strings.Builder
	if _, err := agent.Run(context.Background(), nil, "go", &out, &session.Session{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := n.Load(); got != 3 {
		t.Errorf("requests = %d, want 3 tool-loop iterations unaffected by absent wrap-up", got)
	}
}
