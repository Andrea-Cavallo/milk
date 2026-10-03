package main

import "testing"

func TestDecideFollowup(t *testing.T) {
	cases := []struct {
		name   string
		wave   bool
		active int
		busy   bool
		want   followupDecision
	}{
		{"wave done and idle", true, 0, false, followupNow},
		{"wave still has active jobs", true, 2, false, followupSkip},
		{"wave still active beats busy", true, 1, true, followupSkip},
		{"wave done but busy", true, 0, true, followupRetryLater},
		{"user job idle, others still running", false, 3, false, followupNow},
		{"user job but busy", false, 3, true, followupRetryLater},
	}
	for _, c := range cases {
		if got := decideFollowup(c.wave, c.active, c.busy); got != c.want {
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
