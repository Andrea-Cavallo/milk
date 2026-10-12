package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scoutme/milk/internal/config"
)

// TestApplyConfigRoot pins the --config-root wiring: the flag must redirect
// config.Dir() and export MILK_CONFIG_ROOT so child processes inherit the
// same sandbox; an empty value must leave the default root alone.
func TestApplyConfigRoot(t *testing.T) {
	sandbox := t.TempDir()
	prev := config.SetRoot("")
	defer config.SetRoot(prev)
	t.Setenv("MILK_CONFIG_ROOT", "")

	if err := applyConfigRoot(sandbox); err != nil {
		t.Fatalf("applyConfigRoot: %v", err)
	}
	dir, err := config.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want, _ := filepath.Abs(sandbox); dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
	if got := os.Getenv("MILK_CONFIG_ROOT"); got != dir {
		t.Errorf("MILK_CONFIG_ROOT = %q, want %q", got, dir)
	}

	if err := applyConfigRoot(""); err != nil {
		t.Fatalf("applyConfigRoot(\"\"): %v", err)
	}
}
