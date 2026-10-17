package escalation

import (
	"fmt"
	"time"
)

// DateLine renders the injected "current date" context line that milk
// prepends to every agent's system context (issue: LLMs only know their
// training cutoff — without host injection they hallucinate the date; see
// docs/adr/0060-inject-current-date.md).
//
// The line is deliberately date-granularity only — no HH:MM. It lands in the
// static/cached prompt prefix on every path (identityBlock's home in
// BuildStaticContext, and the prompt core in local.buildSystemPromptOpts), so
// a clock reading that changed every render would invalidate prompt caches
// every turn (Claude's cached prefix, Bedrock cachePoint anchors). At date
// granularity the prefix only goes stale at midnight — or at a DST boundary,
// where the zone abbreviation/offset below changes — which is the accepted
// cost of a fresh date per turn.
//
// Callers pass time.Now() at render time (both prompt builders re-render per
// turn), so a session crossing midnight self-corrects on the next turn.
func DateLine(now time.Time) string {
	zone, offset := now.Zone()
	return fmt.Sprintf(
		"Current date: %s (%s), local time zone %s (UTC%s).",
		now.Format("2006-01-02"), now.Format("Monday"),
		zone, formatUTCOffset(offset),
	)
}

// formatUTCOffset renders a Go zone offset in seconds as "+02:00" / "-05:00".
func formatUTCOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, (seconds%3600)/60)
}
