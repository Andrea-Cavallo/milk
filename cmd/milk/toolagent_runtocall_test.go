package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/memory"
)

// TestLocalRunner_RunToolCall_NoPanicOrFalseEscalation is a regression test
// for two bugs found together while wiring the agent-as-tool bridge into the
// escalation path:
//
//  1. RunToolCall used to pass the current prompt as its own "history", so
//     Run's repeated-prompt check saw an exact self-match (score 1.0 >= the
//     0.9 threshold) and force-escalated on every single call, regardless of
//     actual repetition.
//  2. RunToolCall passed a nil *session.Session. That was masked by bug #1
//     (Run returned before ever touching sess), so fixing #1 alone exposed a
//     nil pointer dereference on sess.CWD inside Run.
//
// A tool-agent call for a longish, non-repeated prompt should complete
// normally: no panic, no error, and no spurious escalation reply.
func TestLocalRunner_RunToolCall_NoPanicOrFalseEscalation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "the viewport shows an empty scene"}, "finish_reason": "stop"},
			},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	defer srv.Close()

	la := local.NewFromConfig(config.AgentConfig{
		Name:  "test-tool-agent",
		URL:   srv.URL,
		Model: "test-model",
	}).WithToolAgentRole()

	r := newLocalRunner(la, "test-tool-agent")

	result, err := r.RunToolCall(context.Background(), config.Config{},
		"get a blender viewport screenshot and describe what you see", nil, io.Discard)
	if err != nil {
		t.Fatalf("RunToolCall returned error: %v", err)
	}
	if result != "the viewport shows an empty scene" {
		t.Errorf("want the model's actual reply, got a spurious result: %q", result)
	}
}

// TestLocalRunner_RunToolCall_MemoryToolsWired pins the tool-agent half of
// issue #172: a tool-agent (agent_<name>) invoked with a memory-store handle
// gets the four memory tools and their mandate in its system prompt — the
// incident was an agent whose prompt mandated forget_memory while the tool
// list lacked it, so it improvised against ~/.milk/memory/*.json. Without a
// store, neither the tools nor the mandate may appear.
func TestLocalRunner_RunToolCall_MemoryToolsWired(t *testing.T) {
	for _, tc := range []struct {
		name    string
		withMem bool
	}{
		{"with memory store", true},
		{"without memory store", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				body = b
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				chunk := map[string]any{
					"choices": []map[string]any{
						{"delta": map[string]any{"content": "ok"}, "finish_reason": "stop"},
					},
				}
				cb, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", cb)
			}))
			defer srv.Close()

			la := local.NewFromConfig(config.AgentConfig{
				Name:  "test-tool-agent",
				URL:   srv.URL,
				Model: "test-model",
			}).WithToolAgentRole()

			r := newLocalRunner(la, "test-tool-agent")
			if tc.withMem {
				mem, err := memory.NewStore(t.TempDir(), "")
				if err != nil {
					t.Fatalf("NewStore: %v", err)
				}
				r.mem = mem
			}

			if _, err := r.RunToolCall(context.Background(), config.Config{},
				"remember that the user prefers dark mode", nil, io.Discard); err != nil {
				t.Fatalf("RunToolCall returned error: %v", err)
			}

			mu.Lock()
			raw := string(body)
			mu.Unlock()

			for _, tool := range []string{"record_memory", "get_memory", "list_memory", "forget_memory"} {
				present := strings.Contains(raw, `"`+tool+`"`)
				if present != tc.withMem {
					t.Errorf("tool %q present=%v, want %v", tool, present, tc.withMem)
				}
			}
			mandate := strings.Contains(raw, "MANDATORY — memory tool actions")
			if mandate != tc.withMem {
				t.Errorf("memory-tool mandate present=%v, want %v", mandate, tc.withMem)
			}
		})
	}
}
