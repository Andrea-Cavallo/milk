package main

import (
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/loop"
)

func userJobPlanConfig() config.Config {
	return config.Config{
		Agent:           "primary",
		EscalationAgent: "esc",
		Agents: []config.AgentConfig{
			{Name: "primary", URL: "http://localhost:1", Model: "p-model", Provider: "local"},
			{Name: "esc", URL: "http://localhost:2", Provider: "local"}, // no model — name fallback
		},
	}
}

// TestUserJobPlan pins the shared /bg-start plan (issue #188): escalation-
// local preferred, primary-local fallback, model-name fallback, 60-char
// label, and the unavailability case both hosts report identically.
func TestUserJobPlan(t *testing.T) {
	cfg := userJobPlanConfig()
	esc, prim := local.New("", ""), local.New("", "")

	agent, modelName, label, ok := userJobPlan(cfg, esc, prim, strings.Repeat("x", 80))
	if !ok || agent != esc {
		t.Errorf("prefer escalation-local: ok=%v agent==esc %v", ok, agent == esc)
	}
	if modelName != "esc" {
		t.Errorf("modelName = %q, want the configured name fallback (no Model set)", modelName)
	}
	if len(label) != 60 || !strings.HasSuffix(label, "...") {
		t.Errorf("label = %q, want a 60-char truncation", label)
	}

	// Primary fallback + model set.
	agent, modelName, _, ok = userJobPlan(cfg, nil, prim, "t")
	if !ok || agent != prim || modelName != "p-model" {
		t.Errorf("primary fallback: ok=%v agent==prim %v model=%q", ok, agent == prim, modelName)
	}

	// Nothing forkable.
	if _, _, _, ok := userJobPlan(cfg, nil, nil, "t"); ok {
		t.Error("expected ok=false with no local agents at all")
	}
}

// TestExecSkipPermsCore pins the shared /skip-permissions grammar (issue
// #188): on/off set, empty reports, anything else is a usage line that
// changes nothing.
func TestExecSkipPermsCore(t *testing.T) {
	cases := []struct {
		sub      string
		cur      bool
		wantNext bool
		wantPart string
	}{
		{"on", false, true, "ON"},
		{"off", true, false, "OFF"},
		{"", true, true, "is on"},
		{"", false, false, "is off"},
		{"bogus", true, true, "usage:"},
		{"bogus", false, false, "usage:"},
	}
	for _, c := range cases {
		next, msg := execSkipPermsCore(c.sub, c.cur)
		if next != c.wantNext || !strings.Contains(msg, c.wantPart) {
			t.Errorf("execSkipPermsCore(%q, %v) = (%v, %q), want (%v, …%q…)", c.sub, c.cur, next, msg, c.wantNext, c.wantPart)
		}
	}
}

// TestLoopVerdictMsg pins the shared loop-verdict message rules (issue #188):
// the token_velocity escalation suggestion only when the session isn't
// already headed to escalation, and the auto-interrupt marker.
func TestLoopVerdictMsg(t *testing.T) {
	v := loop.Verdict{Message: "burning tokens fast", Signal: loop.SignalTokenVelocity}
	if got := loopVerdictMsg(v, false); !strings.Contains(got, "consider /escalate") {
		t.Errorf("unpinned token_velocity = %q, want the escalate suggestion", got)
	}
	if got := loopVerdictMsg(v, true); strings.Contains(got, "consider /escalate") {
		t.Errorf("pinned token_velocity = %q, want no suggestion", got)
	}
	interrupting := loop.Verdict{Message: "chunk repeating", ShouldInterrupt: true}
	if got := loopVerdictMsg(interrupting, false); !strings.Contains(got, "turn auto-interrupted") {
		t.Errorf("interrupting verdict = %q, want the auto-interrupt marker", got)
	}
}
