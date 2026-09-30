// Package instructions loads project-level agent instructions (AGENTS.md,
// falling back to CLAUDE.md where appropriate) so milk's own agents can pick
// up repo-specific conventions the same way Claude Code, MiMo-Code, and
// OpenCode do. See docs/prompt-context-management-review.md for the gap this
// closes.
package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// maxBlockChars caps the combined instructions content injected into a
// prompt. Oversized files are truncated with a trailing note rather than
// sent in full — a repo-instructions file is not expected to be huge, and an
// unbounded read would defeat the whole point of a budgeted prompt.
const maxBlockChars = 12000

// maxSearchDepth bounds the ancestor walk as a safety net against
// pathologically deep or cyclic paths; a real repo boundary (.git) is found
// long before this in practice.
const maxSearchDepth = 25

type cacheEntry struct {
	path    string
	modTime int64
	content string
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

// Block returns a formatted "[Project instructions]" block for cwd, or "" if
// cwd is empty or no instructions file is found. It searches AGENTS.md first,
// walking from cwd up to (and including) the repository root — the first
// directory containing .git — or up to maxSearchDepth levels if no .git is
// found. When includeClaudeFallback is true and no AGENTS.md is found
// anywhere in that walk, the same walk is repeated for CLAUDE.md.
//
// includeClaudeFallback should be true for the primary (local-model) agent,
// which has no other path to a repo's CLAUDE.md, and false for the Claude CLI
// escalation path, which already loads CLAUDE.md natively — injecting it
// again there would only duplicate tokens.
func Block(cwd string, includeClaudeFallback bool) string {
	if cwd == "" {
		return ""
	}
	content := load(cwd, "AGENTS.md")
	if content == "" && includeClaudeFallback {
		content = load(cwd, "CLAUDE.md")
	}
	if content == "" {
		return ""
	}
	if len(content) > maxBlockChars {
		content = content[:maxBlockChars] + "\n... (truncated)"
	}
	var b strings.Builder
	b.WriteString("[Project instructions]\n")
	b.WriteString(content)
	b.WriteString("\n\n")
	return b.String()
}

// load finds the nearest name (e.g. "AGENTS.md") walking up from dir, and
// returns its content, using a small mtime-checked cache so a per-turn call
// doesn't re-walk the filesystem when the file hasn't changed.
func load(dir, name string) string {
	path, found := findUp(dir, name)
	if !found {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	mt := info.ModTime().UnixNano()

	cacheMu.Lock()
	if e, ok := cache[path]; ok && e.modTime == mt {
		cacheMu.Unlock()
		return e.content
	}
	cacheMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))

	cacheMu.Lock()
	cache[path] = cacheEntry{path: path, modTime: mt, content: content}
	cacheMu.Unlock()

	return content
}

// findUp walks from dir upward looking for name, stopping once the directory
// containing it has been checked, or once maxSearchDepth levels have been
// walked. Returns the full path and whether it was found.
func findUp(dir, name string) (string, bool) {
	dir = filepath.Clean(dir)
	for range maxSearchDepth {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			// Reached the repo root without finding it — stop searching further up.
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}
