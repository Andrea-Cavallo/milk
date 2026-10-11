package main

import (
	"context"
	"testing"

	"github.com/scoutme/milk/internal/config"
)

// TestAgentConfigChanged pins what triggers a live runner rebuild on config
// hot-reload: only changes that affect what rebuildRunners constructs.
func TestAgentConfigChanged(t *testing.T) {
	base := config.Config{
		Agents: []config.AgentConfig{{Name: "a", Provider: "local", URL: "http://x", Model: "m"}},
	}
	if agentConfigChanged(base, base) {
		t.Error("identical configs must not report an agent change")
	}

	edited := base
	edited.Agents = []config.AgentConfig{{Name: "a", Provider: "local", URL: "http://y", Model: "m"}}
	if !agentConfigChanged(base, edited) {
		t.Error("agent URL edit must report an agent change")
	}

	sel := base
	sel.EscalationAgent = "claude"
	if !agentConfigChanged(base, sel) {
		t.Error("escalation_agent edit must report an agent change")
	}

	tools := base
	tools.AgentTools = []config.AgentToolEntry{{Agent: "a"}}
	if !agentConfigChanged(base, tools) {
		t.Error("agent-as-tool edit must report an agent change")
	}

	meta := base
	meta.UpdateLastCheck = "2026-01-01T00:00:00Z"
	meta.DirectBash = true
	if agentConfigChanged(base, meta) {
		t.Error("non-agent fields must not report an agent change")
	}
}

// TestRebuildRunners_SubprocessProviders pins the live-rebuild coverage the
// old /agent switch and /agent add paths were missing: subprocess providers
// (smolagent/aider-cli) must rebuild without a restart, for both roles.
func TestRebuildRunners_SubprocessProviders(t *testing.T) {
	cfg := config.Config{
		Agent: "sm",
		Agents: []config.AgentConfig{
			{Name: "sm", Provider: "subprocess", Bin: "/bin/true", Model: "m"},
			{Name: "aid", Provider: "aider-cli", Bin: "/bin/true"},
		},
		EscalationAgent: "aid",
	}
	m := model{ctx: context.Background(), st: &interactiveState{cfg: cfg, cwd: t.TempDir()}}
	m, _ = m.rebuildRunners()

	if _, ok := m.agents.primary.(*subprocessRunner); !ok {
		t.Fatalf("primary runner = %T, want *subprocessRunner", m.agents.primary)
	}
	if m.agents.subprocessPrimary == nil {
		t.Error("subprocessPrimary must be set for a subprocess primary")
	}
	if m.agents.local != nil {
		t.Error("local agent must be cleared when the primary is a subprocess provider")
	}
	if _, ok := m.agents.escalation.(*subprocessRunner); !ok {
		t.Fatalf("escalation runner = %T, want *subprocessRunner", m.agents.escalation)
	}
	if m.agents.subprocessAgent == nil {
		t.Error("subprocessAgent must be set for a subprocess escalation")
	}
	if m.agents.escalationLocal != nil {
		t.Error("escalationLocal must be cleared when the escalation agent is a subprocess provider")
	}
}

// TestRebuildRunners_LocalPrimary pins the /config init first-agent case: a
// local (HTTP) primary must end up wired as the dispatch target with the
// router rebuilt against it — no restart required.
func TestRebuildRunners_LocalPrimary(t *testing.T) {
	cfg := config.Config{
		Agent: "qwen",
		Agents: []config.AgentConfig{
			{Name: "qwen", Provider: "local", URL: "://invalid", Model: "qwen2.5-coder"},
		},
	}
	m := model{ctx: context.Background(), st: &interactiveState{cfg: cfg, cwd: t.TempDir()}}
	m, _ = m.rebuildRunners()

	if m.agents.local == nil {
		t.Fatal("local agent must be built for a local primary")
	}
	if _, ok := m.agents.primary.(*localRunner); !ok {
		t.Fatalf("primary runner = %T, want *localRunner", m.agents.primary)
	}
	if m.agents.localAvail {
		t.Error("a primary pointing at an unreachable server must not report available")
	}
	if m.rtr == nil {
		t.Error("router must be rebuilt against the new primary")
	}
}

// TestRefreshMCPForRole_CLIPrimaryDoesNotClobberEscalation guards the role
// handling in the cliRunner case: refreshing the primary's MCP server list
// must swap m.agents.primary, never m.agents.escalation.
func TestRefreshMCPForRole_CLIPrimaryDoesNotClobberEscalation(t *testing.T) {
	ac := config.AgentConfig{Name: "claude", Provider: "claude-cli", MCPServers: []string{"srv"}}
	cfg := config.Config{
		Agent:           "claude",
		Agents:          []config.AgentConfig{ac},
		MCPServers:      []config.MCPServerConfig{{Name: "srv", URL: "http://127.0.0.1:1/mcp"}},
		EscalationAgent: "claude",
	}
	primary := newCLIRunner(newCLIAgent(ac), "claude", permContext{}, nil)
	esc := newCLIRunner(newCLIAgent(ac), "claude", permContext{}, nil)
	m := model{
		ctx: context.Background(),
		st:  &interactiveState{cfg: cfg, cwd: t.TempDir()},
		agents: dispatchAgents{
			primary:    primary,
			escalation: esc,
		},
	}

	m2, changed := m.refreshMCPForRole(RolePrimary, "claude")
	if !changed {
		t.Fatal("refresh must report a change when servers were never seen")
	}
	if m2.agents.escalation != TurnRunner(esc) {
		t.Error("primary refresh must not touch the escalation runner")
	}
	if m2.agents.primary == TurnRunner(primary) {
		t.Error("primary runner must be swapped for one carrying the server list")
	}
}
