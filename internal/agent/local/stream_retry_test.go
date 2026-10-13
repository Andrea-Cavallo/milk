package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/session"
)

// A request that only ever receives keep-alive comments is cancelled by the
// no-data watchdog and re-sent on its own; the tool results the turn had
// already gathered must be in the re-sent request, not lost to a restart.
func TestStreamStall_ResendsOnlyTheStalledRequest(t *testing.T) {
	var requests int32
	var lastBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		b, _ := io.ReadAll(r.Body)
		lastBody.Store(string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		switch n {
		case 1:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"tc","function":{"name":"create_task","arguments":"{\"title\":\"t\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case 2: // the stalled request: keep-alives only, until the client gives up
			fmt.Fprint(w, ": PROCESSING\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model").WithMemConfig(MemConfig{MaxToolIterations: 10})
	agent.streamNoDataTimeout = 150 * time.Millisecond
	var out strings.Builder

	history, err := agent.Run(context.Background(), nil, "do the thing", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Errorf("want 3 requests (call, stalled, re-sent), got %d", got)
	}
	if last := history[len(history)-1]; !strings.Contains(last.Content, "done") {
		t.Errorf("turn should finish normally after the re-send, got %q", last.Content)
	}
	var req struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(lastBody.Load().(string)), &req); err != nil {
		t.Fatal(err)
	}
	var sawTool bool
	for _, m := range req.Messages {
		sawTool = sawTool || m.Role == "tool"
	}
	if !sawTool {
		t.Error("the re-sent request lost the turn's tool results")
	}
	if !strings.Contains(out.String(), "retrying request 1/") {
		t.Errorf("the retry should be announced in the output, got %q", out.String())
	}
}

// Data events disarm the watchdog: a slow generation that keeps streaming is
// never cancelled.
func TestStreamStall_DataDisarmsWatchdog(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"a"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond) // longer than the watchdog
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"b"},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model")
	agent.streamNoDataTimeout = 100 * time.Millisecond
	history, err := agent.Run(context.Background(), nil, "hi", io.Discard, &session.Session{}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("want 1 request, got %d", got)
	}
	if last := history[len(history)-1]; last.Content != "ab" {
		t.Errorf("got %q, want %q", last.Content, "ab")
	}
}

// A genuine request error (bad request, auth, quota) is not re-sent.
func TestStreamRetry_DoesNotResendNonTransientError(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model")
	if _, err := agent.Run(context.Background(), nil, "hi", io.Discard, &session.Session{}, nil); err == nil {
		t.Fatal("want an error")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("want 1 request, got %d", got)
	}
}

func TestIsRetryableStreamError(t *testing.T) {
	if !isRetryableStreamError(fmt.Errorf("x: %w", errStreamStalled)) {
		t.Error("a stalled stream must be retryable")
	}
	if isRetryableStreamError(context.Canceled) {
		t.Error("a user cancel must not be retryable")
	}
}
