package main

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/session"
	"github.com/scoutme/milk/internal/workflow"
)

// attachTestModel is dragTestModel plus a non-nil session, needed by any test
// that actually triggers an attach: startAttach reaches viewportHeight() →
// headerBar(), which dereferences m.st.sess — dragTestModel alone leaves that
// nil, fine for the panel-selection tests it was written for, not for these.
func attachTestModel() *model {
	m := dragTestModel()
	m.st.sess = &session.Session{}
	return m
}

// spawnJobWithLiveContent creates and waits for a completed job whose Live
// buffer already contains text, for tests that need an attach target without
// caring about the job's actual execution.
func spawnJobWithLiveContent(t *testing.T, mgr *local.Manager, label, content string) *local.Job {
	t.Helper()
	done := make(chan *local.Job, 1)
	mgr.SetOnDone(func(j *local.Job) { done <- j })
	job := mgr.Spawn(label, "t", "primary", "m", func(_ context.Context, _ string, out io.Writer) (string, session.TokenUsage, error) {
		io.WriteString(out, content) //nolint:errcheck
		return "ok", session.TokenUsage{}, nil
	})
	<-done
	return job
}

func TestHandleBackgroundPanelClick_DoubleClickAttaches(t *testing.T) {
	m := attachTestModel()
	mgr := local.NewManager(context.Background(), 1)
	m.agents.backgroundMgr = mgr
	job := spawnJobWithLiveContent(t, mgr, "investigate X", "distinctive job output")

	// buildBackgroundPanelLines: line 0 title, line 1 blank, line 2 = the only job.
	if cmd := m.handleBackgroundPanelClick(2); cmd != nil {
		t.Fatalf("first click should only arm, not return a command")
	}
	if m.attached != nil {
		t.Fatalf("expected no attach after a single click, got %+v", m.attached)
	}

	cmd := m.handleBackgroundPanelClick(2)
	if m.attached == nil {
		t.Fatal("expected attach after second click within 400ms")
	}
	if m.attached.kind != attachBackground || m.attached.jobID != job.ID {
		t.Fatalf("attached = %+v, want kind=attachBackground jobID=%s", m.attached, job.ID)
	}
	if got := m.attached.buf.Snapshot(); got != "distinctive job output" {
		t.Fatalf("attached.buf.Snapshot() = %q, want the job's live content", got)
	}
	if cmd == nil {
		t.Fatal("expected startAttach to return the refresh-tick command")
	}
}

func TestHandleBackgroundPanelClick_OutOfRangeIsNoop(t *testing.T) {
	m := dragTestModel()
	mgr := local.NewManager(context.Background(), 1)
	m.agents.backgroundMgr = mgr
	spawnJobWithLiveContent(t, mgr, "job", "content")

	for _, lineIdx := range []int{-1, 0, 1, 99} {
		if cmd := m.handleBackgroundPanelClick(lineIdx); cmd != nil {
			t.Errorf("lineIdx=%d: expected nil cmd for an out-of-range row", lineIdx)
		}
		if m.attached != nil {
			t.Fatalf("lineIdx=%d: expected no attach for an out-of-range row, got %+v", lineIdx, m.attached)
		}
	}
}

func TestHandleBackgroundPanelClick_NilManagerIsNoop(t *testing.T) {
	m := dragTestModel()
	if cmd := m.handleBackgroundPanelClick(2); cmd != nil {
		t.Error("expected nil cmd with no background manager configured")
	}
	if m.attached != nil {
		t.Fatal("expected no attach with no background manager configured")
	}
}

func TestHandleWorkflowPanelClick_DoubleClickAttaches(t *testing.T) {
	m := attachTestModel()
	m.workflowState = &workflow.State{WorkflowName: "dev", Role: "designer"}
	m.workflowState.LiveBuffer().Append([]byte("distinctive stage output"))

	if cmd := m.handleWorkflowPanelClick(); cmd != nil {
		t.Fatal("first click should only arm, not return a command")
	}
	if m.attached != nil {
		t.Fatal("expected no attach after a single click")
	}

	cmd := m.handleWorkflowPanelClick()
	if m.attached == nil {
		t.Fatal("expected attach after second click within 400ms")
	}
	if m.attached.kind != attachWorkflow {
		t.Fatalf("attached.kind = %v, want attachWorkflow", m.attached.kind)
	}
	if got := m.attached.buf.Snapshot(); got != "distinctive stage output" {
		t.Fatalf("attached.buf.Snapshot() = %q, want the workflow's live content", got)
	}
	if cmd == nil {
		t.Fatal("expected startAttach to return the refresh-tick command")
	}
}

func TestHandleWorkflowPanelClick_NilStateIsNoop(t *testing.T) {
	m := dragTestModel()
	if cmd := m.handleWorkflowPanelClick(); cmd != nil {
		t.Error("expected nil cmd with no workflow running")
	}
	if m.attached != nil {
		t.Fatal("expected no attach with no workflow running")
	}
}

func TestHandleAttachKey_EscDetaches(t *testing.T) {
	m := attachTestModel()
	m.busy = true // an unrelated field the Esc handler must not touch
	mgr := local.NewManager(context.Background(), 1)
	job := spawnJobWithLiveContent(t, mgr, "job", "content")
	m.attached = &attachState{kind: attachBackground, jobID: job.ID, buf: job.Live}

	updated, _ := m.handleAttachKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm := updated.(model)

	if mm.attached != nil {
		t.Fatalf("expected Esc to clear attached, got %+v", mm.attached)
	}
	if !mm.busy {
		t.Error("Esc-to-detach must not touch unrelated model state (m.busy)")
	}
}

func TestHandleAttachKey_OtherKeysAreSwallowed(t *testing.T) {
	m := dragTestModel()
	mgr := local.NewManager(context.Background(), 1)
	job := spawnJobWithLiveContent(t, mgr, "job", "content")
	m.attached = &attachState{kind: attachBackground, jobID: job.ID, buf: job.Live}

	updated, cmd := m.handleAttachKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	mm := updated.(model)

	if mm.attached == nil {
		t.Fatal("expected a non-Esc key to leave attach state untouched")
	}
	if cmd != nil {
		t.Error("expected no command from a swallowed key")
	}
}

func TestView_AttachedRendersLiveBufferNotMainTranscript(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	m.appendTranscript("main transcript sentinel\n")

	mgr := local.NewManager(context.Background(), 1)
	job := spawnJobWithLiveContent(t, mgr, "job", "attached buffer sentinel")
	cmd := m.startAttach(attachBackground, job.ID, "job (completed)", job.Live)
	if cmd == nil {
		t.Fatal("expected startAttach to return the refresh-tick command")
	}

	view := m.View()
	if !strings.Contains(view, "attached buffer sentinel") {
		t.Errorf("expected attach view to render the live buffer content, got:\n%s", view)
	}
	if strings.Contains(view, "main transcript sentinel") {
		t.Errorf("expected attach view to hide the main transcript, got:\n%s", view)
	}
}

func TestHandleResize_ResizesAttachedViewport(t *testing.T) {
	m := layoutTestModel(t, 100, 40)
	mgr := local.NewManager(context.Background(), 1)
	job := spawnJobWithLiveContent(t, mgr, "job", "content")
	m.startAttach(attachBackground, job.ID, "job", job.Live)

	updated, _ := m.handleResize(tea.WindowSizeMsg{Width: 160, Height: 50})
	mm := updated.(model)

	if mm.attached == nil {
		t.Fatal("expected attach state to survive a resize")
	}
	if mm.attached.vp.Width != mm.vpWidth() {
		t.Errorf("attached.vp.Width = %d, want %d (mm.vpWidth())", mm.attached.vp.Width, mm.vpWidth())
	}
	if mm.attached.vp.Height != mm.viewportHeight() {
		t.Errorf("attached.vp.Height = %d, want %d (mm.viewportHeight())", mm.attached.vp.Height, mm.viewportHeight())
	}
}

func TestHandleMouse_WheelScrollsAttachedViewportNotMain(t *testing.T) {
	m := attachTestModel()
	m.height = 10
	// Enough lines in both viewports to make wheel scrolling observable.
	var longMain strings.Builder
	for range 50 {
		longMain.WriteString("main line\n")
	}
	m.vp.SetContent(longMain.String())
	m.vp.GotoBottom()
	mainOffsetBefore := m.vp.YOffset

	mgr := local.NewManager(context.Background(), 1)
	var content strings.Builder
	for range 50 {
		content.WriteString("attach line\n")
	}
	job := spawnJobWithLiveContent(t, mgr, "job", content.String())
	m.startAttach(attachBackground, job.ID, "job", job.Live)
	m.attached.vp.GotoBottom()
	attachOffsetBefore := m.attached.vp.YOffset

	updated, _ := m.handleMouse(tea.MouseMsg(tea.MouseEvent{X: 5, Y: 5, Button: tea.MouseButtonWheelUp}))
	mm := updated.(*model)

	if mm.vp.YOffset != mainOffsetBefore {
		t.Errorf("main viewport YOffset changed from %d to %d — wheel should have scrolled the attached view instead", mainOffsetBefore, mm.vp.YOffset)
	}
	if mm.attached.vp.YOffset >= attachOffsetBefore {
		t.Errorf("attached viewport YOffset = %d, want less than %d (scrolled up)", mm.attached.vp.YOffset, attachOffsetBefore)
	}
}
