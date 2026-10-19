//go:build windows

package main

import (
	"os"
	"os/exec"
)

// execRestart starts the relaunched command and exits the old process
// (issue #258). Windows has no exec, so the replacement is a detached
// spawn followed by exit — the new process keeps the console streams.
// Returns only on failure to start; on success the process exits.
func execRestart(argv []string, dir string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
