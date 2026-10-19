package local

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/session"
)

// ADR-0068 (issue #271): a background job gets a soft deadline before its
// hard execution timeout — a wrap-up message at 80% of the timeout, delivered
// through the job's own inbox — and a job that still times out salvages its
// last assistant text alongside the tool trail.

// softDeadlineDelay pins the fraction and the floor: 80% of the timeout, and
// nothing at all for jobs under the two-minute floor.
func TestSoftDeadlineDelay(t *testing.T) {
	cases := []struct {
		timeout, min time.Duration
		wantOK       bool
		want         time.Duration
	}{
		{20 * time.Minute, softDeadlineMinTimeout, true, 16 * time.Minute},
		{2 * time.Minute, softDeadlineMinTimeout, true, 96 * time.Second},
		{119 * time.Second, softDeadlineMinTimeout, false, 0},
		{5 * time.Second, softDeadlineMinTimeout, false, 0},
		{5 * time.Second, time.Second, true, 4 * time.Second}, // test-seam override
		{5 * time.Second, 0, true, 4 * time.Second},           // floor disabled entirely
	}
	for _, c := range cases {
		got, ok := softDeadlineDelay(c.timeout, c.min)
		if ok != c.wantOK || got != c.want {
			t.Errorf("softDeadlineDelay(%v, %v) = %v, %v; want %v, %v", c.timeout, c.min, got, ok, c.want, c.wantOK)
		}
	}
}

// wrapUpMessage names the time left, says to stop exploring and write the
// final answer, and carries the partial-result convention.
func TestWrapUpMessage(t *testing.T) {
	msg := wrapUpMessage(4 * time.Minute)
	for _, want := range []string{"4 minute", "stop exploring", "write your final answer now", `<result status="partial"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("wrap-up message missing %q: %q", want, msg)
		}
	}
	// A sub-minute remainder still reads as at least one minute — never
	// "about 0 minutes left".
	if !strings.Contains(wrapUpMessage(10*time.Second), "1 minute") {
		t.Errorf("sub-minute remainder must round up to 1 minute: %q", wrapUpMessage(10*time.Second))
	}
}

// A job that runs past 80% of its timeout receives exactly one wrap-up
// message, at roughly the 80% mark, while it is still running.
func TestSoftDeadline_WrapUpAtEightyPercent(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	mgr.SetJobTimeout(400 * time.Millisecond)
	mgr.SetSoftDeadlineMin(0) // exercise the soft deadline without the real 2-minute floor

	type arrival struct {
		msg string
		at  time.Duration
	}
	got := make(chan arrival, 4)
	release := make(chan struct{})
	start := time.Now()
	job := mgr.Spawn("slow", "task", "primary", "m", func(ctx context.Context, jobID string, _ io.Writer) (string, session.TokenUsage, error) {
		inbox := mgr.Inbox(jobID)
		for {
			select {
			case msg := <-inbox:
				got <- arrival{msg, time.Since(start)}
			case <-release:
				return "done", session.TokenUsage{}, nil
			}
		}
	})

	var first arrival
	select {
	case first = <-got:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("no wrap-up message arrived before the deadline")
	}
	close(release)
	if _, err := mgr.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// 80% of 400ms is 320ms — allow generous slack upward on a loaded CI
	// box, but the message must not fire long before its mark.
	if first.at < 250*time.Millisecond {
		t.Errorf("wrap-up arrived after %v, want ~80%% of 400ms (>= 250ms)", first.at)
	}
	for _, want := range []string{"time check", "final answer", `status="partial"`} {
		if !strings.Contains(first.msg, want) {
			t.Errorf("wrap-up message missing %q: %q", want, first.msg)
		}
	}

	// Exactly one: nothing else may arrive after the job ended.
	time.Sleep(150 * time.Millisecond)
	select {
	case extra := <-got:
		t.Errorf("second wrap-up message: %q", extra.msg)
	default:
	}
}

// A job whose timeout is under the floor gets no wrap-up message at all —
// nagging to wrap up with seconds left is noise.
func TestSoftDeadline_ShortTimeoutSkipsWrapUp(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	mgr.SetJobTimeout(300 * time.Millisecond) // under the default 2-minute floor

	got := make(chan string, 4)
	release := make(chan struct{})
	job := mgr.Spawn("quick", "task", "primary", "m", func(ctx context.Context, jobID string, _ io.Writer) (string, session.TokenUsage, error) {
		inbox := mgr.Inbox(jobID)
		for {
			select {
			case msg := <-inbox:
				got <- msg
			case <-release:
				return "done", session.TokenUsage{}, nil
			}
		}
	})

	time.Sleep(500 * time.Millisecond) // past any would-be 80% mark
	close(release)
	if _, err := mgr.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case msg := <-got:
		t.Errorf("sub-2-minute job must get no wrap-up message, got %q", msg)
	default:
	}
}

// A job that finishes before the soft deadline never sees a wrap-up message —
// the timer is stopped when the job body returns.
func TestSoftDeadline_NoWrapUpAfterFinish(t *testing.T) {
	mgr := NewManager(context.Background(), 1)
	mgr.SetJobTimeout(400 * time.Millisecond)
	mgr.SetSoftDeadlineMin(0)

	job := mgr.Spawn("fast", "task", "primary", "m", func(ctx context.Context, _ string, _ io.Writer) (string, session.TokenUsage, error) {
		return "done", session.TokenUsage{}, nil
	})
	if _, err := mgr.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	time.Sleep(500 * time.Millisecond) // past the would-be soft deadline at 320ms
	select {
	case msg := <-mgr.Inbox(job.ID):
		t.Errorf("wrap-up message after the job finished: %q", msg)
	default:
	}
}

// Salvage (ADR-0068): a job that ends in an error keeps its last assistant
// text — the conclusions it had already formed — *alongside* the tool trail,
// instead of one half or the other (issue #271 lost both ways).
func TestBackgroundPartialResult_IncludesLastAssistantTextAndTrail(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "I checked A and B; A is the culprit."},
		{Role: "assistant", ToolCalls: []toolCall{{ID: "c1", Function: toolCallFunction{Name: "bash", Arguments: `{"command":"grep x"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "x:42"},
	}
	got := backgroundPartialResult(msgs)
	if !strings.Contains(got, "I checked A and B; A is the culprit.") {
		t.Errorf("last assistant text missing from the salvage: %q", got)
	}
	if !strings.Contains(got, noFinalSummaryTrailHeader) {
		t.Errorf("tool trail missing from the salvage: %q", got)
	}
	if strings.Index(got, "I checked A and B") > strings.Index(got, noFinalSummaryTrailHeader) {
		t.Errorf("the conclusions must lead the trail: %q", got)
	}

	// Issue #271's evidence shape: no assistant text at all — the trail is
	// still returned, unchanged.
	trail := backgroundPartialResult([]Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", ToolCalls: []toolCall{{ID: "c1", Function: toolCallFunction{Name: "bash", Arguments: `{"command":"grep x"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "binary file matches"},
	})
	if !strings.Contains(trail, noFinalSummaryTrailHeader) || strings.Contains(trail, "culprit") {
		t.Errorf("trail-only salvage = %q", trail)
	}

	// No tool activity at all: the text alone, no empty trail header.
	onlyText := backgroundPartialResult([]Message{{Role: "assistant", Content: "done here"}})
	if onlyText != "done here" {
		t.Errorf("text-only salvage = %q", onlyText)
	}

	// Nothing at all: empty.
	if got := backgroundPartialResult(nil); got != "" {
		t.Errorf("empty salvage = %q", got)
	}
}

func TestLastAssistantText(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "task"},
		{Role: "assistant", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "assistant", ToolCalls: []toolCall{{ID: "c1", Function: toolCallFunction{Name: "bash", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "out"},
	}
	if got := lastAssistantText(msgs); got != "second" {
		t.Errorf("lastAssistantText = %q, want %q", got, "second")
	}
	if got := lastAssistantText([]Message{{Role: "user", Content: "hi"}}); got != "" {
		t.Errorf("lastAssistantText with no assistant text = %q", got)
	}
}
