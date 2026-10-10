package smolagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/mcpauth"
)

// mcpConfigEnvVar is the environment variable through which milk passes the
// path of a JSON file holding resolved MCP server connection params to
// milk-smolagent. The script reads it in _load_mcp_tools() — keep the two
// spellings in sync (issue #122).
const mcpConfigEnvVar = "MILK_MCP_CONFIG_FILE"

// Default handshake/request timeouts, mirroring internal/mcp.New so the
// smolagents path honours the same budget milk's own MCP client uses.
const (
	defaultConnectTimeout = 5 * time.Second
	defaultRequestTimeout = 30 * time.Second
)

// mcpServerEntry is one server in the JSON file scripts/milk-smolagent reads:
//
//	{"servers": [mcpServerEntry, …]}
//
// transport "stdio" → command/args; transport "http" → url (+ optional
// Authorization header). *_seconds fields feed smolagents' MCPAdapt
// adapter_kwargs (connect_timeout / client_session_timeout_seconds).
type mcpServerEntry struct {
	Name                  string            `json:"name"`
	Transport             string            `json:"transport"`
	URL                   string            `json:"url,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	Command               string            `json:"command,omitempty"`
	Args                  []string          `json:"args,omitempty"`
	ConnectTimeoutSeconds float64           `json:"connect_timeout_seconds,omitempty"`
	TimeoutSeconds        float64           `json:"timeout_seconds,omitempty"`
}

// NativeMCPEnv implements subprocess.NativeMCPBuilder: milk-smolagent connects
// to MCP servers itself via smolagents' MCPClient (issue #122).
func (b *argBuilder) NativeMCPEnv(ctx context.Context, servers []config.MCPServerConfig) (string, func(), error) {
	return writeMCPConfigFile(ctx, servers)
}

// writeMCPConfigFile serialises servers into a temp JSON file in the format
// scripts/milk-smolagent's native MCP loader reads, and returns the env
// KEY=VALUE pair passing its path to the script plus a cleanup func.
//
// Auth is resolved through internal/mcpauth.ResolveHeader — the single
// resolution path shared with internal/mcp.Client and claude-cli's
// --mcp-config generation (#115/#120) — and is best-effort: a failed resolve
// (e.g. oauth not yet authorized) leaves the server unauthenticated rather
// than failing the whole turn, mirroring the claude-cli writer.
//
// The caller must invoke cleanup after the subprocess exits.
func writeMCPConfigFile(ctx context.Context, servers []config.MCPServerConfig) (string, func(), error) {
	if len(servers) == 0 {
		return "", func() {}, nil
	}
	entries := make([]mcpServerEntry, 0, len(servers))
	for _, s := range servers {
		entry := mcpServerEntry{
			Name:                  s.Name,
			ConnectTimeoutSeconds: durationSeconds(s.ConnectTimeout, defaultConnectTimeout),
			TimeoutSeconds:        durationSeconds(s.Timeout, defaultRequestTimeout),
		}
		if strings.EqualFold(s.Transport, "stdio") {
			entry.Transport = "stdio"
			entry.Command = s.Command
			entry.Args = s.Args
		} else {
			entry.Transport = "http"
			entry.URL = s.URL
			if header, err := mcpauth.ResolveHeader(ctx, s); err == nil && header != "" {
				entry.Headers = map[string]string{"Authorization": header}
			}
		}
		entries = append(entries, entry)
	}
	data, err := json.Marshal(map[string]any{"servers": entries})
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal mcp config: %w", err)
	}
	f, err := os.CreateTemp("", "milk-mcp-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("create mcp config file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name()) //nolint:errcheck
		return "", func() {}, fmt.Errorf("write mcp config file: %w", err)
	}
	f.Close()
	return mcpConfigEnvVar + "=" + f.Name(), func() { os.Remove(f.Name()) }, nil //nolint:errcheck
}

// durationSeconds parses a config duration string ("5s", "30s") into seconds,
// falling back to def when empty or unparseable — same semantics as
// internal/mcp.New's timeout handling.
func durationSeconds(s string, def time.Duration) float64 {
	if s == "" {
		return def.Seconds()
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def.Seconds()
	}
	return d.Seconds()
}
