package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/session"
)

// realRules returns the shipped default rules (via a throwaway empty config
// merged over defaults) so the scenario tests exercise the actual lexicons
// (refactor/design escalate verbs, list/fix local verbs, local_below_tokens 30).
func realRules(t *testing.T) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func histTurns(turns ...session.Turn) *session.Session {
	return &session.Session{History: turns}
}

func userTurn(s string) session.Turn {
	return session.Turn{Role: session.RoleUser, Content: s}
}

func assistantTurn(s string) session.Turn {
	return session.Turn{Role: session.RoleAssistant, Content: s}
}

func TestMatchConfirmation_Pure(t *testing.T) {
	for _, prompt := range []string{
		"proceed", "go ahead", "yes", "y", "do it", "ok", "OK.",
		"yes, go ahead", "go ahead and do it", "please proceed",
		"sounds good, implement it", "yes please", "sounds good!",
		"yep, go on", "ship it",
	} {
		ci, ok := matchConfirmation(prompt, nil)
		if !ok || ci.Scope != "" {
			t.Errorf("%q should be a pure confirmation, got ok=%v scope=%q", prompt, ok, ci.Scope)
		}
	}
}

func TestMatchConfirmation_WithScope(t *testing.T) {
	cases := map[string]string{
		"yes, do the big refactor":         "do the big refactor",
		"yes, list the files too":          "list the files too",
		"proceed with the refactor":        "refactor",
		"go ahead and delete the branch":   "delete the branch",
		"sounds good, add the tests too":   "add the tests too",
		"yes, and please rename it to cmd": "rename it to cmd",
	}
	for prompt, want := range cases {
		ci, ok := matchConfirmation(prompt, nil)
		if !ok || ci.Scope != want {
			t.Errorf("%q: want scope %q, got ok=%v scope=%q", prompt, want, ok, ci.Scope)
		}
	}
}

func TestMatchConfirmation_NotConfirmations(t *testing.T) {
	for _, prompt := range []string{
		// Ordinary task prompts whose words merely overlap the lexicon.
		"implement the parser rewrite",
		"list the files",
		"proceeding with the fix",
		"yesterday's release notes",
		"why is the sky blue",
		"continue-on-error handling in the runner",
	} {
		if _, ok := matchConfirmation(prompt, nil); ok {
			t.Errorf("%q should NOT be a confirmation", prompt)
		}
	}
	// "continue" IS a phrase: continuation prompts are detected, and their
	// scope is routed as the confirmed content (marker-aware, not the
	// unconditional shortcut).
	ci, ok := matchConfirmation("continue reading the file and print its contents", nil)
	if !ok || ci.Scope != "reading the file and print its contents" {
		t.Errorf("tail-bearing confirmation should carry its scope, got ok=%v %+v", ok, ci)
	}
}

func TestResolveConfirmedScope_NewScopeAgentAsk(t *testing.T) {
	sess := histTurns(
		userTurn("refactor the whole dispatch layer"),
		assistantTurn("Here is the plan… Want me to also rename the test file?"),
	)
	ci, ok := matchConfirmation("yes do it", nil)
	if !ok || ci.Scope != "" {
		t.Fatalf("expected pure confirmation, got %+v", ci)
	}
	scope, user := resolveConfirmedScope(sess, "yes do it", ci, realRules(t))
	if user {
		t.Errorf("agent-ask scope must be agent-authored, got user=%v", user)
	}
	if !strings.Contains(scope, "rename the test file") {
		t.Errorf("scope should be the agent's new-scope ask, got %q", scope)
	}
}

func TestResolveConfirmedScope_PendingTask(t *testing.T) {
	sess := histTurns(
		userTurn("refactor the whole dispatch layer"),
		assistantTurn("Shall I proceed with the plan?"),
	)
	ci, _ := matchConfirmation("proceed", nil)
	scope, user := resolveConfirmedScope(sess, "proceed", ci, realRules(t))
	if !user || scope != "refactor the whole dispatch layer" {
		t.Errorf("scope should be the pending task, got user=%v scope=%q", user, scope)
	}
}

func TestResolveConfirmedScope_ChainedConfirmations(t *testing.T) {
	sess := histTurns(
		userTurn("design the new memory consolidation"),
		assistantTurn("Which variant do you prefer?"),
		userTurn("proceed"),
		userTurn("go ahead"),
	)
	ci, _ := matchConfirmation("yes", nil)
	scope, _ := resolveConfirmedScope(sess, "yes", ci, realRules(t))
	if scope != "design the new memory consolidation" {
		t.Errorf("chained confirmations should walk back to the task, got %q", scope)
	}
}

func TestResolveConfirmedScope_TailBearingUserMessageIsTheTask(t *testing.T) {
	sess := histTurns(
		userTurn("how should we handle this?"),
		assistantTurn("Two options…"),
		userTurn("yes, do the big refactor"),
	)
	ci, _ := matchConfirmation("proceed", nil)
	scope, _ := resolveConfirmedScope(sess, "proceed", ci, realRules(t))
	if scope != "do the big refactor" {
		t.Errorf("tail-bearing message should contribute its scope as the task, got %q", scope)
	}
}

func TestResolveConfirmedScope_NoHistoryFallsBackToPrompt(t *testing.T) {
	ci, _ := matchConfirmation("proceed", nil)
	scope, _ := resolveConfirmedScope(&session.Session{}, "proceed", ci, realRules(t))
	if scope != "proceed" {
		t.Errorf("no history should fall back to the prompt itself, got %q", scope)
	}
}

func TestHasNewScopeMarker_WordBoundary(t *testing.T) {
	if !hasNewScopeMarker("Want me to also rename the test file?", nil) {
		t.Error("'also' should mark new scope")
	}
	if !hasNewScopeMarker("Do you want me to fix the tools too?", nil) {
		t.Error("'too' should mark new scope")
	}
	if hasNewScopeMarker("Shall I proceed with the refactor?", nil) {
		t.Error("continuation question must not carry a new-scope marker")
	}
	if hasNewScopeMarker("Do you want me to fix the tools?", nil) {
		t.Error("'tools' must not match the single-word marker 'too'")
	}
}

func TestConfirmationDecision_IssueScenarioMatrix(t *testing.T) {
	cfg := realRules(t)
	ctx := context.Background()
	cases := []struct {
		name        string
		scope       string
		user        bool
		wantTarget  Target
		wantConclus bool
	}{
		{"hard task: refactor", "refactor the whole dispatch layer", true, TargetEscalation, true},
		{"hard task: design", "design the new memory consolidation", true, TargetEscalation, true},
		{"decision point: do the big refactor", "do the big refactor", true, TargetEscalation, true},
		{"small sub-step: list the files too", "list the files too", true, TargetLocal, true},
		{"small sub-step: fix the typo", "fix the typo", true, TargetLocal, true},
		{"small sub-step: rename it to cmd", "rename it to cmd", true, TargetLocal, true},
		{"agent ask mirrors plain rules: rename the test file", "want me to also rename the test file?", false, TargetLocal, true},
		{"escalate keyword", "context brick for the transcript", true, TargetEscalation, true},
		// Medium scopes with only soft signals fall through to the classifier
		// fallback — inconclusive here.
		{"soft-only medium scope", "rename all the integration tests to include the suite name and update the harness config accordingly so nothing breaks, then also adjust the fixtures and the golden files to match the new naming scheme", true, Target(""), false},
	}
	for _, tc := range cases {
		d := confirmationDecision(ctx, tc.scope, cfg, tc.user)
		if d.Conclusive != tc.wantConclus {
			t.Errorf("%s: conclusive=%v want %v (%+v)", tc.name, d.Conclusive, tc.wantConclus, d)
			continue
		}
		if tc.wantConclus && d.Target != tc.wantTarget {
			t.Errorf("%s: target=%s want %s (%+v)", tc.name, d.Target, tc.wantTarget, d)
		}
	}
}

func TestRoute_ProceedOnHardTaskEscalates(t *testing.T) {
	// The exact incident from issue #263: a hard task, an agent question, and
	// a bare "yes" — which the unconditional local shortcut used to pin local.
	r := New(realRules(t), nil)
	sess := histTurns(
		userTurn("refactor the whole dispatch layer"),
		assistantTurn("Here is the plan. Shall I proceed?"),
	)
	d, err := r.Route(context.Background(), sess, "yes", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetEscalation || !strings.Contains(d.Reason, "confirmation") {
		t.Errorf("proceed on hard task should escalate via confirmation routing, got %+v", d)
	}
}

func TestRoute_ProceedOnSmallTaskStaysLocal(t *testing.T) {
	r := New(realRules(t), nil)
	sess := histTurns(
		userTurn("fix the typo in the README"),
		assistantTurn("Done — want me to proceed with anything else?"),
	)
	d, err := r.Route(context.Background(), sess, "go ahead", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetLocal {
		t.Errorf("proceed on small task should stay local, got %+v", d)
	}
}

func TestRoute_TailScopeDecides(t *testing.T) {
	r := New(realRules(t), nil)
	// Decision point for hard work: the tail carries the marker.
	sess := histTurns(
		userTurn("how should we handle the routing bug?"),
		assistantTurn("Option (a) a small patch, option (b) a big refactor of the dispatch layer. Which do you want?"),
	)
	d, err := r.Route(context.Background(), sess, "yes, do the big refactor", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetEscalation {
		t.Errorf("decision-point confirmation should escalate, got %+v", d)
	}
	// Over-escalation guard: small new scope stays local even mid-hard-session.
	d, err = r.Route(context.Background(), sess, "yes, list the files too", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetLocal {
		t.Errorf("small new scope must stay local, got %+v", d)
	}
}

func TestRoute_NewScopeAskStaysLocal(t *testing.T) {
	r := New(realRules(t), nil)
	sess := histTurns(
		userTurn("refactor the whole dispatch layer"),
		assistantTurn("Plan is in. Want me to also rename the test file?"),
	)
	d, err := r.Route(context.Background(), sess, "yes do it", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetLocal {
		t.Errorf("confirmation of a small new-scope ask must stay local, got %+v", d)
	}
}

func TestRoute_EscalationWaitingUnaffected(t *testing.T) {
	r := New(realRules(t), nil)
	sess := histTurns(userTurn("proceed"))
	sess.State = session.StateEscalationWaiting
	d, err := r.Route(context.Background(), sess, "yes", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != TargetEscalation || !strings.Contains(d.Reason, "ESCALATION_WAITING") {
		t.Errorf("ESCALATION_WAITING must bypass confirmation routing, got %+v", d)
	}
}

func TestRoute_NonConfirmationUnchanged(t *testing.T) {
	r := New(realRules(t), nil)
	sess := histTurns(userTurn("refactor the whole dispatch layer"))
	// An ordinary imperative prompt must not be treated as a confirmation
	// even though it overlaps the lexicon ("implement").
	d, err := r.Route(context.Background(), sess, "implement the parser rewrite", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Reason, "confirmation") {
		t.Errorf("non-confirmation prompt took the confirmation path: %+v", d)
	}
}

func TestRouterRule_ConfirmationLabel(t *testing.T) {
	for _, reason := range []string{
		"confirmation: escalate verb refactor",
		"confirmation: short confirmed content (local)",
		"confirmation signals: local-verb:list",
	} {
		if got := routerRule(reason); got != "confirmation" {
			t.Errorf("routerRule(%q) = %q, want confirmation", reason, got)
		}
	}
}
