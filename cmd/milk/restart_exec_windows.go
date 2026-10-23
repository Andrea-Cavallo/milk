//go:build windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
)

// execRestart starts the relaunched command and stays alive until it exits
// (issue #258, fix for issue #313).
//
// Windows has no exec, so the replacement must be a new process — and the
// obvious "spawn, then exit" is what garbles every /restart on Windows:
//
//   - The shell that launched us (cmd, PowerShell, …) regains control the
//     instant we exit. It prints a prompt and blocks in ReadConsole on the
//     shared console input buffer while the relaunched milk is initializing
//     its TUI on the *same* console. Two readers on one input buffer: the
//     shell eats keystrokes meant for milk (and later executes them as
//     commands), milk misses keys, and prompt writes interleave with the
//     new instance's startup frames — the garbled screen users report.
//   - No spawn ordering can fix it from the other side either: the child
//     must not touch the console while we still live, *and* the shell must
//     not read the console while the child lives.
//
// The Unix path (restart_exec_unix.go) cannot hit any of this:
// syscall.Exec replaces this very process, so the shell stays blocked in
// wait on the same PID and the new milk is the exclusive console owner
// from its first instruction. To reproduce that serialization we
// babysit: keep this process alive — the shell stays blocked in wait on
// *us* — and block in wait on the relaunched instance. The shell only
// gets its prompt (and only then starts reading console input) after the
// relaunched milk exits. Repeated /restarts stack one sleeping waiter per
// generation; every generation stays correctly serialized, the cost is
// one idle process per past restart.
//
// While babysitting, console control events must not kill us: keyboard
// Ctrl+C normally reaches the child as a key event (the TUI runs with
// processed input off), but CTRL_C/CTRL_BREAK are also delivered as
// control events to every process sharing the console — always for
// Ctrl+Break, and for Ctrl+C whenever the console is in cooked mode
// (e.g. the child's startup window, before its TUI takes over). On
// Windows both surface to Go as os.Interrupt (runtime ctrlHandler maps
// CTRL_C_EVENT and CTRL_BREAK_EVENT to SIGINT — see Go's own
// os/signal TestCtrlBreak). Go's ctrl handler reports the event as
// handled only when the signal is *wanted* (runtime ctrlHandler →
// sigsend), which is exactly what a drained Notify channel arranges —
// and exactly what signal.Ignore does NOT do: Ignore clears the wanted
// bit, so the event still falls through to the default handler and
// would kill us mid-session, handing the console back to the shell with
// milk still running.
//
// Returns only on failure to start; on success this blocks until the
// relaunched instance exits, then exits.
func execRestart(argv []string, dir string) error {
	// Absorb control events *before* the child exists so there is no
	// window in which Ctrl+C/Ctrl+Break could kill the babysitter.
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt)
	go func() {
		for range sig {
			// Deliberately ignored: the relaunched instance owns the keys.
		}
	}()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// The shell stays blocked in wait on us for as long as we wait here.
	_ = cmd.Wait()
	os.Exit(0)
	return nil
}
