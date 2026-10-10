package subprocess

import (
	"context"
	"io"

	"github.com/scoutme/milk/internal/config"
)

// Agent is a generic subprocess-backed agent. Provider packages (aider, smolagent)
// construct one with their ArgBuilder and StreamParser and expose it directly.
// All fields are immutable after construction; With* methods return copies.
type Agent struct {
	runner *Runner
	// callbacks
	onPercept     func(content, consumerHint string)
	perceptNonce  string
	agentNames    []string
	onNeed        func(content string)
	needNonce     string
	onEscalate    func(reason string)
	escalateNonce string
}

// NewAgent constructs an Agent wrapping b and p.
func NewAgent(b ArgBuilder, p StreamParser) *Agent {
	return &Agent{runner: New(b, p)}
}

// Ping checks whether the underlying binary is available.
func (a *Agent) Ping() error { return a.runner.Ping() }

// SupportsNativeMCP reports whether the underlying binary has its own MCP
// client and therefore receives resolved MCP server connection params
// out-of-band (env + temp config file) rather than as a context-injected
// tool-description block (issue #122). True for milk-smolagent, false for
// aider-cli.
func (a *Agent) SupportsNativeMCP() bool {
	_, ok := a.runner.builder.(NativeMCPBuilder)
	return ok
}

// NativeMCPEnv delegates to the ArgBuilder's native-MCP capability. ok=false
// means the binary has no native MCP client — the caller falls back to
// context injection (the aider path). Callers should defer cleanup when ok.
func (a *Agent) NativeMCPEnv(ctx context.Context, servers []config.MCPServerConfig) (envKV string, cleanup func(), ok bool, err error) {
	b, ok := a.runner.builder.(NativeMCPBuilder)
	if !ok {
		return "", func() {}, false, nil
	}
	envKV, cleanup, err = b.NativeMCPEnv(ctx, servers)
	return envKV, cleanup, true, err
}

// WithDebugLog returns a copy of the agent that writes every raw stdout line to w.
func (a *Agent) WithDebugLog(w io.Writer) *Agent {
	c := *a
	c.runner = a.runner.WithDebugLog(w)
	return &c
}

// WithLogContext enables logging of static/dynamic context and prompt at DEBUG level.
func (a *Agent) WithLogContext(v bool) *Agent {
	c := *a
	c.runner = a.runner.WithLogContext(v)
	return &c
}

// WithExtraEnv returns a copy of the agent with extra KEY=VALUE env pairs.
func (a *Agent) WithExtraEnv(pairs ...string) *Agent {
	c := *a
	c.runner = a.runner.WithExtraEnv(pairs...)
	return &c
}

// WithOnPercept wires a percept callback.
func (a *Agent) WithOnPercept(fn func(content, consumerHint string), nonce, primaryName, escalationName string) *Agent {
	c := *a
	c.onPercept = fn
	c.perceptNonce = nonce
	c.agentNames = []string{primaryName, escalationName}
	return &c
}

// WithOnNeed wires a need-tag callback.
func (a *Agent) WithOnNeed(fn func(content string), nonce string) *Agent {
	c := *a
	c.onNeed = fn
	c.needNonce = nonce
	return &c
}

// WithOnEscalate wires an escalation-tag callback called when the subprocess
// primary emits <milk:escalate:NONCE>reason</milk:escalate:NONCE>.
func (a *Agent) WithOnEscalate(fn func(reason string), nonce string) *Agent {
	c := *a
	c.onEscalate = fn
	c.escalateNonce = nonce
	return &c
}

// RunFirst runs the first turn of a new session and returns the session ID.
func (a *Agent) RunFirst(ctx context.Context, staticContext, dynamicContext, prompt string, out io.Writer) (string, ParseResult, error) {
	return a.runner.RunFirst(ctx, staticContext, dynamicContext, prompt, a.parseOpts(), out)
}

// RunResume continues an existing session.
func (a *Agent) RunResume(ctx context.Context, sessionID, staticContext, dynamicContext, prompt string, out io.Writer) (ParseResult, error) {
	return a.runner.RunResume(ctx, sessionID, staticContext, dynamicContext, prompt, a.parseOpts(), out)
}

func (a *Agent) parseOpts() ParseOpts {
	return ParseOpts{
		OnPercept:     a.onPercept,
		PerceptNonce:  a.perceptNonce,
		AgentNames:    a.agentNames,
		OnNeed:        a.onNeed,
		NeedNonce:     a.needNonce,
		OnEscalate:    a.onEscalate,
		EscalateNonce: a.escalateNonce,
	}
}
