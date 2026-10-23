package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/updater"
)

// The TUI must surface a Windows staged install as a deferred success —
// applied on next start — not as "update failed" (the old Windows behavior
// that made /update install appear broken), and it must clear the pending
// update notice the same way a completed install does.
func TestUpdateDoneMsg_StagedWindowsNote(t *testing.T) {
	m := layoutTestModel(t, 80, 20)
	m.pendingUpdate = &updater.Release{Tag: "v9.9.9"}

	next, _ := m.Update(updateDoneMsg{
		err: fmt.Errorf("%w: %s", updater.ErrWindowsStaged, "/tmp/milk.exe.new"),
	})
	got := next.(model)
	if got.pendingUpdate != nil {
		t.Error("pendingUpdate not cleared after a staged install")
	}
	out := got.transcript.String()
	if !strings.Contains(out, "update staged") {
		t.Errorf("transcript = %q, want the staged note", out)
	}
	if !strings.Contains(out, "on next start") {
		t.Errorf("transcript = %q, want the deferred-application note", out)
	}
	if strings.Contains(out, "update failed") {
		t.Errorf("staged install reported as failure: %q", out)
	}
}

// The manual fallback still reports as a failure carrying its path.
func TestUpdateDoneMsg_ManualStillFails(t *testing.T) {
	m := layoutTestModel(t, 80, 20)
	m.pendingUpdate = &updater.Release{Tag: "v9.9.9"}

	next, _ := m.Update(updateDoneMsg{
		err: fmt.Errorf("%w: %s", updater.ErrWindowsManual, "/tmp/milk-update-x"),
	})
	got := next.(model)
	out := got.transcript.String()
	if !strings.Contains(out, "update failed") || !strings.Contains(out, "/tmp/milk-update-x") {
		t.Errorf("transcript = %q, want the failed message with the temp path", out)
	}
	if got.pendingUpdate == nil {
		t.Error("pendingUpdate cleared although nothing was installed")
	}
}
