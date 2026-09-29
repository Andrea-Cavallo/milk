package local

import "testing"

// --- matchesBashPattern ---

func TestMatchesBashPattern_PrefixMatch(t *testing.T) {
	if !matchesBashPattern("git diff --stat HEAD", []string{"git diff*"}) {
		t.Error("expected a prefix pattern to match a command starting with that prefix")
	}
}

func TestMatchesBashPattern_ExactMatchNoWildcard(t *testing.T) {
	if !matchesBashPattern("git status", []string{"git status"}) {
		t.Error("expected an exact pattern to match the identical command")
	}
	if matchesBashPattern("git status --short", []string{"git status"}) {
		t.Error("expected an exact (no-wildcard) pattern NOT to prefix-match a longer command")
	}
}

func TestMatchesBashPattern_NoMatch(t *testing.T) {
	if matchesBashPattern("git push origin main", []string{"git diff*", "git status"}) {
		t.Error("expected git push to not match either pattern")
	}
}

func TestMatchesBashPattern_EmptyPatternsOrCommand(t *testing.T) {
	if matchesBashPattern("git status", nil) {
		t.Error("expected no match with an empty pattern list")
	}
	if matchesBashPattern("", []string{"git status"}) {
		t.Error("expected no match for an empty command")
	}
}

func TestMatchesBashPattern_TrimsWhitespace(t *testing.T) {
	if !matchesBashPattern("  git diff --stat  ", []string{"git diff*"}) {
		t.Error("expected leading/trailing whitespace on the command to be trimmed before matching")
	}
}

// --- checkPermission's bashAllowedPatterns short-circuit ---

func TestCheckPermission_BashAllowedPattern_GrantsWithoutAsking(t *testing.T) {
	a := &Agent{
		bashAllowedPatterns: []string{"git status*", "git diff*"},
		permAsk: func(tool, summary string) bool {
			t.Fatal("permAsk should not be called when a bash-allowed pattern matches")
			return false
		},
	}
	allowed, denied := a.checkPermission("bash", "git status", "git status --short")
	if !allowed || denied != "" {
		t.Errorf("expected the matching bash command to be granted without asking, got allowed=%v denied=%q", allowed, denied)
	}
}

func TestCheckPermission_BashNonMatchingPattern_FallsThroughToAsk(t *testing.T) {
	asked := false
	a := &Agent{
		bashAllowedPatterns: []string{"git status*", "git diff*"},
		permAsk: func(tool, summary string) bool {
			asked = true
			return false
		},
	}
	allowed, _ := a.checkPermission("bash", "git push", "git push origin main")
	if allowed {
		t.Error("expected git push (no matching pattern) NOT to be auto-granted")
	}
	if !asked {
		t.Error("expected permAsk to be invoked for a non-matching bash command")
	}
}

func TestCheckPermission_NonBashTool_IgnoresBashAllowedPatterns(t *testing.T) {
	// A pattern that would match anything, on an Agent with no permAsk (the
	// non-interactive deny-cleanly path) — if bashAllowedPatterns leaked
	// into non-bash tools, this would be wrongly granted instead of denied.
	a := &Agent{bashAllowedPatterns: []string{"*"}}
	allowed, denied := a.checkPermission("edit_file", "some/file.go", "")
	if allowed || denied == "" {
		t.Errorf("expected a non-bash tool to be denied (no permAsk configured), got allowed=%v denied=%q", allowed, denied)
	}
}
