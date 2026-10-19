package local

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/scoutme/milk/internal/livebuf"
)

// bashbg.go — background shells started via bash(run_in_background), with
// bash_output to read them and kill_bash to stop them. Lifecycle rules are
// recorded in ADR-0063:
//
//   - A shell outlives the turn that started it (it runs under the registry's
//     own context, like a background agent job) but never outlives milk:
//     StopBackgroundShells is called from the process exit path.
//   - Hard cap on concurrent shells; a shell is auto-killed after
//     bgShellMaxLifetime so a forgotten dev server cannot live forever.
//   - Output accumulates in a byte-capped livebuf.Buffer (oldest dropped), so
//     a chatty process cannot exhaust memory.
//   - Finished shells stay readable (bash_output) until reaped
//     (bgShellRetainAfterExit after exit, or when the registry fills up).

const (
	maxBackgroundShells     = 16
	bgShellMaxLifetime      = time.Hour
	bgShellRetainAfterExit  = 30 * time.Minute
	bgShellOutputBufferByte = 256 * 1024
)

type bgShell struct {
	id      string
	cmd     *exec.Cmd
	out     *livebuf.Buffer
	done    chan struct{}
	started time.Time

	mu         sync.Mutex
	exitCode   int
	exitErr    error
	finished   bool
	finishedAt time.Time
	killed     bool
}

// statusLine renders the shell's state prefix for bash_output results.
func (s *bgShell) statusLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.killed && !s.finished:
		return fmt.Sprintf("[shell %s] killed (stopping)", s.id)
	case s.killed:
		return fmt.Sprintf("[shell %s] killed (exit code %d)", s.id, s.exitCode)
	case s.finished:
		if s.exitErr != nil {
			return fmt.Sprintf("[shell %s] exited: %v", s.id, s.exitErr)
		}
		return fmt.Sprintf("[shell %s] exited (code %d)", s.id, s.exitCode)
	default:
		pid := 0
		if s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		return fmt.Sprintf("[shell %s] running since %s (pid %d)", s.id, s.started.Format(time.RFC3339), pid)
	}
}

// finishState reports whether the shell has exited and when.
func (s *bgShell) finishState() (finished bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished, s.finishedAt
}

type shellRegistry struct {
	mu     sync.Mutex
	shells map[string]*bgShell
	nextID int
}

var bgShells = &shellRegistry{shells: map[string]*bgShell{}}

// StopBackgroundShells kills every tracked background shell. Called from the
// milk process exit path so no shell outlives the session (ADR-0063).
func StopBackgroundShells() {
	bgShells.mu.Lock()
	all := make([]*bgShell, 0, len(bgShells.shells))
	for _, s := range bgShells.shells {
		all = append(all, s)
	}
	bgShells.shells = map[string]*bgShell{}
	bgShells.mu.Unlock()
	for _, s := range all {
		s.kill()
	}
}

// reapLocked drops finished shells past their retention window.
// Caller holds r.mu.
func (r *shellRegistry) reapLocked() {
	now := time.Now()
	for id, s := range r.shells {
		if done, at := s.finishState(); done && now.Sub(at) > bgShellRetainAfterExit {
			delete(r.shells, id)
		}
	}
}

// start registers and starts a background shell running sh -c command in dir.
func (r *shellRegistry) start(command, dir string) (*bgShell, error) {
	r.mu.Lock()
	r.reapLocked()
	if len(r.shells) >= maxBackgroundShells {
		r.mu.Unlock()
		return nil, fmt.Errorf("too many background shells (%d) — stop one with kill_bash", maxBackgroundShells)
	}
	r.nextID++
	s := &bgShell{
		id:      fmt.Sprintf("s%d", r.nextID),
		out:     livebuf.New(bgShellOutputBufferByte),
		done:    make(chan struct{}),
		started: time.Now(),
	}
	s.cmd = exec.Command("sh", "-c", command)
	if dir != "" {
		s.cmd.Dir = dir
	}
	setProcGroup(s.cmd)
	s.cmd.Stdout = s.out.Writer()
	s.cmd.Stderr = s.out.Writer()
	r.shells[s.id] = s
	r.mu.Unlock()

	if err := s.cmd.Start(); err != nil {
		r.mu.Lock()
		delete(r.shells, s.id)
		r.mu.Unlock()
		return nil, err
	}

	go func() {
		err := s.cmd.Wait()
		s.mu.Lock()
		s.finished = true
		s.finishedAt = time.Now()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				s.exitCode = exit.ExitCode()
			} else {
				s.exitErr = err
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	// Auto-kill after the hard lifetime cap.
	go func() {
		select {
		case <-s.done:
		case <-time.After(bgShellMaxLifetime):
			s.kill()
		}
	}()

	return s, nil
}

// kill stops the shell's process group; a shell that already exited is left
// as-is (its output stays readable).
func (s *bgShell) kill() {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.killed = true
	proc := s.cmd.Process
	s.mu.Unlock()
	if proc != nil {
		killProcGroup(s.cmd)
	}
}

func (r *shellRegistry) get(id string) (*bgShell, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.shells[id]
	return s, ok
}

// runBashOutput implements the bash_output tool.
func runBashOutput(args map[string]any) (string, bool) {
	id, _ := args["shell_id"].(string)
	s, ok := bgShells.get(id)
	if !ok {
		return toolResult{Error: fmt.Sprintf("no background shell %q — IDs are returned by bash with run_in_background; finished shells are reaped 30 min after exit", id)}.String(), false
	}
	out := s.out.Snapshot()
	if n := intArg(args, "tail_lines"); n > 0 {
		lines := strings.Split(out, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1] // trailing newline is not a line
		}
		if len(lines) > n {
			omitted := len(lines) - n
			out = fmt.Sprintf("… (%d earlier lines omitted)\n", omitted) + strings.Join(lines[len(lines)-n:], "\n")
		}
	}
	return toolResult{Output: s.statusLine() + "\n" + out}.String(), false
}

// runKillBash implements the kill_bash tool.
func runKillBash(args map[string]any) (string, bool) {
	id, _ := args["shell_id"].(string)
	s, ok := bgShells.get(id)
	if !ok {
		return toolResult{Error: fmt.Sprintf("no background shell %q", id)}.String(), false
	}
	s.kill()
	return toolResult{Output: fmt.Sprintf("killed background shell %s", id)}.String(), false
}
