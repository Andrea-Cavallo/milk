package main

import "testing"

func TestDecideFollowup(t *testing.T) {
	cases := []struct {
		name    string
		wave    bool
		active  int
		pending int
		busy    bool
		want    followupDecision
	}{
		{"wave done and idle", true, 0, 1, false, followupNow},
		{"wave still has active jobs", true, 2, 1, false, followupSkip},
		{"wave still active beats busy", true, 1, 1, true, followupSkip},
		{"wave done but busy", true, 0, 1, true, followupRetryLater},
		{"user job idle, others still running", false, 3, 1, false, followupNow},
		{"user job but busy", false, 3, 1, true, followupRetryLater},
		// The shared nothing-to-report guard (issue #188): a user-job
		// completion fires both the per-job and the wave-done signal, so the
		// second one must not run an empty follow-up turn.
		{"nothing pending skips even when idle", true, 0, 0, false, followupSkip},
		{"user-job second signal with nothing pending", false, 0, 0, false, followupSkip},
		{"nothing pending wins over busy (no retry)", false, 0, 0, true, followupSkip},
	}
	for _, c := range cases {
		if got := decideFollowup(c.wave, c.active, c.pending, c.busy); got != c.want {
			t.Errorf("%s: decideFollowup = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestApplyFollowupDecisionFlags(t *testing.T) {
	type flags struct{ wave, user bool }
	cases := []struct {
		name  string
		wave  bool
		d     followupDecision
		start flags
		want  flags
	}{
		{"now consumes both", true, followupNow, flags{true, true}, flags{false, false}},
		{"retry remembers the wave kind only", true, followupRetryLater, flags{}, flags{wave: true}},
		{"retry remembers the user kind only", false, followupRetryLater, flags{}, flags{user: true}},
		{"skip clears its own kind", true, followupSkip, flags{true, true}, flags{user: true}},
		{"skip clears the user kind without touching the wave", false, followupSkip, flags{true, true}, flags{wave: true}},
	}
	for _, c := range cases {
		f := c.start
		applyFollowupDecision(c.wave, c.d, &f.wave, &f.user)
		if f != c.want {
			t.Errorf("%s: flags = %+v, want %+v", c.name, f, c.want)
		}
	}
}
