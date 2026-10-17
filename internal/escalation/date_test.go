package escalation

import (
	"strings"
	"testing"
	"time"
)

// fixed zone helpers keep the assertions independent of the host timezone.
func fixedZone(offsetSec int) *time.Location {
	name := "UTC"
	if offsetSec > 0 {
		name = "POS"
	} else if offsetSec < 0 {
		name = "NEG"
	}
	return time.FixedZone(name, offsetSec)
}

func TestDateLine_Format(t *testing.T) {
	now := time.Date(2026, time.October, 17, 22, 40, 0, 0, fixedZone(2*3600))
	want := "Current date: 2026-10-17 (Saturday), local time zone POS (UTC+02:00)."
	if got := DateLine(now); got != want {
		t.Errorf("DateLine = %q, want %q", got, want)
	}
}

func TestDateLine_NegativeOffset(t *testing.T) {
	now := time.Date(2026, time.October, 17, 16, 40, 0, 0, fixedZone(-5*3600))
	want := "Current date: 2026-10-17 (Saturday), local time zone NEG (UTC-05:00)."
	if got := DateLine(now); got != want {
		t.Errorf("DateLine = %q, want %q", got, want)
	}
}

// TestDateLine_HalfHourOffset guards non-whole-hour zones (e.g. UTC+05:30).
func TestDateLine_HalfHourOffset(t *testing.T) {
	now := time.Date(2026, time.October, 17, 22, 10, 0, 0, fixedZone(5*3600+1800))
	want := "Current date: 2026-10-17 (Saturday), local time zone POS (UTC+05:30)."
	if got := DateLine(now); got != want {
		t.Errorf("DateLine = %q, want %q", got, want)
	}
}

// TestDateLine_DateGranularity pins the cache-stability property: the rendered
// line must not change within the same calendar day (no HH:MM), but must roll
// over when the clock crosses midnight.
func TestDateLine_DateGranularity(t *testing.T) {
	before := DateLine(time.Date(2026, time.October, 17, 23, 59, 59, 0, fixedZone(0)))
	after := DateLine(time.Date(2026, time.October, 18, 0, 0, 1, 0, fixedZone(0)))
	earlier := DateLine(time.Date(2026, time.October, 17, 0, 0, 1, 0, fixedZone(0)))

	if before != earlier {
		t.Errorf("same-day renders differ (line is not date-granularity): %q vs %q", before, earlier)
	}
	if before == after {
		t.Errorf("line did not roll over at midnight: both %q", before)
	}
	if !strings.Contains(after, "2026-10-18 (Sunday)") {
		t.Errorf("post-midnight line missing new date/weekday: %q", after)
	}
}

// TestBuildStaticContext_AlwaysContainsDateLine walks every ContextMode /
// injectInstructions combination — the date line must survive even the
// identity-only paths (Continuation, suppressed Resume).
func TestBuildStaticContext_AlwaysContainsDateLine(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	want := "Current date: " + today

	cases := []struct {
		name               string
		mode               ContextMode
		injectInstructions bool
	}{
		{"First", ContextModeFirst, true},
		{"First-no-instructions", ContextModeFirst, false},
		{"Resume-suppressed", ContextModeResume, false},
		{"Resume-injected", ContextModeResume, true},
		{"Returning", ContextModeReturning, true},
		{"Continuation", ContextModeContinuation, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildStaticContext("n", []string{"- fact"}, tc.mode, tc.injectInstructions, "primary", "esc")
			if !strings.Contains(got, want) {
				t.Errorf("BuildStaticContext(%s, inject=%v) missing %q", tc.name, tc.injectInstructions, want)
			}
		})
	}
}

// TestBuildPrimaryStaticContext_ContainsDateLine: subprocess primaries
// (aider, smolagents) wrap the same static block — they must get the date too.
func TestBuildPrimaryStaticContext_ContainsDateLine(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	got := BuildPrimaryStaticContext("n", nil, ContextModeFirst, true, "primary", "esc")
	if !strings.Contains(got, "Current date: "+today) {
		t.Errorf("BuildPrimaryStaticContext missing current date line")
	}
}
