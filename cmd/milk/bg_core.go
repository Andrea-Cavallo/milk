package main

// Background-job construction shared by the TUI and the ACP server (issue
// #188, the /bg-start and manager rows of the duplication table): how the
// session's job manager is built, and how a user-started job (/bg start) is
// planned and forked. Host-specific halves — how jobs announce themselves
// (tea.Msg vs ACP notifications), the follow-up plumbing, and the transcripts
// around the spawn — stay with the hosts.

import (
	"context"
	"io"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/session"
)

// newBackgroundManager builds the session's background-job manager (ADR-0043)
// identically for both hosts: the session-rooted context (jobs outlive the
// turn that spawned them), the configured concurrency and per-job timeout,
// and best-effort per-session state-file persistence — a home dir milk can't
// write to just means no persistence, never a failed session. onStart/
// onDone/onBatchDone are the hosts' own (tea.Msg vs ACP notifications) and are
// wired by the callers.
func newBackgroundManager(ctx context.Context, cfg config.Config, sessID string) *local.Manager {
	mgr := local.NewManager(ctx, cfg.EffectiveMaxBackgroundAgents())
	mgr.SetJobTimeout(cfg.EffectiveBackgroundAgentTimeout())
	if dir, err := config.Dir(); err == nil && sessID != "" {
		mgr.SetStateFile(dir + "/jobs/" + sessID + ".json")
	}
	return mgr
}

// userJobUnavailableMsg is the shared "nothing to fork from" report of
// userJobPlan, in both hosts' words.
const userJobUnavailableMsg = "background agents unavailable — no inference-server-backed agent configured"

// userJobPlan picks what a user-started background job (/bg start) runs on:
// the escalation agent's local backend when there is one (generally the more
// capable one), else the primary's — unlike an agent-initiated
// spawn_background_agent, the in-flight turn may well be running on
// claude-cli, which has no Manager at all, so the job just needs any
// inference-server-backed agent to fork from. modelName falls back to the
// agent's configured name; label is the task truncated to 60 chars (the
// panel/summary width both hosts render). ok=false when nothing is forkable —
// the caller reports userJobUnavailableMsg.
func userJobPlan(cfg config.Config, escLocal, primaryLocal *local.Agent, task string) (agent *local.Agent, modelName, label string, ok bool) {
	agent = escLocal
	ac := cfg.EscalationAgentConfig()
	if agent == nil {
		agent = primaryLocal
		ac = cfg.ActiveAgent()
	}
	if agent == nil {
		return nil, "", "", false
	}
	modelName = ac.Model
	if modelName == "" {
		modelName = ac.Name
	}
	label = task
	if len(label) > 60 {
		label = label[:57] + "..."
	}
	return agent, modelName, label, true
}

// userJobRunner builds the spawn body both hosts pass to Manager.Spawn for a
// user-started job: fork the planned agent on the task text, persisting
// through the session's memory store.
func userJobRunner(agent *local.Agent, cwd, task string, mem *memory.Store) func(context.Context, string, io.Writer) (string, session.TokenUsage, error) {
	return func(ctx context.Context, jobID string, out io.Writer) (string, session.TokenUsage, error) {
		return agent.RunBackgroundTask(ctx, jobID, cwd, task, "", out, mem)
	}
}
