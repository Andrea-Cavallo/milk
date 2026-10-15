package main

import (
	"fmt"
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

// TestUserJobPlanFor pins explicit agent choice for user-started jobs: aliases
// and names reuse the session's wired agents, other agents come from build,
// blank falls back to background_agent then the escalation-first default, and
// non-inference-server agents are rejected.
func TestUserJobPlanFor(t *testing.T) {
	cfg := userJobPlanConfig()
	cfg.Agents = append(cfg.Agents,
		config.AgentConfig{Name: "third", URL: "http://localhost:3", Model: "t-model", Provider: "local"},
		config.AgentConfig{Name: "cli", Provider: "claude-cli"},
	)
	esc, prim, third := local.New("", ""), local.New("", ""), local.New("", "")
	built := 0
	build := func(name string) (*local.Agent, error) {
		built++
		if name != "third" {
			t.Errorf("build called for %q", name)
		}
		return third, nil
	}

	cases := []struct {
		name, spec string
		want       *local.Agent
		model      string
	}{
		{"blank defaults to escalation", "", esc, "esc"},
		{"primary alias", "primary", prim, "p-model"},
		{"escalation alias", "Escalation", esc, "esc"},
		{"escalation by name reuses wired agent", "esc", esc, "esc"},
		{"other agent is built", "third", third, "t-model"},
	}
	for _, c := range cases {
		agent, model, _, err := userJobPlanFor(cfg, esc, prim, build, c.spec, "task")
		if err != nil || agent != c.want || model != c.model {
			t.Errorf("%s: agent ok=%v model=%q err=%v, want model %q", c.name, agent == c.want, model, err, c.model)
		}
	}
	if built != 1 {
		t.Errorf("build called %d times, want only for the non-session agent", built)
	}

	for _, spec := range []string{"cli", "nope"} {
		if _, _, _, err := userJobPlanFor(cfg, esc, prim, build, spec, "t"); err == nil {
			t.Errorf("spec %q: expected an error", spec)
		}
	}

	// background_agent is the default when no spec is given.
	cfg.BackgroundAgent = "primary"
	if agent, _, _, err := userJobPlanFor(cfg, esc, prim, build, "", "t"); err != nil || agent != prim {
		t.Errorf("background_agent default: err=%v agent==prim %v", err, agent == prim)
	}
	// An explicit spec still wins over it.
	if agent, _, _, err := userJobPlanFor(cfg, esc, prim, build, "escalation", "t"); err != nil || agent != esc {
		t.Errorf("explicit spec over background_agent: err=%v agent==esc %v", err, agent == esc)
	}
	// Nothing forkable at all.
	cfg.BackgroundAgent = ""
	if _, _, _, err := userJobPlanFor(cfg, nil, nil, nil, "", "t"); err == nil {
		t.Error("expected unavailable error with no local agents")
	}
	// A local escalation agent the session hasn't wired is built on demand; if
	// that fails the error surfaces instead of silently picking another agent.
	failBuild := func(string) (*local.Agent, error) { return nil, fmt.Errorf("boom") }
	if _, _, _, err := userJobPlanFor(cfg, nil, prim, failBuild, "escalation", "t"); err == nil {
		t.Error("failed build should error, not fall back")
	}
}

func TestParseBgStartArgs(t *testing.T) {
	cases := []struct {
		in, agent, task string
		wantErr         bool
	}{
		{" fix the bug ", "", "fix the bug", false},
		{" --agent gemma list tools", "gemma", "list tools", false},
		{"--agent=gemma list tools", "gemma", "list tools", false},
		{"--agent", "", "", true},
		{"--agent=", "", "", true},
		{"do x --agent y", "", "do x --agent y", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		agent, task, err := parseBgStartArgs(c.in)
		if agent != c.agent || task != c.task || (err != nil) != c.wantErr {
			t.Errorf("parseBgStartArgs(%q) = %q, %q, %v; want %q, %q, err=%v", c.in, agent, task, err, c.agent, c.task, c.wantErr)
		}
	}
}

func TestBgAgentCandidatesAndWizardChoice(t *testing.T) {
	cfg := userJobPlanConfig()
	cfg.Agents = append(cfg.Agents, config.AgentConfig{Name: "cli", Provider: "claude-cli"})
	names := bgAgentCandidates(cfg)
	if len(names) != 2 || names[0] != "primary" || names[1] != "esc" {
		t.Errorf("candidates = %v, want [primary esc]", names)
	}
	w := &bgAgentWizardState{names: names}
	for in, want := range map[string]bool{"": true, "ESC": true, "primary": true, "escalation": true, "cli": false, "zzz": false} {
		if w.validChoice(in) != want {
			t.Errorf("validChoice(%q) = %v, want %v", in, !want, want)
		}
	}
	if got := userJobDefaultName(cfg, true); got != "esc" {
		t.Errorf("default with escalation-local = %q", got)
	}
	if got := userJobDefaultName(cfg, false); got != "primary" {
		t.Errorf("default without escalation-local = %q", got)
	}
	cfg.BackgroundAgent = "primary"
	if got := userJobDefaultName(cfg, true); got != "primary" {
		t.Errorf("configured default = %q", got)
	}
}
