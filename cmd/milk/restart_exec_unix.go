//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// execRestart replaces the current process with the relaunched command
// (issue #258) — the clean restart: same PID, and the terminal is already
// restored by the TUI teardown before this is called. Returns only on
// failure; on success the process simply *is* the new milk.
func execRestart(argv []string, dir string) error {
	if dir != "" {
		// syscall.Exec has no cwd parameter — chdir first. Best effort: a
		// vanished launch dir still restarts, just from the current one.
		_ = os.Chdir(dir) //nolint:errcheck
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
