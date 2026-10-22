package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/oversight"
)

// TestExecPermissionsCore_SessionMode pins the /permissions mode grammar
// (issue #302): allow-all|deny-all|off set the session mode, bad arguments
// get a usage line, and the bare listing shows the active mode.
func TestExecPermissionsCore_SessionMode(t *testing.T) {
	cases := []struct {
		rest     string
		wantMode sessionPermMode
		wantPart string
	}{
		{"mode allow-all", sessionPermAllowAll, "allow-all"},
		{"mode deny-all", sessionPermDenyAll, "deny-all"},
		{"mode off", sessionPermOff, "off — agents prompt again"},
		{"mode bogus", sessionPermOff, "usage: /permissions mode"},
		{"mode", sessionPermOff, "usage: /permissions mode"},
	}
	for _, c := range cases {
		var ov permOverrides
		out, listing := execPermissionsCore(c.rest, config.Config{}, &ov)
		if listing {
			t.Errorf("(%q) unexpectedly requested listing", c.rest)
		}
		if ov.sessionMode != c.wantMode {
			t.Errorf("(%q) sessionMode = %v, want %v", c.rest, ov.sessionMode, c.wantMode)
		}
		if !strings.Contains(out, c.wantPart) {
			t.Errorf("(%q) output %q missing %q", c.rest, out, c.wantPart)
		}
	}

	// Bare form lists the mode.
	var ov permOverrides
	ov.sessionMode = sessionPermDenyAll
	_, listing := execPermissionsCore("", config.Config{}, &ov)
	if !listing {
		t.Fatalf("bare /permissions should list")
	}
	if got := formatPermPolicy(config.Config{}, ov); !strings.Contains(got, "deny-all") {
		t.Errorf("formatPermPolicy missing active mode, got: %s", got)
	}
}

// TestAskPermission_SessionMode pins that a decided session mode answers the
// ask before any surface is prompted — including the no-surface fast path and
// without waiting out a background deadline (issue #302).
func TestAskPermission_SessionMode(t *testing.T) {
	boom := func(context.Context) bool {
		t.Fatal("direct surface must not be consulted when a session mode is set")
		return false
	}
	for _, tc := range []struct {
		mode    sessionPermMode
		want    bool
		wantSrc permSource
	}{
		{sessionPermAllowAll, true, permSrcSessionMode},
		{sessionPermDenyAll, false, permSrcSessionMode},
	} {
		p := bgAskPolicy(config.Config{}, permOverrides{sessionMode: tc.mode})
		p.deadline = time.Minute // would be waited out without the mode
		got, src := askPermission(context.Background(), nil, oversight.PermRequest{ToolName: "bash"}, boom, p)
		if got != tc.want || src != tc.wantSrc {
			t.Errorf("mode %v: askPermission = (%v, %s), want (%v, %s)", tc.mode, got, src, tc.want, tc.wantSrc)
		}
	}

	// No mode: the same policy falls through to the timed answer (the direct
	// surface is consulted but never replies — silence is the deadline's).
	p := bgAskPolicy(config.Config{}, permOverrides{})
	p.deadline = 10 * time.Millisecond
	p.def = true
	got, src := askPermission(context.Background(), nil, oversight.PermRequest{ToolName: "bash"},
		func(ctx context.Context) bool { <-ctx.Done(); return false }, p)
	if !got || src != permSrcDefault {
		t.Errorf("no mode: askPermission = (%v, %s), want (true, timeout_default)", got, src)
	}
}

// TestSafetyPolicy_IgnoresSessionMode pins the doom-loop exemption (issue
// #302): even with the session mode set to allow-all, the safety gate's
// policy never consults it and keeps prompting.
func TestSafetyPolicy_IgnoresSessionMode(t *testing.T) {
	ov := permOverrides{sessionMode: sessionPermAllowAll}
	p := safetyAskPolicy(config.Config{}, ov)
	if p.sessionMode.decided() {
		t.Fatalf("safetyAskPolicy must not carry the session mode, got %v", p.sessionMode)
	}
	if bgAskPolicy(config.Config{}, ov).sessionMode != sessionPermAllowAll {
		t.Fatalf("bgAskPolicy should carry the session mode")
	}
}

// TestClearSessionPermMode pins /skip-permissions' mode clearing (issue #302)
// and the toggle flag it rides on.
func TestClearSessionPermMode(t *testing.T) {
	var ov permOverrides
	ov.sessionMode = sessionPermAllowAll
	if note := clearSessionPermMode(&ov); !strings.Contains(note, "cleared") {
		t.Errorf("note = %q, want 'cleared'", note)
	}
	if ov.sessionMode.decided() {
		t.Errorf("mode still decided after clear")
	}
	if note := clearSessionPermMode(&ov); note != "" {
		t.Errorf("second clear note = %q, want empty", note)
	}
	for _, c := range []struct {
		sub     string
		changed bool
	}{
		{"on", true}, {"off", true}, {"", false}, {"bogus", false},
	} {
		if _, _, changed := execSkipPermsCore(c.sub, false); changed != c.changed {
			t.Errorf("execSkipPermsCore(%q) changed = %v, want %v", c.sub, changed, c.changed)
		}
	}
}

// TestACPPermChoice pins the ACP dialog mapping (issue #302): once/always ×
// allow/deny, unknown IDs fail closed.
func TestACPPermChoice(t *testing.T) {
	for _, c := range []struct {
		id          string
		allow, once bool
	}{
		{acpPermAllowOptionID, true, false},
		{acpPermAlwaysAllowOptionID, true, true},
		{acpPermDenyOptionID, false, false},
		{acpPermAlwaysDenyOptionID, false, true},
		{"invented", false, false},
	} {
		allow, always := acpPermChoice(c.id)
		if allow != c.allow || always != c.once {
			t.Errorf("acpPermChoice(%q) = (%v, %v), want (%v, %v)", c.id, allow, always, c.allow, c.once)
		}
	}
	opts := acpPermOptions()
	if len(opts) != 4 {
		t.Fatalf("acpPermOptions() = %d entries, want 4", len(opts))
	}
	kinds := map[string]bool{}
	for _, o := range opts {
		kinds[string(o.Kind)] = true
	}
	for _, want := range []string{"allow_once", "allow_always", "reject_once", "reject_always"} {
		if !kinds[want] {
			t.Errorf("acpPermOptions() missing kind %q", want)
		}
	}
}
