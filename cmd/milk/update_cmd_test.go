package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/updater"
)

// TestHandleUpdateCmd_Status pins the /update status verb the shared
// registry hint (check|status|install|skip) advertises on both hosts: it
// reports the update state from what is already known — no network round
// trip — and never falls through to the usage line. Ordering follows
// actionability: install state, then the pending release, then the skip.
func TestHandleUpdateCmd_Status(t *testing.T) {
	m := newTestModelForPanels(t)
	m.st.cfg = config.Config{}

	// A pending release: named, with the install/skip next steps.
	m.pendingUpdate = &updater.Release{Tag: "v9.9.9"}
	mm, _ := m.handleUpdateCmd("status")
	out := strings.ToLower(mm.(model).transcript.String())
	if !strings.Contains(out, "v9.9.9") || !strings.Contains(out, "available") {
		t.Errorf("status with a pending release = %q, want the tag and next steps", out)
	}

	// Skipped release: the skip note instead of the usage line.
	m.pendingUpdate = nil
	m.st.cfg.UpdateSkippedVersion = "v9.9.8"
	mm, _ = m.handleUpdateCmd("status")
	out = strings.ToLower(mm.(model).transcript.String())
	if !strings.Contains(out, "v9.9.8") || !strings.Contains(out, "skipped") {
		t.Errorf("status with a skipped release = %q, want the skip note", out)
	}

	// Nothing known yet: points at /update check.
	m.st.cfg.UpdateSkippedVersion = ""
	mm, _ = m.handleUpdateCmd("status")
	out = strings.ToLower(mm.(model).transcript.String())
	if !strings.Contains(out, "/update check") {
		t.Errorf("status with no information = %q, want a pointer to /update check", out)
	}

	// Unknown verbs still get the usage line — with status in it.
	mm, _ = m.handleUpdateCmd("wat")
	out = mm.(model).transcript.String()
	if !strings.Contains(out, "usage: /update [check|status|install|skip]") {
		t.Errorf("unknown verb = %q, want the usage line naming every verb", out)
	}
}
