package local

import (
	"strings"
	"testing"
)

// TestCheckPermission_SessionMode pins the session allow-all/deny-all hook
// (issue #302): it answers before the persistent grant store and before any
// prompt — an allow-all grant must never leave a persistent grant behind, a
// deny-all denial gets the usual "was denied by user" error, and an undecided
// mode falls through to the normal chain untouched.
func TestCheckPermission_SessionMode(t *testing.T) {
	mustNotAsk := func(tool, summary string) bool {
		t.Fatalf("permAsk must not be consulted when a session mode is set (tool %s)", tool)
		return false
	}

	ps, err := OpenPermStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenPermStore: %v", err)
	}

	a := New("http://invalid", "test-model").
		WithPermissions(ps, mustNotAsk).
		WithPermissionModeFunc(func() (bool, bool) { return true, true })
	allowed, denied := a.checkPermission("write_file", "x", "")
	if !allowed || denied != "" {
		t.Fatalf("allow-all: got (%v, %q), want granted", allowed, denied)
	}
	if ps.IsAllowed("write_file") {
		t.Errorf("allow-all wrote a persistent grant — the mode must never persist")
	}

	a = a.WithPermissionModeFunc(func() (bool, bool) { return true, false })
	allowed, denied = a.checkPermission("bash", "rm -rf", "")
	if allowed {
		t.Fatalf("deny-all granted a tool call")
	}
	if !strings.Contains(denied, "was denied by user") {
		t.Errorf("deny-all denial = %q, want the usual 'was denied by user' error", denied)
	}

	// Undecided mode: the normal chain runs — here the prompt answers no.
	asked := false
	a = a.WithPermissionModeFunc(func() (bool, bool) { return false, false }).
		WithPermissions(nil, func(tool, summary string) bool { asked = true; return false })
	allowed, denied = a.checkPermission("bash", "rm -rf", "")
	if allowed || !asked {
		t.Errorf("undecided mode: got (allowed=%v, asked=%v), want (false, true)", allowed, asked)
	}
}
