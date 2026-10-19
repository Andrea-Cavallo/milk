package acp

import (
	"testing"
	"time"

	"github.com/scoutme/milk/internal/agent/local"
)

func TestShellToolCallID(t *testing.T) {
	if got := ShellToolCallID("s3"); got != ToolCallID("shell:s3") {
		t.Fatalf("ShellToolCallID = %q, want shell:s3", got)
	}
}

func TestToolCallStatusForShell(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		info local.ShellInfo
		want ToolCallStatus
	}{
		{"running", local.ShellInfo{StartedAt: now}, ToolCallInProgress},
		{"stopping", local.ShellInfo{StartedAt: now, Killed: true}, ToolCallInProgress},
		{"exited clean", local.ShellInfo{Finished: true, StartedAt: now, FinishedAt: now}, ToolCallCompleted},
		{"non-zero exit", local.ShellInfo{Finished: true, ExitCode: 3, StartedAt: now, FinishedAt: now}, ToolCallFailed},
		{"killed", local.ShellInfo{Finished: true, Killed: true, ExitCode: -1, StartedAt: now, FinishedAt: now}, ToolCallCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolCallStatusForShell(tc.info); got != tc.want {
				t.Fatalf("ToolCallStatusForShell = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBackgroundShellCall(t *testing.T) {
	info := local.ShellInfo{
		ID:        "s7",
		Command:   "npm run dev",
		Dir:       "/proj",
		Finished:  true,
		ExitCode:  0,
		StartedAt: time.Now().Add(-time.Minute),
	}
	u := BackgroundShellCall(info)
	if u.ToolCallID != ShellToolCallID("s7") {
		t.Fatalf("ToolCallID = %q", u.ToolCallID)
	}
	if u.Name != "background_shell" {
		t.Fatalf("Name = %q", u.Name)
	}
	if u.Title != "background shell: npm run dev" {
		t.Fatalf("Title = %q", u.Title)
	}
	if u.Status != ToolCallCompleted {
		t.Fatalf("Status = %q, want completed", u.Status)
	}
	if rawIn, ok := u.RawInput.(map[string]any); !ok || rawIn["command"] != "npm run dev" {
		t.Fatalf("RawInput = %#v", u.RawInput)
	}
	rawOut, ok := u.RawOutput.(map[string]any)
	if !ok || rawOut["exit_code"] != 0 {
		t.Fatalf("RawOutput = %#v, want exit_code 0", u.RawOutput)
	}
	if u.Meta["milk/shell_id"] != "s7" {
		t.Fatalf("Meta = %#v", u.Meta)
	}

	// A running shell has no terminal output block yet.
	running := BackgroundShellCall(local.ShellInfo{ID: "s8", Command: "x", StartedAt: time.Now()})
	if running.RawOutput != nil {
		t.Fatalf("running shell RawOutput = %#v, want nil", running.RawOutput)
	}
	if running.Status != ToolCallInProgress {
		t.Fatalf("running Status = %q", running.Status)
	}
}
