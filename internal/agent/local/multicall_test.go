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

	"github.com/scoutme/milk/internal/session"
)

func callNames(calls []toolCall) []string {
	var n []string
	for _, c := range calls {
		n = append(n, c.Function.Name)
	}
	return n
}

// runStream feeds a whole response to a fresh detector the way scanSSE does and
// returns the visible text plus the extracted calls.
func runStream(t *testing.T, resp string) (string, []toolCall) {
	t.Helper()
	d := NewStreamDetector(ToolFormatUnknown)
	var visible strings.Builder
	for _, ch := range resp {
		fl, _ := d.Feed(string(ch))
		visible.Write(fl)
	}
	if !d.InBlock() && d.RawBlock() == "" {
		return visible.String(), nil
	}
	return visible.String(), d.Extract()
}

// Qwen2.5-Coder lists several calls in one fence, one JSON object per line.
func TestExtract_FencedJSONLines(t *testing.T) {
	_, calls := runStream(t, "Plan:\n```json\n"+
		`{"name": "bash", "arguments": {"command": "git init"}}`+"\n"+
		`{"name": "bash", "arguments": {"command": "mkdir src"}}`+"\n"+
		`{"name": "write_file", "arguments": {"path": "a.txt", "content": "x"}}`+"\n```\n")
	if got := callNames(calls); strings.Join(got, ",") != "bash,bash,write_file" {
		t.Fatalf("got %v, want all three calls", got)
	}
	if !strings.Contains(calls[1].Function.Arguments, "mkdir src") {
		t.Errorf("arguments mixed up: %q", calls[1].Function.Arguments)
	}
}

// The model interleaves prose and several fences; every fence's calls count.
func TestExtract_SeveralFencesWithProse(t *testing.T) {
	resp := "### 1. Init\n```json\n" + `{"name": "bash", "arguments": {"command": "git init"}}` + "\n```\n" +
		"### 2. Files\nSome text.\n```json\n" +
		`{"name": "write_file", "arguments": {"path": "a", "content": "b"}}` + "\n" +
		`{"name": "bash", "arguments": {"command": "ls"}}` + "\n```\n### 3. Done\n"
	_, calls := runStream(t, resp)
	if got := strings.Join(callNames(calls), ","); got != "bash,write_file,bash" {
		t.Fatalf("got %q, want bash,write_file,bash", got)
	}
}

// Parallel <tool_call> blocks in one response.
func TestExtract_SeveralToolCallTags(t *testing.T) {
	resp := `<tool_call>{"name":"read_file","arguments":{"path":"a"}}</tool_call>` + "\n" +
		`<tool_call>{"name":"read_file","arguments":{"path":"b"}}</tool_call>`
	_, calls := runStream(t, resp)
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2: %v", len(calls), callNames(calls))
	}
}

// Models write code into a string with real newlines instead of \n escapes.
func TestParse_RawNewlinesInsideStrings(t *testing.T) {
	raw := "{\"name\": \"write_file\", \"arguments\": {\"path\": \"s.js\", \"content\": \"const a = 1;\n\tconst b = 2;\n\"}}"
	calls := extractToolCalls("```json\n" + raw + "\n```")
	if len(calls) != 1 {
		t.Fatalf("got %d calls", len(calls))
	}
	if !strings.Contains(calls[0].Function.Arguments, `const a = 1;\n\tconst b = 2;\n`) {
		t.Errorf("content not preserved: %q", calls[0].Function.Arguments)
	}
}

func TestParse_JSONArrayOfCalls(t *testing.T) {
	calls := extractToolCalls("```json\n" + `[{"name":"bash","arguments":{"command":"a"}},{"name":"bash","arguments":{"command":"b"}}]` + "\n```")
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
}

// A truncated last call must not take the complete earlier ones down with it,
// nor run half a call.
func TestParse_TruncatedLastCallIsDropped(t *testing.T) {
	calls := extractToolCalls("```json\n" + `{"name":"bash","arguments":{"command":"a"}}` + "\n" + `{"name":"bash","arguments":{"command":"b`)
	if len(calls) != 1 || !strings.Contains(calls[0].Function.Arguments, `"a"`) {
		t.Fatalf("got %v", callNames(calls))
	}
}

// Code examples in an answer must keep working: a fence whose JSON is not a
// tool call yields nothing, and a non-tool value between calls is skipped.
func TestParse_NonToolJSONIgnored(t *testing.T) {
	if calls := extractToolCalls("```json\n" + `{"port": 8080, "debug": true}` + "\n```"); len(calls) != 0 {
		t.Errorf("config example parsed as a call: %v", callNames(calls))
	}
	calls := extractToolCalls("```json\n" + `{"x":1}` + "\n" + `{"name":"bash","arguments":{"command":"a"}}` + "\n```")
	if len(calls) != 1 {
		t.Errorf("got %d calls, want 1", len(calls))
	}
}

// End to end, shaped like the reported Qwen2.5-Coder answer: a plan with
// prose, then fenced blocks holding one call per line, streamed in small
// chunks. Every call must run and the turn must continue with their results.
func TestRun_QwenStylePlanWithFencedCallLines(t *testing.T) {
	plan := "### 1. Setup\n- init the project\n\n```json\n" +
		`{"name": "create_task", "arguments": {"title": "one"}}` + "\n" +
		`{"name": "create_task", "arguments": {"title": "two"}}` + "\n```\n\n" +
		"### 2. UI\n- a simple layout\n\n```json\n" +
		`{"name": "create_task", "arguments": {"title": "three"}}` + "\n```\n\n### 3. Finish\n- docs\n"

	var requests int32
	var secondBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			for i := 0; i < len(plan); i += 3 {
				end := min(i+3, len(plan))
				chunk, _ := json.Marshal(plan[i:end])
				fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":%s}}]}`+"\n\n", chunk)
			}
			fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		} else {
			secondBody.Store(string(b))
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"all done"},"finish_reason":"stop"}]}`+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	agent := New(srv.URL, "qwen2.5-coder").WithMemConfig(MemConfig{MaxToolIterations: 5})
	var out strings.Builder
	history, err := agent.Run(context.Background(), nil, "plan it", &out, &session.Session{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if last := history[len(history)-1]; !strings.Contains(last.Content, "all done") {
		t.Fatalf("the turn should continue after the calls, ended with %q", last.Content)
	}
	body, _ := secondBody.Load().(string)
	if body == "" {
		t.Fatal("no follow-up request: the calls were not recognised")
	}
	var req struct {
		Messages []struct {
			Role      string `json:"role"`
			ToolCalls []any  `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	tools, calls := 0, 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			tools++
		}
		calls += len(m.ToolCalls)
	}
	if tools != 3 || calls != 3 {
		t.Errorf("want 3 calls and 3 results across both fences, got %d calls and %d results", calls, tools)
	}
	if strings.Contains(out.String(), `"name": "create_task"`) {
		t.Errorf("tool-call JSON leaked into the visible output:\n%s", out.String())
	}
}
