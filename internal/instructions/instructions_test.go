package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBlock_FindsNearestAgentsMD(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "root instructions")
	sub := filepath.Join(root, "a", "b")
	writeFile(t, filepath.Join(sub, "AGENTS.md"), "nearest instructions")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Block(sub, true)
	if !strings.Contains(got, "nearest instructions") {
		t.Errorf("expected nearest AGENTS.md content, got %q", got)
	}
	if strings.Contains(got, "root instructions") {
		t.Errorf("expected only the nearest AGENTS.md, got root content too: %q", got)
	}
}

func TestBlock_StopsAtGitBoundary(t *testing.T) {
	outer := t.TempDir()
	writeFile(t, filepath.Join(outer, "AGENTS.md"), "outer instructions — should never be seen")
	repo := filepath.Join(outer, "repo")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(repo, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Block(sub, true)
	if got != "" {
		t.Errorf("expected no instructions found within repo boundary, got %q", got)
	}
}

func TestBlock_ClaudeFallbackOnlyWhenRequested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "claude-only instructions")

	if got := Block(dir, false); got != "" {
		t.Errorf("expected no fallback when includeClaudeFallback=false, got %q", got)
	}
	if got := Block(dir, true); !strings.Contains(got, "claude-only instructions") {
		t.Errorf("expected CLAUDE.md fallback content, got %q", got)
	}
}

func TestBlock_AgentsMDTakesPrecedenceOverClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "agents content")
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "claude content")

	got := Block(dir, true)
	if !strings.Contains(got, "agents content") {
		t.Errorf("expected AGENTS.md content, got %q", got)
	}
	if strings.Contains(got, "claude content") {
		t.Errorf("expected CLAUDE.md to be ignored when AGENTS.md exists, got %q", got)
	}
}

func TestBlock_EmptyCwd(t *testing.T) {
	if got := Block("", true); got != "" {
		t.Errorf("expected empty block for empty cwd, got %q", got)
	}
}

func TestBlock_NoInstructionsFound(t *testing.T) {
	dir := t.TempDir()
	if got := Block(dir, true); got != "" {
		t.Errorf("expected empty block when no instructions file exists, got %q", got)
	}
}

func TestBlock_TruncatesOversizedContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), strings.Repeat("x", maxBlockChars+500))

	got := Block(dir, true)
	if !strings.Contains(got, "(truncated)") {
		t.Errorf("expected truncation note for oversized content, got length %d", len(got))
	}
	if len(got) > maxBlockChars+200 {
		t.Errorf("expected truncated block to stay near the cap, got %d chars", len(got))
	}
}

func TestBlock_CachesUntilMTimeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	writeFile(t, path, "version one")

	first := Block(dir, true)
	if !strings.Contains(first, "version one") {
		t.Fatalf("expected version one, got %q", first)
	}

	// Overwrite without changing mtime granularity guarantees: force a
	// distinct mtime so the cache is expected to invalidate.
	future := time.Now().Add(2 * time.Second)
	writeFile(t, path, "version two")
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	second := Block(dir, true)
	if !strings.Contains(second, "version two") {
		t.Errorf("expected updated content after mtime change, got %q", second)
	}
}
