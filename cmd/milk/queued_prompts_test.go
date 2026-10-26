package main

import (
	"context"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/oversight"
	"github.com/scoutme/milk/internal/session"
)

// busyQueueModel returns a busy model with the queue knob set as asked.
func busyQueueModel(t *testing.T, queueWhileBusy bool) model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sess, err := session.New("/repo", "")
	if err != nil {
		t.Fatal(err)
	}
	knob := queueWhileBusy
	st := &interactiveState{sess: sess, cwd: "/repo", notifier: oversight.Noop{}, cfg: config.Config{QueueWhileBusy: &knob}}
	mgr := local.NewManager(context.Background(), 3)
	escAgent := local.New("http://127.0.0.1:1", "esc-model")
	m := newModel(context.Background(), st, nil, dispatchAgents{backgroundMgr: mgr, escalationLocal: escAgent}, nil)
	m.busy = true
	return m
}

// TestQueuePrompt_CapturesAndClears: queueing takes the text, the paste
// provenance and the staged attachments, clears the input, and leaves a
// receipt line naming the /queue review surface.
func TestQueuePrompt_CapturesAndClears(t *testing.T) {
	m := busyQueueModel(t, true)
	m.leadingPasted = true
	m.pendingAttachments = []PendingAttachment{{Name: "img.png", MIMEType: "image/png"}}
	m.ta.SetValue("fix the retry logic")

	m.queuePrompt(strings.TrimSpace(m.ta.Value()))
	m.ta.Reset()

	if len(m.st.queuedPrompts) != 1 {
		t.Fatalf("expected 1 queued prompt, got %d", len(m.st.queuedPrompts))
	}
	q := m.st.queuedPrompts[0]
	if q.text != "fix the retry logic" {
		t.Errorf("queued text = %q", q.text)
	}
	if !q.leadingPasted {
		t.Errorf("paste provenance not captured")
	}
	if len(q.attachments) != 1 {
		t.Errorf("attachments not captured: %#v", q.attachments)
	}
	if len(m.pendingAttachments) != 0 {
		t.Errorf("staged attachments not consumed at queue time")
	}
}

// TestQueuePrompt_ReceiptLine: the receipt proves the input was not dropped
// and shows the preview, the line count and the attachment count.
func TestQueuePrompt_ReceiptLine(t *testing.T) {
	m := busyQueueModel(t, true)
	m.queuePrompt("first line\nsecond line")

	if !strings.Contains(m.transcript.String(), "queued for next turn") {
		t.Errorf("missing receipt line:\n%s", m.transcript.String())
	}
	if !strings.Contains(m.transcript.String(), "/queue to review") {
		t.Errorf("receipt does not name /queue:\n%s", m.transcript.String())
	}
	if !strings.Contains(m.transcript.String(), "⋯ 2 lines") {
		t.Errorf("receipt missing the line-count variant:\n%s", m.transcript.String())
	}
}

// TestQueuePrompt_Cap: the 21st prompt is refused with a hint and the typed
// text stays in the input box.
func TestQueuePrompt_Cap(t *testing.T) {
	m := busyQueueModel(t, true)
	for i := 0; i < queuePromptCap; i++ {
		m.queuePrompt("filler")
	}
	m.ta.SetValue("one too many")
	m.queuePrompt(strings.TrimSpace(m.ta.Value()))

	if len(m.st.queuedPrompts) != queuePromptCap {
		t.Fatalf("queue grew past the cap: %d", len(m.st.queuedPrompts))
	}
	if !strings.Contains(m.busyHint, "queue is full") {
		t.Errorf("expected the full-queue hint, got %q", m.busyHint)
	}
	if m.ta.Value() != "one too many" {
		t.Errorf("refused prompt lost its text: %q", m.ta.Value())
	}
}

// TestDrainQueuedPrompt_FIFOOnePerTurn: two queued prompts flush in order,
// one per drain (i.e. one per turn).
func TestDrainQueuedPrompt_FIFOOnePerTurn(t *testing.T) {
	m := busyQueueModel(t, true)
	m.queuePrompt("first")
	m.queuePrompt("second")

	next, _, ok := m.drainQueuedPrompt()
	if !ok {
		t.Fatal("expected a drain for the first queued prompt")
	}
	out := next.(model)
	if len(out.st.queuedPrompts) != 1 || out.st.queuedPrompts[0].text != "second" {
		t.Fatalf("expected FIFO order, queue now %#v", out.st.queuedPrompts)
	}

	next2, _, ok2 := out.drainQueuedPrompt()
	if !ok2 {
		t.Fatal("expected a drain for the second queued prompt")
	}
	out2 := next2.(model)
	if len(out2.st.queuedPrompts) != 0 {
		t.Fatalf("expected an empty queue after two drains, got %#v", out2.st.queuedPrompts)
	}
	if _, _, ok3 := out2.drainQueuedPrompt(); ok3 {
		t.Error("drain must no-op on an empty queue")
	}
}

// TestDrainQueuedPrompt_ClearsFollowupFlagsAndReplaysProvenance: the flushed
// turn is the next turn — follow-up flags are cleared so finished job
// results ride along inside it, and the entry's paste provenance and
// attachments are replayed.
func TestDrainQueuedPrompt_ClearsFollowupFlagsAndReplaysProvenance(t *testing.T) {
	m := busyQueueModel(t, true)
	m.leadingPasted = true
	m.pendingAttachments = []PendingAttachment{{Name: "notes.md", MIMEType: "text/markdown"}}
	m.queuePrompt("continue here")
	m.pendingBackgroundFollowup = true
	m.pendingUserBackgroundFollowup = true

	next, cmd, ok := m.drainQueuedPrompt()
	if !ok {
		t.Fatal("expected a drain")
	}
	out := next.(model)
	if out.pendingBackgroundFollowup || out.pendingUserBackgroundFollowup {
		t.Errorf("follow-up flags not cleared at flush: %v %v",
			out.pendingBackgroundFollowup, out.pendingUserBackgroundFollowup)
	}
	// Provenance and attachments are consumed *into* the flushed turn
	// (submitInput snapshots leadingPasted; the dispatch takes the staged
	// attachments) — what the entry owned is pinned by
	// TestQueuePrompt_CapturesAndClears.
	if out.leadingPasted {
		t.Errorf("paste provenance left set after the flush consumed it")
	}
	if len(out.pendingAttachments) != 0 {
		t.Errorf("attachments left staged after the flush consumed them: %#v", out.pendingAttachments)
	}
	if cmd == nil {
		t.Error("expected the flush to start a turn")
	}
}

// TestQueuePrompt_Knob: "queue_while_busy": false disables the queue path
// (handleBusyKey keeps the hint — pinned in completion_while_busy_test),
// and the knob is read live from the config.
func TestQueuePrompt_Knob(t *testing.T) {
	m := busyQueueModel(t, false)
	if m.st.cfg.QueueWhileBusyEnabled() {
		t.Error("expected the knob to read false")
	}
	m2 := busyQueueModel(t, true)
	if !m2.st.cfg.QueueWhileBusyEnabled() {
		t.Error("expected the knob to read true")
	}
	var unset config.Config
	if !unset.QueueWhileBusyEnabled() {
		t.Error("unset knob must default to true")
	}
}

// TestQueueDroppedOnSessionSwitch: queued prompts do not leak across a
// session switch (like staged attachments) and say so.
func TestQueueDroppedOnSessionSwitch(t *testing.T) {
	m := busyQueueModel(t, true)
	m.queuePrompt("for this conversation only")
	m.queuePrompt("also for this one")

	next, _ := m.handleSlashInput(cmdNew, "")
	out := next.(model)
	if len(out.st.queuedPrompts) != 0 {
		t.Errorf("queued prompts survived a session switch: %#v", out.st.queuedPrompts)
	}
	if !strings.Contains(out.transcript.String(), "dropped 2 queued prompt(s)") {
		t.Errorf("missing the drop notice:\n%s", out.transcript.String())
	}
}

// TestHandleQueueCmd_Review: /queue list|show|drop|clear round-trips the
// queue, and /queue is busy-safe.
func TestHandleQueueCmd_Review(t *testing.T) {
	m := busyQueueModel(t, true)
	if !busySafeCommands[cmdQueue] {
		t.Fatal("/queue must be busy-safe")
	}
	m.queuePrompt("alpha")
	m.queuePrompt("beta")

	m = m.handleQueueCmd("")
	if !strings.Contains(m.transcript.String(), "2 queued prompt(s)") {
		t.Errorf("list does not show the count:\n%s", m.transcript.String())
	}

	m = m.handleQueueCmd("show 2")
	if !strings.Contains(m.transcript.String(), "beta") {
		t.Errorf("show does not print the entry:\n%s", m.transcript.String())
	}

	m = m.handleQueueCmd("drop last")
	if len(m.st.queuedPrompts) != 1 || m.st.queuedPrompts[0].text != "alpha" {
		t.Errorf("drop last removed the wrong entry: %#v", m.st.queuedPrompts)
	}

	m = m.handleQueueCmd("clear")
	if len(m.st.queuedPrompts) != 0 {
		t.Errorf("clear left entries behind: %#v", m.st.queuedPrompts)
	}
}

// TestQueuePreview: the receipt preview is the first line clipped to 60
// columns.
func TestQueuePreview(t *testing.T) {
	if got := queuePreview("short"); got != "short" {
		t.Errorf("short preview mangled: %q", got)
	}
	long := strings.Repeat("x", 80)
	got := queuePreview(long)
	if len([]rune(got)) != 60 {
		t.Errorf("preview not clipped to 60 columns: %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clipped preview must end with …: %q", got)
	}
	if got := queuePreview("one\ntwo"); got != "one" {
		t.Errorf("multi-line preview must be the first line: %q", got)
	}
}
