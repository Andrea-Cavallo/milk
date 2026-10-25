package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/session"
)

// projectMapFixture writes a small mixed tree: a Go package with a struct,
// a method, a func with a unique body sentinel, consts and vars; a second
// package; and a non-Go file.
func projectMapFixture(t *testing.T) string {
	t.Helper()
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
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := projectMapCall(t, root, `{}`)
	if !strings.Contains(out, "broken.go") {
		t.Errorf("unparseable file must still be listed:\n%s", out)
	}
}
