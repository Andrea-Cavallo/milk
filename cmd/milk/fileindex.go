package main

// Candidate index for @-file completion (issue #198, ADR-0057).
//
// Every keystroke used to walk the tree with a visit budget and filter
// .gitignore afterwards, so ignored ballast (node_modules, .venv) spent the
// whole budget before the walk reached the real files. Following the other
// harnesses (OpenCode, Codex, Gemini CLI, aider, Claude Code) the candidate
// list is built once, off the keystroke path, by the ignore engine itself
// (`git ls-files --cached --others --exclude-standard`, or a bounded walk that
// prunes ballast directories while descending when git is not available), and
// every keystroke is an in-memory filter over it.

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// fileIndexMaxEntriesDefault caps the candidate list (files plus derived
	// directories) so memory stays fixed however large the tree is.
	fileIndexMaxEntriesDefault = 50000
	// fileIndexTTL is how long a snapshot is served before the next use
	// refreshes it in the background (files created mid-session appear).
	fileIndexTTL = 3 * time.Second
	// fileIndexBuildTimeout bounds one background build.
	fileIndexBuildTimeout = 5 * time.Second
	// fileIndexTabWait is how long a Tab press waits for a first build.
	fileIndexTabWait = 250 * time.Millisecond
	// fileIndexMaxIndexed is how many registry entries (working directories)
	// are kept.
	fileIndexMaxIndexed = 4
)

// fileIndexMaxEntries is the live cap (a variable so tests can lower it).
var fileIndexMaxEntries = fileIndexMaxEntriesDefault

// ballastDirs are never descended into by the non-git fallback walk: without
// a .gitignore to consult these are almost never what the user means.
var ballastDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, ".venv": {}, "venv": {}, "__pycache__": {},
	".mypy_cache": {}, ".pytest_cache": {}, ".tox": {}, "target": {}, "dist": {},
	"build": {}, "vendor": {}, ".next": {}, ".cache": {}, ".gradle": {},
	".idea": {}, ".terraform": {}, ".claude": {},
}

// fileEntry is one candidate: a path relative to the index root ("/"-separated,
// trailing "/" for directories) plus its lower-cased form and the offset of its
// base name in that form, both computed once at build time.
type fileEntry struct {
	path  string
	lower string
	base  int // offset of the base name within lower
}

type fileSnapshot struct {
	entries   []fileEntry
	truncated bool
	builtAt   time.Time
	fromGit   bool
}

type fileIndex struct {
	cwd string

	snap atomic.Pointer[fileSnapshot]

	builds atomic.Int64 // completed + running builds, for tests

	mu       sync.Mutex
	building bool
	done     chan struct{} // closed when the build in flight finishes
	dirty    bool
	onReady  func()
}

var (
	fileIndexesMu sync.Mutex
	fileIndexes   []*fileIndex
)

// indexFor returns the (shared) index for a working directory.
func indexFor(cwd string) *fileIndex {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	fileIndexesMu.Lock()
	defer fileIndexesMu.Unlock()
	for _, ix := range fileIndexes {
		if ix.cwd == cwd {
			return ix
		}
	}
	ix := &fileIndex{cwd: cwd}
	fileIndexes = append(fileIndexes, ix)
	if len(fileIndexes) > fileIndexMaxIndexed {
		fileIndexes = fileIndexes[1:]
	}
	return ix
}

// setOnReady registers a callback run after each finished build.
func (ix *fileIndex) setOnReady(f func()) {
	ix.mu.Lock()
	ix.onReady = f
	ix.mu.Unlock()
}

// invalidate marks the snapshot stale: the next use refreshes it (an agent
// turn probably created or removed files).
func (ix *fileIndex) invalidate() {
	ix.mu.Lock()
	ix.dirty = true
	ix.mu.Unlock()
}

// get returns the current snapshot without ever blocking on a refresh: a
// missing or stale snapshot starts one background build (single flight) and
// the old snapshot, if any, keeps serving. Only when there is no snapshot at
// all does it wait up to wait for the first build; nil if it is not ready.
func (ix *fileIndex) get(wait time.Duration) *fileSnapshot {
	snap := ix.snap.Load()
	ix.mu.Lock()
	stale := snap == nil || ix.dirty || time.Since(snap.builtAt) > fileIndexTTL
	var done chan struct{}
	if stale {
		if !ix.building {
			ix.building = true
			ix.dirty = false
			ix.done = make(chan struct{})
			go ix.runBuild(ix.done)
		}
		done = ix.done
	}
	ix.mu.Unlock()

	if snap == nil && wait > 0 && done != nil {
		select {
		case <-done:
		case <-time.After(wait):
		}
		snap = ix.snap.Load()
	}
	return snap
}

func (ix *fileIndex) runBuild(done chan struct{}) {
	ix.builds.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), fileIndexBuildTimeout)
	defer cancel()
	snap := buildFileSnapshot(ctx, ix.cwd)

	ix.mu.Lock()
	if snap != nil {
		ix.snap.Store(snap)
	}
	ix.building = false
	cb := ix.onReady
	ix.mu.Unlock()
	close(done)
	if snap != nil && cb != nil {
		cb()
	}
}

// buildFileSnapshot lists cwd with git when it can, otherwise with a bounded
// walk that prunes ballast directories during descent.
func buildFileSnapshot(ctx context.Context, cwd string) *fileSnapshot {
	if paths, truncated, ok := gitFileList(ctx, cwd); ok {
		return newFileSnapshot(paths, truncated, true)
	}
	paths, truncated := walkFileList(ctx, cwd)
	return newFileSnapshot(paths, truncated, false)
}

// gitFileList returns tracked plus untracked-not-ignored files below cwd,
// relative to it; ok is false when cwd is not inside a usable git work tree.
// Nested .gitignore files, .git/info/exclude and the global excludes are all
// honoured by git itself, and nothing is traversed by milk.
func gitFileList(ctx context.Context, cwd string) (paths []string, truncated, ok bool) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false, false
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		if p := filepath.ToSlash(sc.Text()); p != "" {
			paths = append(paths, p)
		}
		if len(paths) >= fileIndexMaxEntries {
			truncated = true
			break
		}
	}
	if truncated {
		_ = cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil && !truncated {
		return nil, false, false
	}
	return paths, truncated, true
}

// walkFileList is the non-git fallback: a walk bounded by entry count and the
// context deadline that never enters ballast directories.
func walkFileList(ctx context.Context, cwd string) (paths []string, truncated bool) {
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == cwd {
			return nil
		}
		if ctx.Err() != nil || len(paths) >= fileIndexMaxEntries {
			truncated = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if _, skip := ballastDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(cwd, path); err == nil {
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	return paths, truncated
}

// newFileSnapshot turns a file list into the candidate set: the files plus
// every directory above them.
func newFileSnapshot(files []string, truncated, fromGit bool) *fileSnapshot {
	dirs := make(map[string]struct{})
	entries := make([]fileEntry, 0, len(files)+len(files)/4)
	add := func(p string) {
		lower := strings.ToLower(p)
		trim := strings.TrimSuffix(lower, "/")
		entries = append(entries, fileEntry{path: p, lower: lower, base: strings.LastIndexByte(trim, '/') + 1})
	}
	for _, f := range files {
		for i := 0; i < len(f); i++ {
			if f[i] != '/' {
				continue
			}
			d := f[:i+1]
			if _, seen := dirs[d]; !seen {
				dirs[d] = struct{}{}
				add(d)
			}
		}
		add(f)
	}
	return &fileSnapshot{entries: entries, truncated: truncated, builtAt: time.Now(), fromGit: fromGit}
}

// match returns up to limit candidates under dir (a relative "a/b/" prefix, or
// "") for the base-name fragment frag, best first:
//
//	tier 0  base name starts with frag
//	tier 1  base name contains frag
//	tier 2  the path below dir contains frag (2+ characters only)
//
// then direct children before deeper entries, shorter paths first, then
// alphabetical. An empty frag lists the direct children of dir. A one-character
// frag is limited to direct children (too broad to search the tree with).
func (s *fileSnapshot) match(dir, frag string, limit int) []string {
	if limit <= 0 {
		limit = 1
	}
	dirL := strings.ToLower(dir)
	fragL := strings.ToLower(frag)
	deep := len([]rune(frag)) >= 2

	type cand struct {
		e      fileEntry
		tier   int
		direct bool
	}
	best := make([]cand, 0, limit+1)
	less := func(a, b cand) bool {
		if fragL == "" {
			return a.e.path < b.e.path
		}
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.direct != b.direct {
			return a.direct
		}
		if len(a.e.path) != len(b.e.path) {
			return len(a.e.path) < len(b.e.path)
		}
		return a.e.path < b.e.path
	}

	for _, e := range s.entries {
		if !strings.HasPrefix(e.lower, dirL) || len(e.lower) == len(dirL) {
			continue
		}
		rest := e.lower[len(dirL):]
		direct := !strings.Contains(strings.TrimSuffix(rest, "/"), "/")
		if fragL == "" {
			if !direct {
				continue
			}
			best = insertBest(best, cand{e: e, direct: true}, limit, less)
			continue
		}
		base := e.lower[e.base:]
		base = strings.TrimSuffix(base, "/")
		tier := -1
		switch {
		case strings.HasPrefix(base, fragL):
			tier = 0
		case strings.Contains(base, fragL):
			tier = 1
		case deep && strings.Contains(rest, fragL):
			tier = 2
		}
		if tier < 0 || (!deep && !direct) {
			continue
		}
		best = insertBest(best, cand{e: e, tier: tier, direct: direct}, limit, less)
	}
	out := make([]string, len(best))
	for i, c := range best {
		out[i] = c.e.path
	}
	return out
}

// insertBest keeps the limit best candidates in order, in O(limit) per
// accepted candidate — no allocation proportional to the match count.
func insertBest[T any](best []T, c T, limit int, less func(a, b T) bool) []T {
	i := sort.Search(len(best), func(i int) bool { return less(c, best[i]) })
	if i >= limit {
		return best
	}
	best = append(best, c)
	copy(best[i+1:], best[i:])
	best[i] = c
	if len(best) > limit {
		best = best[:limit]
	}
	return best
}

// atPathMatches resolves the text after "@" to completion candidates for the
// working directory cwd: at most limit, best first. wait is how long to wait
// for a first index build (0 = never wait).
//
// Paths inside cwd use the index. Absolute paths and paths that leave cwd
// ("../x") are listed one directory at a time instead — never walked.
func atPathMatches(token, cwd string, limit int, wait time.Duration) []string {
	token = strings.TrimPrefix(token, "./")
	dir, frag := "", token
	if i := strings.LastIndexByte(token, '/'); i >= 0 {
		dir, frag = token[:i+1], token[i+1:]
	}
	if filepath.IsAbs(token) || token == ".." || strings.HasPrefix(token, "../") || strings.Contains(token, "/../") {
		return listDirFlat(token, cwd, limit)
	}
	snap := indexFor(cwd).get(wait)
	if snap == nil {
		return listDirFlat(token, cwd, limit) // first build still running
	}
	// Ask for a little more than needed: tracked files deleted from the work
	// tree are still listed by git and are dropped here.
	cands := snap.match(dir, frag, limit+limit/2+4)
	if !snap.fromGit {
		return trimTo(cands, limit)
	}
	out := cands[:0]
	for _, p := range cands {
		if _, err := os.Lstat(filepath.Join(cwd, filepath.FromSlash(p))); err == nil {
			out = append(out, p)
		}
	}
	return trimTo(out, limit)
}

func trimTo(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// listDirFlat lists one directory for paths outside the index (absolute, or
// leaving cwd), or while the first build is still running: entries whose name
// starts with, or contains, the last path element. No recursion, no git.
func listDirFlat(token, cwd string, limit int) []string {
	base := token
	if !filepath.IsAbs(base) {
		base = filepath.Join(cwd, base)
	}
	dir, namePart := filepath.Dir(base), strings.ToLower(filepath.Base(base))
	if strings.HasSuffix(token, "/") || token == "" {
		dir, namePart = base, ""
	}
	if dir == "/" || dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if len(out) >= limit {
			break
		}
		if namePart != "" && !strings.Contains(strings.ToLower(e.Name()), namePart) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !filepath.IsAbs(token) {
			if rel, err := filepath.Rel(cwd, p); err == nil {
				p = rel
			}
		}
		if e.IsDir() {
			p += "/"
		}
		out = append(out, filepath.ToSlash(p))
	}
	return out
}
