package main

import (
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/agent/local"
)

func TestClassifyParent(t *testing.T) {
	for _, tc := range []struct {
		parent   string
		viaTask  bool
		taskName string
	}{
		{parent: "task run", viaTask: true, taskName: "run"},
		{parent: "/usr/local/bin/task run --verbose", viaTask: true, taskName: "run"},
		{parent: "task", viaTask: true, taskName: "run"}, // Taskfile default
		{parent: "task test:unit", viaTask: true, taskName: "test:unit"},
		{parent: "task -c /some/dir run", viaTask: true, taskName: "run"},
		{parent: "bash -l", viaTask: false},
		{parent: "/usr/local/go/bin/go run ./cmd/milk", viaTask: false},
		{parent: "", viaTask: false},
	} {
		viaTask, taskName := classifyParent(tc.parent)
		if viaTask != tc.viaTask || taskName != tc.taskName {
			t.Errorf("classifyParent(%q) = (%v, %q), want (%v, %q)", tc.parent, viaTask, taskName, tc.viaTask, tc.taskName)
		}
	}
}

func TestPlanRestart_BinaryLaunchPinsCurrentSession(t *testing.T) {
	li := LaunchInfo{
		Args:    []string{"milk", "--new", "--config-root", "/tmp/root"},
		ExePath: "/usr/bin/milk",
		Cwd:     "/work",
	}
	plan := li.planRestart("sess-abc")
	if plan.Mode != "exec" {
		t.Fatalf("Mode = %q, want exec", plan.Mode)
	}
	want := []string{"/usr/bin/milk", "--config-root", "/tmp/root", "--session", "sess-abc"}
	if strings.Join(plan.Argv, " ") != strings.Join(want, " ") {
		t.Errorf("Argv = %q, want %q (no --new: the restart resumes, it must not start fresh)", plan.Argv, want)
	}
	if plan.Dir != "/work" {
		t.Errorf("Dir = %q, want the launch cwd", plan.Dir)
	}
}

func TestStripSessionFlags(t *testing.T) {
	got := stripSessionFlags([]string{"--new", "--session", "old", "--session=old2", "--continue", "-c", "--agent", "x"})
	if strings.Join(got, " ") != "--agent x" {
		t.Errorf("stripSessionFlags = %q, want only the non-session flags", got)
	}
}

func TestPlanRestart_TaskLaunchRebuilds(t *testing.T) {
	li := LaunchInfo{
		Args:     []string{"/home/u/.local/bin/milk"},
		ExePath:  "/home/u/.local/bin/milk",
		Cwd:      "/work",
		ViaTask:  true,
		TaskName: "run",
	}
	plan := li.planRestart("sess-abc")
	if plan.Mode != "task" || strings.Join(plan.Argv, " ") != "task run" {
		t.Fatalf("plan = %+v, want task run (session pinning rides the default resume: task passes no flags)", plan)
	}
}

// The busy-state refusal (issue #258 acceptance): /restart must refuse while
// a turn is running, and while a background shell is live — the process is
// about to be replaced.
func TestHandleRestartCmd_RefusedWhileBusy(t *testing.T) {
	m := attachTestModel()
	m.busy = true
	res, cmd := m.handleRestartCmd("")
	if cmd != nil {
		t.Fatal("a busy /restart must not quit")
	}
	if !strings.Contains(res.(model).transcript.String(), "refused") {
		t.Fatalf("expected a refusal in the transcript, got %q", res.(model).transcript.String())
	}
}

func TestHandleRestartCmd_RefusedWhileShellRunning(t *testing.T) {
	info, err := local.StartBackgroundShell("sleep 5", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell: %v", err)
	}
	defer func() {
		local.KillBackgroundShell(info.ID) //nolint:errcheck
		// Wait for the shell to actually be dead: later tests assert on an
		// idle restart and would see it as still running.
		for i := 0; i < 500; i++ {
			if cur, ok := local.BackgroundShell(info.ID); ok && cur.Finished {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	m := attachTestModel()
	res, cmd := m.handleRestartCmd("")
	if cmd != nil {
		t.Fatal("a /restart with a live background shell must not quit")
	}
	out := res.(model).transcript.String()
	if !strings.Contains(out, "refused") || !strings.Contains(out, info.ID) {
		t.Fatalf("expected a refusal naming the shell, got %q", out)
	}
}

func TestHandleRestartCmd_IdleQuitsWithPlan(t *testing.T) {
	old := capturedLaunch
	capturedLaunch = LaunchInfo{
		Args:    []string{"milk", "--new"},
		ExePath: "/usr/bin/milk",
		Cwd:     "/work",
	}
	defer func() { capturedLaunch = old }()

	m := attachTestModel()
	m.st.sess.ID = "sess-xyz"
	res, cmd := m.handleRestartCmd("")
	if cmd == nil {
		t.Fatal("an idle /restart must quit with the relaunch plan armed")
	}
	fm := res.(model)
	if fm.restartPlan == nil {
		t.Fatal("restartPlan not armed")
	}
	if strings.Join(fm.restartPlan.Argv, " ") != "/usr/bin/milk --session sess-xyz" {
		t.Errorf("relaunch argv = %q", fm.restartPlan.Argv)
	}
}

func TestHandleRestartCmd_RejectsArguments(t *testing.T) {
	m := attachTestModel()
	res, cmd := m.handleRestartCmd("now")
	if cmd != nil {
		t.Fatal("/restart with arguments must not quit")
	}
	if !strings.Contains(res.(model).transcript.String(), "usage: /restart") {
		t.Fatalf("expected a usage line, got %q", res.(model).transcript.String())
	}
}
