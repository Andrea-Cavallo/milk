package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/session"
)

// projectMapTestIsolate points the persistent index at a temp state root and
// empties the in-memory cache, so tests neither touch ~/.milk nor leak cache
// state into each other.
func projectMapTestIsolate(t *testing.T) {
	t.Helper()
	prev := config.SetRoot(t.TempDir())
	t.Cleanup(func() { config.SetRoot(prev) })
	projectMapCache.Lock()
	defer projectMapCache.Unlock()
	projectMapCache.m = map[string]projectMapEntry{}
	projectMapCache.roots = map[string]*projectMapIndex{}
	projectMapCache.parseCalls = 0
}

// projectMapFixture writes a small mixed tree: a Go package with a struct,
// a method, a func with a unique body sentinel, consts and vars; a second
// package; and a non-Go file.
func projectMapFixture(t *testing.T) string {
	t.Helper()
	projectMapTestIsolate(t)
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg1/a.go", `package pkg1

// Config is a config.
type Config struct {
	Path string
}

type Store struct{ m map[string]string }

const ModeFast = "fast"

var Default *Config

// Load loads.
func Load(path string) (*Config, error) {
	const bodySentinel = "BODY_SENTINEL_MARKER"
	_ = bodySentinel
	return &Config{Path: path}, nil
}

func (s *Store) Get(key string) (string, error) {
	return s.m[key], nil // BODY_SENTINEL_METHOD
}
`)
	write("pkg1/b.go", `package pkg1

const (
	B1 = iota
	B2
)

var Y int
`)
	write("pkg2/c.go", `package pkg2

type Handler interface {
	Serve(x int) error
}

type Fn func(string) error
`)
	write("docs/guide.md", "# guide\n")
	return root
}

func projectMapCall(t *testing.T, root, argsJSON string) string {
	t.Helper()
	res, _ := dispatchTool(context.Background(), "project_map", argsJSON,
		&session.Session{CWD: root}, nil, memory.ConsumerAll, "", nil)
	if isToolError(res) {
		t.Fatalf("project_map errored: %s", res)
	}
	var r toolResult
	if err := json.Unmarshal([]byte(res), &r); err != nil {
		t.Fatalf("bad result JSON %q: %v", res, err)
	}
	return r.Output
}

func TestProjectMap_OutlineSignaturesNoBodies(t *testing.T) {
	root := projectMapFixture(t)
	out := projectMapCall(t, root, `{}`)

	for _, want := range []string{
		"pkg1/", "pkg2/", "docs/",
		"a.go", "guide.md",
		"type Config struct", "type Store struct",
		"func Load(path string) (*Config, error)",
		"func (s *Store) Get(key string) (string, error)",
		"type Handler interface", "type Fn func(string) error",
		"const ModeFast", "var Default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "BODY_SENTINEL") {
		t.Errorf("outline leaked a function body:\n%s", out)
	}
}

func TestProjectMap_BudgetDegradesAndStaysBounded(t *testing.T) {
	root := projectMapFixture(t)
	out := projectMapCall(t, root, `{"max_chars": 220}`)
	if len(out) > 220 {
		t.Errorf("output must stay within max_chars: %d > 220", len(out))
	}
	if !strings.Contains(out, "omitted to fit the budget") {
		t.Errorf("degraded output must say so:\n%s", out)
	}
	// Even degraded, the file tree survives.
	if !strings.Contains(out, "a.go") {
		t.Errorf("file tree must survive degradation:\n%s", out)
	}
}

func TestProjectMap_MaxFilesTruncationNote(t *testing.T) {
	root := projectMapFixture(t)
	out := projectMapCall(t, root, `{"max_files": 3}`)
	if !strings.Contains(out, "more file") {
		t.Errorf("expected a truncation note:\n%s", out)
	}
	if strings.Contains(out, "c.go") {
		t.Errorf("beyond-cap file must be dropped (lexicographic tail):\n%s", out)
	}
}

func TestProjectMap_CacheInvalidatesOnChange(t *testing.T) {
	root := projectMapFixture(t)
	a := filepath.Join(root, "pkg1", "a.go")

	out := projectMapCall(t, root, `{"path": "pkg1"}`)
	if !strings.Contains(out, "func Load") {
		t.Fatalf("fixture not mapped:\n%s", out)
	}

	newSrc := `package pkg1

func Rewritten(marker string) string { return marker } // BODY_SENTINEL_MARKER
`
	if err := os.WriteFile(a, []byte(newSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// Push mtime forward explicitly — same-second rewrites must invalidate too.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}

	out = projectMapCall(t, root, `{"path": "pkg1"}`)
	if !strings.Contains(out, "func Rewritten(marker string) string") {
		t.Errorf("changed file must be re-parsed:\n%s", out)
	}
	if strings.Contains(out, "func Load") {
		t.Errorf("stale cache served old symbols:\n%s", out)
	}
}

func TestProjectMap_SingleFileScope(t *testing.T) {
	root := projectMapFixture(t)
	out := projectMapCall(t, root, `{"path": "pkg2/c.go"}`)
	if !strings.Contains(out, "c.go") || !strings.Contains(out, "type Handler interface") {
		t.Errorf("single-file scope must map that file:\n%s", out)
	}
	if strings.Contains(out, "a.go") {
		t.Errorf("single-file scope leaked other files:\n%s", out)
	}
}

func TestProjectMap_NoBodyLeakOnParseFailure(t *testing.T) {
	projectMapTestIsolate(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := projectMapCall(t, root, `{}`)
	if !strings.Contains(out, "broken.go") {
		t.Errorf("unparseable file must still be listed:\n%s", out)
	}
}

func TestProjectMap_Framing(t *testing.T) {
	projectMapTestIsolate(t)
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/thing\n\ngo 1.26\n")
	write("README.md", "# thing\n")
	write("Taskfile.yml", "version: '3'\n")
	write("docs/guide.md", "# guide\n")
	write("pkg/code.go", "package pkg\n\nfunc Do() {}\n")

	out := projectMapCall(t, root, `{}`)
	for _, want := range []string{
		"project: example.com/thing (go)",
		"docs: README.md, docs/guide.md",
		"build/config: Taskfile.yml, go.mod",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("framing missing %q\n---\n%s", want, out)
		}
	}
}

func TestProjectMap_FramingAbsentWithoutManifest(t *testing.T) {
	projectMapTestIsolate(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := projectMapCall(t, root, `{}`)
	if strings.Contains(out, "project:") {
		t.Errorf("no manifest — no project line expected:\n%s", out)
	}
}

// projectMapNoFixture is a one-Go-file tree (isolation included).
func projectMapNoFixture(t *testing.T) string {
	t.Helper()
	projectMapTestIsolate(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestProjectMap_PersistentIndexSurvivesRestart(t *testing.T) {
	projectMapTestIsolate(t)
	root := projectMapNoFixture(t)

	first := projectMapCall(t, root, `{}`)

	// The index must exist on disk after a parse.
	base := projectMapSanitizeName(filepath.Base(root))
	entries, err := filepath.Glob(filepath.Join(mustConfigDir(t), "outline-cache", base+"-*.json"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one index file, got %v (err=%v)", entries, err)
	}

	// Simulate a fresh process: wipe the in-memory cache only.
	projectMapCache.Lock()
	projectMapCache.m = map[string]projectMapEntry{}
	projectMapCache.roots = map[string]*projectMapIndex{}
	projectMapCache.parseCalls = 0
	projectMapCache.Unlock()

	second := projectMapCall(t, root, `{}`)
	if second != first {
		t.Errorf("disk-hydrated map differs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	projectMapCache.Lock()
	calls := projectMapCache.parseCalls
	projectMapCache.Unlock()
	if calls != 0 {
		t.Errorf("unchanged tree must hydrate from disk without parsing, parsed %d", calls)
	}

	// A changed file must re-parse exactly that one file — and update the index.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(root, "a.go"), future, future); err != nil {
		t.Fatal(err)
	}
	out := projectMapCall(t, root, `{}`)
	if !strings.Contains(out, "func Beta") {
		t.Errorf("change must re-parse:\n%s", out)
	}
	projectMapCache.Lock()
	calls = projectMapCache.parseCalls
	projectMapCache.Unlock()
	if calls != 1 {
		t.Errorf("exactly one re-parse expected, got %d", calls)
	}
}

func mustConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProjectMap_RankedPruningKeepsReferencedSymbols(t *testing.T) {
	projectMapTestIsolate(t)
	root := t.TempDir()
	// Hot is referenced across the tree; Cold is dead. Both live in the same
	// file so per-file "keep at least one" cannot mask the ranking.
	src := "package a\n\nfunc Hot() {}\n\nfunc Cold() {}\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("package x\n\nfunc Use() { Hot(); Hot() }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := []projectMapFile{{rel: "a.go", syms: []projectMapSym{
		{Name: "Hot", Full: "func Hot()"},
		{Name: "Cold", Full: "func Cold()"},
	}}}
	pruned := projectMapPrune(root, files, 12) // small enough to force a drop
	if len(pruned[0].syms) != 1 || pruned[0].syms[0].Name != "Hot" || pruned[0].dropped != 1 {
		t.Errorf("ranking must drop Cold and keep Hot, got %+v", pruned[0])
	}
}
