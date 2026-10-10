package smolagent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/config"
)

// readConfigFile parses the temp JSON file writeMCPConfigFile produced.
func readConfigFile(t *testing.T, envKV string) (map[string]any, string) {
	t.Helper()
	if !strings.HasPrefix(envKV, mcpConfigEnvVar+"=") {
		t.Fatalf("envKV %q: want prefix %q", envKV, mcpConfigEnvVar+"=")
	}
	path := strings.TrimPrefix(envKV, mcpConfigEnvVar+"=")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config file: %v", err)
	}
	return cfg, path
}

func TestWriteMCPConfigFile_StdioServer(t *testing.T) {
	servers := []config.MCPServerConfig{
		{Name: "local-tools", Transport: "stdio", Command: "uvx", Args: []string{"mcp-server-time"}},
	}
	envKV, cleanup, err := writeMCPConfigFile(context.Background(), servers)
	if err != nil {
		t.Fatalf("writeMCPConfigFile: %v", err)
	}
	defer cleanup()

	cfg, path := readConfigFile(t, envKV)
	defer os.Remove(path) //nolint:errcheck
	entries, ok := cfg["servers"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("want 1 server entry, got %#v", cfg["servers"])
	}
	entry := entries[0].(map[string]any)
	if entry["transport"] != "stdio" {
		t.Errorf("transport: want stdio, got %v", entry["transport"])
	}
	if entry["command"] != "uvx" {
		t.Errorf("command: want uvx, got %v", entry["command"])
	}
	if entry["url"] != nil {
		t.Errorf("stdio entry must have no url field, got %v", entry["url"])
	}
	// Default timeouts must be present (feed MCPAdapt's adapter_kwargs).
	if entry["connect_timeout_seconds"] != defaultConnectTimeout.Seconds() {
		t.Errorf("connect_timeout_seconds: want %v, got %v", defaultConnectTimeout.Seconds(), entry["connect_timeout_seconds"])
	}
	if entry["timeout_seconds"] != defaultRequestTimeout.Seconds() {
		t.Errorf("timeout_seconds: want %v, got %v", defaultRequestTimeout.Seconds(), entry["timeout_seconds"])
	}
}

func TestWriteMCPConfigFile_HTTPServer_Unauthenticated(t *testing.T) {
	servers := []config.MCPServerConfig{
		{Name: "plain-http", Transport: "http", URL: "http://127.0.0.1:8000/mcp"},
	}
	envKV, cleanup, err := writeMCPConfigFile(context.Background(), servers)
	if err != nil {
		t.Fatalf("writeMCPConfigFile: %v", err)
	}
	defer cleanup()

	cfg, path := readConfigFile(t, envKV)
	defer os.Remove(path) //nolint:errcheck
	entry := cfg["servers"].([]any)[0].(map[string]any)
	if entry["transport"] != "http" {
		t.Errorf("transport: want http, got %v", entry["transport"])
	}
	if entry["url"] != "http://127.0.0.1:8000/mcp" {
		t.Errorf("url: got %v", entry["url"])
	}
	if entry["command"] != nil {
		t.Errorf("http entry must have no command field, got %v", entry["command"])
	}
	if headers := entry["headers"]; headers != nil {
		t.Errorf("no auth configured → no headers expected, got %v", headers)
	}
}

func TestWriteMCPConfigFile_BearerAuthResolved(t *testing.T) {
	servers := []config.MCPServerConfig{
		{Name: "authed", Transport: "http", URL: "https://example.com/mcp", Auth: "bearer", APIKey: "secret-123"},
	}
	envKV, cleanup, err := writeMCPConfigFile(context.Background(), servers)
	if err != nil {
		t.Fatalf("writeMCPConfigFile: %v", err)
	}
	defer cleanup()

	cfg, path := readConfigFile(t, envKV)
	defer os.Remove(path) //nolint:errcheck
	entry := cfg["servers"].([]any)[0].(map[string]any)
	headers, _ := entry["headers"].(map[string]any)
	if headers == nil || headers["Authorization"] != "Bearer secret-123" {
		t.Errorf("Authorization header: want %q, got %v", "Bearer secret-123", headers)
	}
}

func TestWriteMCPConfigFile_EmptyServers(t *testing.T) {
	envKV, cleanup, err := writeMCPConfigFile(context.Background(), nil)
	if err != nil {
		t.Fatalf("writeMCPConfigFile: %v", err)
	}
	cleanup()
	if envKV != "" {
		t.Errorf("empty server list must yield empty envKV, got %q", envKV)
	}
}

func TestWriteMCPConfigFile_CleanupRemovesFile(t *testing.T) {
	servers := []config.MCPServerConfig{{Name: "x", Transport: "stdio", Command: "true"}}
	envKV, cleanup, err := writeMCPConfigFile(context.Background(), servers)
	if err != nil {
		t.Fatalf("writeMCPConfigFile: %v", err)
	}
	_, path := readConfigFile(t, envKV)
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file must be removed by cleanup, stat err=%v", err)
	}
}

func TestDurationSeconds(t *testing.T) {
	const def = 5 * time.Second
	cases := []struct {
		in   string
		want float64
	}{
		{"", 5},
		{"10s", 10},
		{"1m", 60},
		{"nonsense", 5},
		{"-3s", 5},
	}
	for _, c := range cases {
		if got := durationSeconds(c.in, def); got != c.want {
			t.Errorf("durationSeconds(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNativeMCPEnv_ImplementsBuilder(t *testing.T) {
	// Compile-time-ish check: the smolagent argBuilder must satisfy
	// subprocess.NativeMCPBuilder or Agent.SupportsNativeMCP() silently
	// returns false and every smolagent turn falls back to context injection.
	b := newArgBuilder(config.AgentConfig{Name: "sa", Provider: "smolagent"})
	if _, _, err := b.NativeMCPEnv(context.Background(), []config.MCPServerConfig{
		{Name: "x", Transport: "stdio", Command: "true"},
	}); err != nil {
		t.Fatalf("NativeMCPEnv: %v", err)
	}
}
