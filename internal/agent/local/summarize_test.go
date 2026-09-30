package local

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSummarize_ReturnsContentAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"  a concise summary  "}}],"usage":{"prompt_tokens":42,"completion_tokens":7}}`)
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model")
	summary, usage, err := agent.Summarize(context.Background(), "a bunch of old conversation turns")
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary != "a concise summary" {
		t.Errorf("expected trimmed summary content, got %q", summary)
	}
	if usage.Prompt != 42 || usage.Completion != 7 {
		t.Errorf("expected usage {42,7}, got %+v", usage)
	}
}

func TestSummarize_NoChoicesReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	agent := New(srv.URL, "test-model")
	_, _, err := agent.Summarize(context.Background(), "text")
	if err == nil {
		t.Error("expected an error when the response has no choices")
	}
}

func TestSummarize_UnreachableServerReturnsError(t *testing.T) {
	// Bind a listener and close it immediately: the OS refuses the
	// connection right away instead of the slow retry/timeout behavior an
	// arbitrary unused port can trigger, keeping this test fast.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	agent := New("http://"+addr, "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err = agent.Summarize(ctx, "text")
	if err == nil {
		t.Error("expected an error for an unreachable inference server")
	}
}

func TestSummarize_BedrockUnsupported(t *testing.T) {
	agent := New("http://unused", "test-model")
	agent.useBedrockNative = true
	_, _, err := agent.Summarize(context.Background(), "text")
	if err != ErrCompactionUnsupported {
		t.Errorf("expected ErrCompactionUnsupported, got %v", err)
	}
}

func TestSummarize_ResponsesAPIUnsupported(t *testing.T) {
	agent := New("http://unused", "test-model")
	agent.useResponsesAPI = true
	_, _, err := agent.Summarize(context.Background(), "text")
	if err != ErrCompactionUnsupported {
		t.Errorf("expected ErrCompactionUnsupported, got %v", err)
	}
}
