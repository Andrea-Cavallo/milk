package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitOnlyPath restricts PATH to git alone so runGrep cannot find rg (or a
// system grep) and must take the git grep branch.
func gitOnlyPath(t *testing.T) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	bin := t.TempDir()
	if err := os.Symlink(gitBin, filepath.Join(bin, "git")); err != nil {
		t.Skip("cannot symlink git")
	}
	t.Setenv("PATH", bin)
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, c := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, c...)...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", c, err, out)
		}
	}
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755) //nolint:errcheck
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "site/\n")
	write("main.go", "package main\n// needle in tracked file\n")
	write("sub/a-12-b.go", "line one\nneedle here\nline three\n")
	write("sub/new.txt", "needle in untracked file\n")
	write("site/page.html", "<p>needle in ignored build output</p>\n")
	write("big.txt", "needle "+strings.Repeat("x", 5000)+"\n")
	if out, err := exec.Command("git", "-C", dir, "add", ".gitignore", "main.go", "sub/a-12-b.go", "big.txt").CombinedOutput(); err != nil {
		t.Skipf("git add: %v %s", err, out)
	}
	return dir
}

func TestRunGrep_GitFallbackHonorsGitignore(t *testing.T) {
	dir := gitRepo(t)
	gitOnlyPath(t)
	out, _ := runGrep(context.Background(), map[string]any{"pattern": "needle"}, dir)
	for _, want := range []string{filepath.Join(dir, "main.go") + ":2:", filepath.Join(dir, "sub/new.txt") + ":1:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "site/page.html") {
		t.Errorf("gitignored directory was searched:\n%s", out)
	}
}

func TestRunGrep_GitFallbackContextAndOddFilename(t *testing.T) {
	dir := gitRepo(t)
	gitOnlyPath(t)
	out, _ := runGrep(context.Background(), map[string]any{"pattern": "needle here", "context": float64(1), "path": "sub"}, dir)
	base := filepath.Join(dir, "sub/a-12-b.go")
	for _, want := range []string{base + "-1-line one", base + ":2:needle here", base + "-3-line three"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunGrep_GitFallbackIncludeAndSingleFile(t *testing.T) {
	dir := gitRepo(t)
	gitOnlyPath(t)
	out, _ := runGrep(context.Background(), map[string]any{"pattern": "needle", "include": "*.txt"}, dir)
	if !strings.Contains(out, "new.txt") || strings.Contains(out, "main.go") {
		t.Errorf("include filter not applied:\n%s", out)
	}
	out, _ = runGrep(context.Background(), map[string]any{"pattern": "needle", "path": "main.go"}, dir)
	if !strings.Contains(out, filepath.Join(dir, "main.go")+":2:") || strings.Contains(out, "sub/") {
		t.Errorf("single-file search wrong:\n%s", out)
	}
}

func TestRunGrep_LongLinesAreCapped(t *testing.T) {
	dir := gitRepo(t)
	gitOnlyPath(t)
	out, _ := runGrep(context.Background(), map[string]any{"pattern": "needle", "path": "big.txt"}, dir)
	if len(out) > grepMaxLineChars+500 || !strings.Contains(out, "line truncated") {
		t.Errorf("long line not capped (len %d)", len(out))
	}
}

func TestCapLongLines_RuneBoundary(t *testing.T) {
	in := strings.Repeat("é", 3000) // 2 bytes per rune
	out := capLongLines(in, 2001)
	if !strings.Contains(out, "line truncated") {
		t.Fatal("expected truncation marker")
	}
	for _, r := range strings.SplitN(out, "…", 2)[0] {
		if r == '�' {
			t.Fatal("cut inside a rune")
		}
	}
}
