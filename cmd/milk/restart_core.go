package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// restart_core.go — /restart (issue #258): end this process and relaunch milk
// with the very same starting command and the current session.
//
// Safety: the relaunch argv is built exclusively from this process's own
// startup capture (os.Args, os.Executable, the detected parent process) —
// never from config, a model-provided string, or command output. /restart
// only ever replays what the user themselves launched.
//
// Two launch shapes:
//   - binary launch: in-place re-exec of the (possibly just /update
//     install-replaced) milk binary with the original argv, session flags
//     swapped for `--session <current>` so the conversation resumes even if
//     the original command used --new;
//   - task run launch (milk is a child of the task runner): re-run the same
//     task so the binary is rebuilt from the changed codebase instead of
//     re-running a stale one.

// LaunchInfo captures how this milk process was started.
type LaunchInfo struct {
	Args      []string // full argv as launched (os.Args)
	ExePath   string   // resolved absolute path of the running binary
	Cwd       string   // process working directory at startup
	ViaTask   bool     // started as a child of the task runner (task run)
	TaskName  string   // task to re-run in ViaTask mode (default "run")
	ParentCmd string   // parent process command line (diagnostics)
}

// capturedLaunch is filled once at TUI startup (runREPL), before any /resume
// can switch directories — so the captured cwd is the one milk was actually
// launched in. Tests set it directly.
var capturedLaunch LaunchInfo

// captureLaunchInfo snapshots the launch state.
func captureLaunchInfo() LaunchInfo {
	li := LaunchInfo{Args: append([]string(nil), os.Args...)}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		li.ExePath = exe
	}
	if cwd, err := os.Getwd(); err == nil {
		li.Cwd = cwd
	}
	li.ParentCmd = parentProcessArgs()
	li.ViaTask, li.TaskName = classifyParent(li.ParentCmd)
	return li
}

// parentProcessArgs returns the parent process's command line via ps(1)
// (Linux/macOS). An error returns "" — restart then falls back to binary
// mode, which is correct for everything except a task-runner launch.
func parentProcessArgs() string {
	out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(os.Getppid())).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// classifyParent decides whether milk was launched by the task runner and
// which task to re-run. Pure parsing (the ps capture lives in
// parentProcessArgs): `task run`, `/usr/local/bin/task run --verbose` and
// bare `task` (Taskfile default) are task launches; anything else is a plain
// binary launch. `-c <dir>`/`--dir <dir>` runner flags are skipped with
// their values.
func classifyParent(parentCmd string) (viaTask bool, taskName string) {
	fields := strings.Fields(parentCmd)
	if len(fields) == 0 {
		return false, ""
	}
	base := fields[0]
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if base != "task" {
		return false, ""
	}
	taskName = "run"
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if f == "-c" || f == "--dir" {
			i++ // the runner flag takes the next field as its value
			continue
		}
		if strings.HasPrefix(f, "-") {
			continue
		}
		taskName = f
		break
	}
	return true, taskName
}

// RestartPlan is the concrete relaunch action /restart performs.
type RestartPlan struct {
	Mode string   // "exec" (replace this process) or "task" (re-run via the task runner)
	Argv []string // argv for the new process (Argv[0] resolved via PATH when bare)
	Dir  string   // working directory for the relaunch (the launch cwd)
	Why  string   // human summary shown in the transcript
}

// planRestart builds the relaunch plan. sessionID (the currently bound
// session) is pinned explicitly via --session, so a launch that used --new
// resumes its conversation instead of starting fresh; the default
// resume-most-recent behavior is replaced by the explicit pin either way.
func (li LaunchInfo) planRestart(sessionID string) RestartPlan {
	if li.ViaTask {
		return RestartPlan{
			Mode: "task",
			Argv: []string{"task", li.TaskName},
			Dir:  li.Cwd,
			Why:  fmt.Sprintf("task %s (rebuild + relaunch)", li.TaskName),
		}
	}
	args := []string{li.ExePath}
	rest := li.Args
	if len(rest) > 0 {
		rest = rest[1:] // drop argv[0] — ExePath replaces it
	}
	args = append(args, stripSessionFlags(rest)...)
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	return RestartPlan{Mode: "exec", Argv: args, Dir: li.Cwd, Why: strings.Join(args, " ")}
}

// stripSessionFlags drops the session-shaping flags from the original argv:
// --new would start a fresh conversation and --continue/--session would
// target whatever they originally named, while the restart must resume the
// current session. Every other flag is replayed verbatim.
func stripSessionFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--new" || a == "--continue" || a == "-c":
			continue
		case a == "--session":
			i++ // drop the value with the flag
		case strings.HasPrefix(a, "--session="):
			continue
		default:
			out = append(out, a)
		}
	}
	return out
}
