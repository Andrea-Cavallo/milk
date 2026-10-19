package local

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/memory"
)

// Issue #249 contract: ask_user is defined in the schema set and routed to
// the host's question callbacks — the foreground race for ordinary turns,
// the bounded background ask for unattended contexts.

func askUserToolCall(args map[string]any) toolCall {
	b, _ := json.Marshal(args)
	return toolCall{ID: "call_1", Function: toolCallFunction{Name: "ask_user", Arguments: string(b)}}
}

func TestAskUserInSchema(t *testing.T) {
	tools := schemas(&memory.Store{}, t.TempDir(), nil, nil, &mockTaskStore{}, nil)
	found := false
	for _, s := range tools {
		if toolName(s) == "ask_user" {
			found = true
		}
	}
	if !found {
		t.Fatal("ask_user missing from schemas()")
	}
}

func TestAskUserUnwiredFails(t *testing.T) {
	a := &Agent{}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{"question": "blue or green?"}))
	if !strings.Contains(out.msg.Content, "ask_user is only available in interactive sessions") {
		t.Fatalf("want unavailable error, got %s", out.msg.Content)
	}
}

func TestAskUserForegroundRoutesToUserAsk(t *testing.T) {
	var gotQ string
	var gotOpts []string
	a := &Agent{userAsk: func(_ context.Context, q string, opts []string) (string, bool) {
		gotQ, gotOpts = q, opts
		return "blue", true
	}}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{
		"question": "blue or green?",
		"options":  []string{"blue", "green"},
	}))
	if gotQ != "blue or green?" {
		t.Fatalf("question = %q", gotQ)
	}
	if len(gotOpts) != 2 {
		t.Fatalf("options = %v", gotOpts)
	}
	if !strings.Contains(out.msg.Content, "blue") {
		t.Fatalf("answer missing from result: %s", out.msg.Content)
	}
}

func TestAskUserBackgroundRoutesToBgAsk(t *testing.T) {
	var gotJob string
	a := &Agent{jobID: "job-7", bgUserAsk: func(_ context.Context, jobID, _ string, _ []string) (string, bool) {
		gotJob = jobID
		return "", false // nobody answered
	}}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{"question": "ship it?"}))
	if gotJob != "job-7" {
		t.Fatalf("jobID = %q", gotJob)
	}
	if !strings.Contains(out.msg.Content, "Nobody answered") {
		t.Fatalf("want no-answer guidance, got %s", out.msg.Content)
	}
	if strings.Contains(out.msg.Content, `"error"`) && strings.Contains(out.msg.Content, "Nobody answered") {
		t.Fatalf("no-answer must not be an error result: %s", out.msg.Content)
	}
}

func TestAskUserWorkflowRoleUsesBgAsk(t *testing.T) {
	called := false
	a := &Agent{workflowRole: true, bgUserAsk: func(_ context.Context, _, _ string, _ []string) (string, bool) {
		called = true
		return "yes", true
	}}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{"question": "q"}))
	if !called {
		t.Fatal("workflow step must use the bounded background ask")
	}
	if !strings.Contains(out.msg.Content, "yes") {
		t.Fatalf("got %s", out.msg.Content)
	}
}

func TestAskUserUnattendedWithoutBgAskFails(t *testing.T) {
	a := &Agent{jobID: "job-1", userAsk: func(_ context.Context, _ string, _ []string) (string, bool) {
		t.Fatal("foreground ask must not serve unattended contexts")
		return "", false
	}}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{"question": "q"}))
	if !strings.Contains(out.msg.Content, "ask_user is not available in this context") {
		t.Fatalf("want unavailable error, got %s", out.msg.Content)
	}
}

func TestAskUserEmptyQuestionFails(t *testing.T) {
	a := &Agent{userAsk: func(_ context.Context, _ string, _ []string) (string, bool) {
		t.Fatal("empty question must not reach the surface")
		return "", false
	}}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{"question": "   "}))
	if !strings.Contains(out.msg.Content, "question must not be empty") {
		t.Fatalf("got %s", out.msg.Content)
	}
}

func TestAskUserCancelledFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Agent{userAsk: func(c context.Context, _ string, _ []string) (string, bool) {
		<-c.Done()
		return "", false
	}}
	out := a.runAskUser(ctx, askUserToolCall(map[string]any{"question": "q"}))
	if !strings.Contains(out.msg.Content, "ask_user cancelled") {
		t.Fatalf("got %s", out.msg.Content)
	}
}

func TestAskUserNoAnswerMentionsOptions(t *testing.T) {
	a := &Agent{userAsk: func(_ context.Context, _ string, _ []string) (string, bool) { return "", false }}
	out := a.runAskUser(context.Background(), askUserToolCall(map[string]any{
		"question": "q", "options": []string{"a1", "b2"},
	}))
	if !strings.Contains(out.msg.Content, "a1 / b2") {
		t.Fatalf("no-answer should restate the options: %s", out.msg.Content)
	}
}
