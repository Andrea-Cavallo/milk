package workflow

import "testing"

// Issue #197: stages declare their own verdict words (team's all_done /
// more_work) — the key-based parser matches THOSE, from the end, longest
// first, never searching for the "unknown" fallback itself, and callers
// fall back to the historical keywords (ParseVerdict) only when no declared
// key matched.
func TestParseVerdictKey_DeclaredWords(t *testing.T) {
	rules := map[string]VerdictRule{
		"all_done":  {Action: "break"},
		"more_work": {Action: "retry"},
		"unknown":   {Action: "retry", Warn: true},
	}

	if got := ParseVerdictKey("everything is finished\nALL_DONE", rules); got != "all_done" {
		t.Errorf("case-insensitive end match = %q, want all_done", got)
	}
	if got := ParseVerdictKey("keep going\nmore_work", rules); got != "more_work" {
		t.Errorf("more_work = %q", got)
	}
	if got := ParseVerdictKey("no verdict line anywhere", rules); got != "" {
		t.Errorf("no match must return \"\", got %q", got)
	}
	// The fallback word itself must never be scraped out of prose.
	if got := ParseVerdictKey("this is unknown territory", rules); got != "" {
		t.Errorf("the unknown fallback must not match prose, got %q", got)
	}
	// Longest key wins on one line: "all_done" beats a shorter "done".
	rules["done"] = VerdictRule{Action: "break"}
	if got := ParseVerdictKey("we are all_done now", rules); got != "all_done" {
		t.Errorf("longest-key priority = %q, want all_done", got)
	}
	// A later line wins over an earlier one (final verdict line discipline).
	if got := ParseVerdictKey("more_work\nactually all_done", rules); got != "all_done" {
		t.Errorf("end-scan = %q, want all_done", got)
	}
}
