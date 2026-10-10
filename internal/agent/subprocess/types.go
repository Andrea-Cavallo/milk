package subprocess

import (
	"context"
	"io"

	"github.com/scoutme/milk/internal/config"
)

// ArgBuilder constructs the CLI args for a specific subprocess agent binary.
// Each agent type (claude, smolagent, …) implements this interface.
type ArgBuilder interface {
	// Bin returns the path to the agent binary.
	Bin() string
	// BaseArgs returns flags prepended before all session-specific args.
	// For claude: ["--print", "--output-format", "stream-json", "--verbose", "--include-partial-messages"]
	// For smolagent: ["--model-type", "OpenAIModel", ...]
	BaseArgs() []string
	// FirstArgs returns the complete per-turn args for a new session, including
	// prompt injection. contextFiles holds temp file paths for context injection.
	// The prompt must be appended in whatever form the binary expects.
	FirstArgs(sessionID, prompt string, contextFiles []string) []string
	// ResumeArgs returns the complete per-turn args to continue an existing session.
	ResumeArgs(sessionID, prompt string, contextFiles []string) []string
	// EnvStrip returns environment variable name prefixes to remove from the
	// subprocess environment before launching.
	EnvStrip() []string
	// Ping performs a lightweight check that the binary is available.
	Ping() error
}

// NativeMCPBuilder is an optional ArgBuilder capability: the underlying binary
// has its own MCP client and consumes resolved MCP server connection params
// out-of-band, instead of through the context-injected tool-description block
// that agents without a native client (aider-cli) receive. Currently
// implemented by milk-smolagent, which connects via smolagents' MCPClient
// (issue #122).
type NativeMCPBuilder interface {
	// NativeMCPEnv serialises servers into whatever channel the binary reads —
	// currently a temp JSON file whose path travels in an env KEY=VALUE pair —
	// and returns that pair plus a cleanup func the caller must defer until the
	// subprocess has exited. Auth headers are resolved here (per turn, via
	// internal/mcpauth.ResolveHeader), so token_cmd/oauth servers stay fresh.
	NativeMCPEnv(ctx context.Context, servers []config.MCPServerConfig) (envKV string, cleanup func(), err error)
}

// StreamParser processes stdout from a subprocess agent.
type StreamParser interface {
	// Parse reads NDJSON lines from r, writes display text to out, and returns
	// a ParseResult when the stream ends. opts carries optional callbacks.
	Parse(r io.Reader, out io.Writer, opts ParseOpts) (ParseResult, error)
}

// ParseOpts holds optional callbacks shared by all subprocess stream parsers.
type ParseOpts struct {
	// OnText is called for each text fragment written to out, before writing.
	// Used by callers that need a copy of raw streamed text (e.g. for logging).
	OnText func(string)
	// OnPercept is called when a <milk:percept:NONCE>…</milk:percept:NONCE> tag
	// is intercepted in the response stream.
	OnPercept    func(content, consumerHint string)
	PerceptNonce string
	AgentNames   []string
	// OnNeed is called when a <milk:need:NONCE>…</milk:need:NONCE> tag is intercepted.
	OnNeed    func(content string)
	NeedNonce string
	// OnEscalate is called when a <milk:escalate:NONCE>reason</milk:escalate:NONCE> tag
	// is intercepted, signalling that the subprocess primary agent wants to hand off
	// the current turn to the escalation agent. The tag body is the reason string.
	OnEscalate    func(reason string)
	EscalateNonce string
	// DebugLog receives every raw line from the subprocess stdout when non-nil.
	DebugLog io.Writer
}

// ParseResult holds the parsed output of a subprocess agent run.
// Both claude and smolagent parsers produce this type.
type ParseResult struct {
	SessionID string
	Text      string
	EndsWithQ bool // true if the final text ends with a question mark
	IsError   bool
	// EscalationReason is non-empty when the subprocess primary agent emitted a
	// <milk:escalate:NONCE>…</milk:escalate:NONCE> tag requesting hand-off to the escalation agent.
	EscalationReason string
	// Token usage from the result event (may be zero if not reported).
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	TotalCostUSD             float64
}
