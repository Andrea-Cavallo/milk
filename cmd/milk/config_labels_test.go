package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/session"
)

// withSandboxedRoot points milk's state root at a temp dir (the --config-root
// / $MILK_CONFIG_ROOT mechanism) and parks the process cwd in another temp
// dir so no repo-local overlay leaks in. Restores both on cleanup.
func withSandboxedRoot(t *testing.T) (root, cwd string) {
	t.Helper()
	root = t.TempDir()
	prev := config.SetRoot(root)
	t.Cleanup(func() { config.SetRoot(prev) })

	cwd = t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
	return root, cwd
}

// writeLocalOverlay writes a project-local .milk/config.json under dir.
func writeLocalOverlay(t *testing.T, dir, body string) {
	t.Helper()
	milkDir := filepath.Join(dir, config.LocalConfigDir)
	if err := os.MkdirAll(milkDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(milkDir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestRenderConfigJSON_HeaderNamesRealPaths pins directive #2: the /config
// header must name the actual state-root config.json (sandboxed here) and the
// actual local-overlay path — never a hardcoded ~/.milk claim, in either the
// global-only or the merged rendering.
func TestRenderConfigJSON_HeaderNamesRealPaths(t *testing.T) {
	root, _ := withSandboxedRoot(t)
	if err := config.Save(config.Config{Agent: "x", Agents: []config.AgentConfig{{Name: "x", URL: "http://localhost:1", Model: "m", Provider: "local"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	globalPath := filepath.Join(root, "config.json")

	out, err := renderConfigJSON("")
	if err != nil {
		t.Fatalf("renderConfigJSON: %v", err)
	}
	if !strings.Contains(out, globalPath) {
		t.Errorf("header must name the real global config path %q, got %q", globalPath, firstLine(out))
	}
	if strings.Contains(out, "~/.milk") {
		t.Errorf("header must not claim ~/.milk under a sandboxed root, got %q", firstLine(out))
	}

	proj := t.TempDir()
	writeLocalOverlay(t, proj, `{"agent":"overlay"}`)
	mout, err := renderConfigJSON(proj)
	if err != nil {
		t.Fatalf("renderConfigJSON(local): %v", err)
	}
	localPath := filepath.Join(proj, config.LocalConfigDir, "config.json")
	if !strings.Contains(mout, globalPath) || !strings.Contains(mout, localPath) {
		t.Errorf("merged header must name both %q and %q, got %q", globalPath, localPath, firstLine(mout))
	}
	if strings.Contains(mout, "~/.milk") {
		t.Errorf("merged header must not claim ~/.milk under a sandboxed root, got %q", firstLine(mout))
	}
}

// TestHandleReloadCmd_MergesActiveDirOverlay pins the /reload regression: it
// used to call config.Load() — global only — silently dropping any
// project-local overlay and ignoring a session bound under another directory.
// The reload must merge against the currently selected dir (the bound
// session's cwd) exactly like every other config load.
func TestHandleReloadCmd_MergesActiveDirOverlay(t *testing.T) {
	_, launchDir := withSandboxedRoot(t)
	if err := config.Save(config.Config{Agent: "base", Agents: []config.AgentConfig{{Name: "base", URL: "http://localhost:1", Model: "m", Provider: "local"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	proj := t.TempDir()
	writeLocalOverlay(t, proj, `{"agent":"overlay"}`)

	// The session is bound under proj while the process runs from launchDir:
	// the session's dir is the selected one and must win.
	st := &interactiveState{cwd: launchDir, sess: &session.Session{CWD: proj}}
	m := model{st: st}
	_, cmd := m.handleReloadCmd()
	msg := cmd()
	rm, ok := msg.(configReloadMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want configReloadMsg", msg)
	}
	if rm.err != nil {
		t.Fatalf("reload error: %v", rm.err)
	}
	if rm.cfg.Agent != "overlay" {
		t.Errorf("reloaded agent = %q, want the session-dir overlay %q (global-only reload would drop it)", rm.cfg.Agent, "overlay")
	}

	// Sanity: the global base still merges through.
	if len(rm.cfg.Agents) != 1 || rm.cfg.Agents[0].Name != "base" {
		t.Errorf("reloaded agents = %+v, want the global base entry kept", rm.cfg.Agents)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestActiveConfigDir_PrefersBoundSessionDir pins the selection order behind
// every session-aware config load/save: the bound session's cwd is the
// currently selected directory and wins; the session-start cwd is next; "" (=
// process cwd) is the stateless fallback.
func TestActiveConfigDir_PrefersBoundSessionDir(t *testing.T) {
	if got := activeConfigDir(nil); got != "" {
		t.Errorf("activeConfigDir(nil) = %q, want \"\"", got)
	}
	if got := activeConfigDir(&interactiveState{}); got != "" {
		t.Errorf("activeConfigDir(no sess, no cwd) = %q, want \"\"", got)
	}
	if got := activeConfigDir(&interactiveState{cwd: "/launch"}); got != "/launch" {
		t.Errorf("activeConfigDir(cwd only) = %q, want /launch", got)
	}
	st := &interactiveState{cwd: "/launch", sess: &session.Session{CWD: "/selected"}}
	if got := activeConfigDir(st); got != "/selected" {
		t.Errorf("activeConfigDir(bound sess) = %q, want the session dir /selected", got)
	}
}
