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

// TestRun_UnlimitedMaxToolIterations pins limits.max_tool_iterations: -1 —
// the runner maps the config resolver's 0 ("unlimited") onto this negative
// sentinel, and runToolLoop must honor it: no forced closing summary, no
// budget stop, only the context (or a loop detector) can end the turn. The
// old `maxIter <= 0 → default` mapping silently turned an unlimited budget
// into the default cap, so "-1 = unlimited" never actually worked.
func TestRun_UnlimitedMaxToolIterations(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		if i >= 45 {
			// Bound the test: an unlimited loop only ends when the context
			// does. 45 sits above the built-in default cap (40), so a capped
			// loop ends with a budget fallback (nil error, < 45 requests)
			// instead of reaching the cancel.
			cancel()
		}
		// Distinct arguments every iteration — identical calls would trip
		// the consecutive-identical/doom-loop gate, which is not what this
		// test is about.
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc%d","function":{"name":"read_file","arguments":"{\"path\":\"/nonexistent/file%d\"}"}}]}}]}`+"\n\n", i, i)
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: -1})
	agent.skipPerms = true
	var out strings.Builder

	_, err := agent.Run(ctx, nil, "keep going", &out, &session.Session{}, nil)
	if err == nil {
		t.Fatalf("expected Run to end on the canceled context (unlimited budget), got nil error after %d requests — the budget was capped instead", n.Load())
	}
	if got := n.Load(); got < 45 {
		t.Errorf("unlimited budget stopped after %d requests, want >= 45 (a capped loop stops at the default budget, ~40)", got)
	}
}
