package local

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/session"
)

// Issue #250 contract: the rest of the spawn_background_agent lifecycle —
// status_background_agent (check), wait_background_agent (block on the
// result), send_background_agent (follow-up input to a running job).

func bgToolCall(name string, args map[string]any) toolCall {
	b, _ := json.Marshal(args)
	return toolCall{ID: "call_1", Function: toolCallFunction{Name: name, Arguments: string(b)}}
}

func dispatchBG(t *testing.T, a *Agent, tc toolCall) string {
	t.Helper()
	out := a.dispatchOneTool(context.Background(), tc, 0, "", "", io.Discard, &session.Session{}, nil, nil)
	return out.msg.Content
}

func TestSendDeliversFollowupToRunningJob(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	got := make(chan string, 1)
	mgr.Spawn("t", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		select {
		case msg := <-mgr.Inbox(jobID):
			got <- msg
		case <-time.After(2 * time.Second):
		}
		return "done", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}

	if err := mgr.Send(id, "the failing test is TestX"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "the failing test is TestX" {
			t.Fatalf("delivered %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up never reached the job")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	job, err := mgr.Wait(ctx, id)
	if err != nil || job.Status != JobCompleted || job.Result != "done" {
		t.Fatalf("Wait: %+v err=%v", job, err)
	}
	if err := mgr.Send(id, "late"); !errors.Is(err, ErrJobFinished) {
		t.Fatalf("Send after finish = %v, want ErrJobFinished", err)
	}
}

func TestSendUnknownJob(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	if err := mgr.Send("job_999", "hi"); !errors.Is(err, ErrJobUnknown) {
		t.Fatalf("Send = %v, want ErrJobUnknown", err)
	}
	if _, err := mgr.Wait(context.Background(), "job_999"); !errors.Is(err, ErrJobUnknown) {
		t.Fatalf("Wait = %v, want ErrJobUnknown", err)
	}
}

func TestSendRefusesWhenInboxFull(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	block := make(chan struct{})
	defer close(block)
	mgr.Spawn("t", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		<-block // never consumes its inbox
		return "", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	for i := 0; i < jobInboxSize; i++ {
		if err := mgr.Send(id, "m"); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if err := mgr.Send(id, "overflow"); !errors.Is(err, ErrJobInboxFull) {
		t.Fatalf("Send = %v, want ErrJobInboxFull", err)
	}
}

func TestWaitTimeoutReturnsRunningSnapshot(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	block := make(chan struct{})
	defer close(block)
	mgr.Spawn("t", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return "", session.TokenUsage{}, ctx.Err()
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	job, err := mgr.Wait(ctx, id)
	if err != nil || job.Status != JobRunning {
		t.Fatalf("Wait: %+v err=%v — a cancelled wait is 'still running', not an error", job, err)
	}
}

func TestDrainFollowupsInjectsUserMessages(t *testing.T) {
	inbox := make(chan string, 2)
	inbox <- "first"
	inbox <- "second"
	msgs := drainFollowups([]Message{{Role: "user", Content: "task"}}, inbox)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d", len(msgs))
	}
	if !strings.Contains(msgs[1].Content, "first") || msgs[1].Role != "user" {
		t.Fatalf("msg[1] = %+v", msgs[1])
	}
	if !strings.Contains(msgs[1].Content, backgroundFollowupPrefix) {
		t.Fatalf("follow-up not marked as external input: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[2].Content, "second") {
		t.Fatalf("msg[2] = %+v", msgs[2])
	}
	// nil inbox (every non-job loop) is a no-op.
	if out := drainFollowups(msgs, nil); len(out) != 3 {
		t.Fatalf("nil inbox mutated msgs: %d", len(out))
	}
}

func TestStatusBackgroundAgentDispatch(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	block := make(chan struct{})
	defer close(block)
	mgr.Spawn("analyze stuff", "the task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return "final result", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	a := &Agent{backgroundManager: mgr}

	res := dispatchBG(t, a, bgToolCall("status_background_agent", map[string]any{}))
	if !strings.Contains(res, id) || !strings.Contains(res, "running") || !strings.Contains(res, "analyze stuff") {
		t.Fatalf("listing: %s", res)
	}

	res = dispatchBG(t, a, bgToolCall("status_background_agent", map[string]any{"job_id": id}))
	if !strings.Contains(res, "the task") || !strings.Contains(res, "running for") {
		t.Fatalf("detail: %s", res)
	}

	res = dispatchBG(t, a, bgToolCall("status_background_agent", map[string]any{"job_id": "job_999"}))
	if !strings.Contains(res, "no background agent job with that ID") {
		t.Fatalf("unknown id: %s", res)
	}
}

func TestWaitBackgroundAgentDispatch(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	release := make(chan struct{})
	defer close(release)
	mgr.Spawn("label", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return "", session.TokenUsage{}, ctx.Err()
		}
		return "the answer", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	a := &Agent{backgroundManager: mgr}

	res := dispatchBG(t, a, bgToolCall("wait_background_agent", map[string]any{"job_id": id, "timeout_secs": 0.05}))
	if !strings.Contains(res, "still running after") {
		t.Fatalf("timeout wait: %s", res)
	}

	// Finish the job, then wait again: the result comes back.
	// (The run unblocks on the deferred close(release) only at test end, so
	// cancel the job instead and check the failure path.)
	mgr.Cancel(id)
	res = dispatchBG(t, a, bgToolCall("wait_background_agent", map[string]any{"job_id": id, "timeout_secs": 2}))
	if !strings.Contains(res, "failed") {
		t.Fatalf("failed wait: %s", res)
	}

	res = dispatchBG(t, a, bgToolCall("wait_background_agent", map[string]any{"job_id": "job_999"}))
	if !strings.Contains(res, "no background agent job with that ID") {
		t.Fatalf("unknown id: %s", res)
	}
}

func TestWaitBackgroundAgentReturnsResult(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	mgr.Spawn("label", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		return "the full answer", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	a := &Agent{backgroundManager: mgr}
	res := dispatchBG(t, a, bgToolCall("wait_background_agent", map[string]any{"job_id": id, "timeout_secs": 2}))
	if !strings.Contains(res, "the full answer") {
		t.Fatalf("wait result: %s", res)
	}
}

func TestSendBackgroundAgentDispatch(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	block := make(chan struct{})
	defer close(block)
	mgr.Spawn("label", "task", "primary", "m", func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		<-block
		return "", session.TokenUsage{}, nil
	})
	var id string
	for _, j := range mgr.Jobs() {
		id = j.ID
	}
	a := &Agent{backgroundManager: mgr}

	res := dispatchBG(t, a, bgToolCall("send_background_agent", map[string]any{"job_id": id, "message": "use the new spec"}))
	if !strings.Contains(res, "Delivered your input") {
		t.Fatalf("send: %s", res)
	}
	if err := mgr.Send(id, "x"); err != nil {
		t.Fatalf("queued message lost: %v", err)
	}

	res = dispatchBG(t, a, bgToolCall("send_background_agent", map[string]any{"job_id": id, "message": "  "}))
	if !strings.Contains(res, "message must not be empty") {
		t.Fatalf("empty message: %s", res)
	}
	res = dispatchBG(t, a, bgToolCall("send_background_agent", map[string]any{"job_id": "job_999", "message": "x"}))
	if !strings.Contains(res, "no background agent job with that ID") {
		t.Fatalf("unknown id: %s", res)
	}
}

// The three tools are schema-gated exactly like spawn/cancel: absent from a
// background job's own tool list (no Manager set there).
func TestBackgroundLifecycleToolsGated(t *testing.T) {
	a := &Agent{}
	for _, name := range []string{"status_background_agent", "wait_background_agent", "send_background_agent"} {
		out := a.dispatchOneTool(context.Background(), bgToolCall(name, map[string]any{"job_id": "job_1"}), 0, "", "", io.Discard, &session.Session{}, nil, nil)
		if !isUnknownToolResult(out.msg.Content) {
			t.Errorf("%s must be unknown without a background manager: %s", name, out.msg.Content)
		}
	}
}
