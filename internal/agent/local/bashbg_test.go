package local

import (
	"strings"
	"testing"
	"time"
)

// waitForShell polls BackgroundShell until want holds or the deadline passes.
func waitForShell(t *testing.T, id string, want func(ShellInfo) bool) ShellInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, ok := BackgroundShell(id); ok && want(info) {
			return info
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, _ := BackgroundShell(id)
	t.Fatalf("shell %s never reached the wanted state; last: %+v", id, info)
	return ShellInfo{}
}

func TestStartBackgroundShell_ListAndSnapshot(t *testing.T) {
	info, err := StartBackgroundShell("printf distinctive-shell-output", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell: %v", err)
	}
	if info.ID == "" {
		t.Fatal("fresh shell must have an ID")
	}
	if got := info.Command; got != "printf distinctive-shell-output" {
		t.Fatalf("Command = %q, want the command verbatim", got)
	}
	final := waitForShell(t, info.ID, func(i ShellInfo) bool { return i.Finished })
	if final.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", final.ExitCode)
	}
	if final.Running {
		t.Fatal("finished shell must not report Running")
	}
	var listed bool
	for _, s := range BackgroundShells() {
		if s.ID == info.ID {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("BackgroundShells() is missing %s", info.ID)
	}
	buf, ok := ShellLive(info.ID)
	if !ok {
		t.Fatalf("ShellLive(%s) missing", info.ID)
	}
	if !strings.Contains(buf.Snapshot(), "distinctive-shell-output") {
		t.Fatalf("live snapshot = %q, want the shell's output", buf.Snapshot())
	}
	if _, ok := ShellLive("no-such-shell"); ok {
		t.Fatal("ShellLive must report ok=false for unknown shells")
	}
}

func TestKillBackgroundShell_AndStatusLabel(t *testing.T) {
	info, err := StartBackgroundShell("sleep 30", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell: %v", err)
	}
	if got := info.StatusLabel(); got != "running" {
		t.Fatalf("fresh StatusLabel = %q, want running", got)
	}
	killed, ok := KillBackgroundShell(info.ID)
	if !ok {
		t.Fatalf("KillBackgroundShell(%s) not found", info.ID)
	}
	if !killed.Killed {
		t.Fatal("killed shell must report Killed")
	}
	if got := killed.StatusLabel(); got != "stopping" {
		t.Fatalf("post-kill StatusLabel = %q, want stopping", got)
	}
	final := waitForShell(t, info.ID, func(i ShellInfo) bool { return i.Finished })
	if got := final.StatusLabel(); !strings.HasPrefix(got, "killed (exit ") {
		t.Fatalf("finished StatusLabel = %q, want killed (exit N)", got)
	}
	if _, ok := KillBackgroundShell("no-such-shell"); ok {
		t.Fatal("KillBackgroundShell must report ok=false for unknown shells")
	}
}

func TestBackgroundShell_NonZeroExitFails(t *testing.T) {
	info, err := StartBackgroundShell("exit 3", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell: %v", err)
	}
	final := waitForShell(t, info.ID, func(i ShellInfo) bool { return i.Finished })
	if final.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", final.ExitCode)
	}
	if got := final.StatusLabel(); got != "exited 3" {
		t.Fatalf("StatusLabel = %q, want exited 3", got)
	}
}

func TestShellListener_OrderAndRemove(t *testing.T) {
	events := make(chan string, 8)
	remove := AddShellListener(
		func(info ShellInfo) { events <- "start:" + info.ID },
		func(info ShellInfo) { events <- "finish:" + info.ID },
	)
	defer remove()

	info, err := StartBackgroundShell("true", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell: %v", err)
	}
	waitForShell(t, info.ID, func(i ShellInfo) bool { return i.Finished })
	var got []string
	for len(got) < 2 {
		select {
		case e := <-events:
			got = append(got, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("listener events = %v, want start then finish", got)
		}
	}
	if got[0] != "start:"+info.ID || got[1] != "finish:"+info.ID {
		t.Fatalf("listener order = %v, want [%s %s]", got, "start:"+info.ID, "finish:"+info.ID)
	}

	remove()
	second, err := StartBackgroundShell("true", "")
	if err != nil {
		t.Fatalf("StartBackgroundShell after remove: %v", err)
	}
	waitForShell(t, second.ID, func(i ShellInfo) bool { return i.Finished })
	select {
	case e := <-events:
		t.Fatalf("removed listener still received %q", e)
	case <-time.After(200 * time.Millisecond):
	}
}
