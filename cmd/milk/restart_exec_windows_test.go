//go:build windows

package main

import (
	"errors"
	"os/exec"
	"testing"
)

// TestExecRestart_StartFailureReturns pins the only branch execRestart can
// return through on Windows (issue #313): a binary that cannot be started
// reports the error instead of exiting — the caller turns it into
// "restart failed: …" and the process returns to its shell normally. The
// success path cannot be asserted in-process (it deliberately ends in
// os.Exit after the relaunched instance exits — the handover itself is
// live-verified on Windows: no prompt interleave, exclusive input for the
// new instance, one clean prompt when it quits).
func TestExecRestart_StartFailureReturns(t *testing.T) {
	err := execRestart([]string{`C:\definitely\not\present\milk-restart-test.exe`}, "")
	if err == nil {
		t.Fatal("execRestart with a missing binary must return an error, not exit")
	}
	var pathErr *exec.Error
	if !errors.As(err, &pathErr) {
		t.Logf("error type: %T (%v) — exec.Error expected from a missing path", err, err)
	}
}
