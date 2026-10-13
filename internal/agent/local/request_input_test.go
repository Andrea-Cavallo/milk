package local

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scoutme/milk/internal/obs"
	"github.com/scoutme/milk/internal/session"
)

func usageServer(prompt, cached int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprintf(w, `data: {"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":%d}}}`+"\n\n", prompt, cached)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// The context-size record is one request's total input (fresh + cached), kept
// per exact role: stateless tool-agent and workflow clones are separate
// conversations and must not feed the plain role the ctx:x/y indicator reads.
func TestRequestInput_RecordedPerExactRole(t *testing.T) {
	obs.ResetSessionTokens()
	t.Cleanup(obs.ResetSessionTokens)

	main := usageServer(1000, 400)
	defer main.Close()
	clone := usageServer(5000, 0)
	defer clone.Close()

	run := func(a *Agent) {
		t.Helper()
		if _, err := a.Run(context.Background(), nil, "hi", io.Discard, &session.Session{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	run(New(main.URL, "m"))
	run(New(clone.URL, "m").WithToolAgentRole())
	run(New(clone.URL, "m").AsWorkflowExecutor())

	if got := obs.SessionLastRequestInput("primary"); got != 1000 {
		t.Errorf("primary: got %d, want 1000 (600 fresh + 400 cached)", got)
	}
	if got := obs.SessionLastRequestInput("primary:toolagent"); got != 5000 {
		t.Errorf("primary:toolagent: got %d, want 5000", got)
	}
	if got := obs.SessionLastRequestInput("primary:workflow"); got != 5000 {
		t.Errorf("primary:workflow: got %d, want 5000", got)
	}
	if read, _ := obs.SessionCacheByRole("primary"); read != 400 {
		t.Errorf("primary cache_read: got %d, want 400 (local requests must record cache tokens)", read)
	}
}
