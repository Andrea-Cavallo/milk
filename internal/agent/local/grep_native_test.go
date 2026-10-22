package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeTree(t *testing.T) string {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // no rg, git or grep: forces nativeGrep
	dir := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755) //nolint:errcheck
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("a.go", "one\nNeedle 42\nthree\nfour\nfive\nsix\nneedle 7\neight\n")
	w("sub/b.txt", "no match here\nneedle in b\n")
	w(".hidden/c.txt", "needle hidden\n")
	w("node_modules/d.js", "needle vendored\n")
	w("bin.dat", "needle\x00binary")
	return dir
}

func grepNative(t *testing.T, dir string, args map[string]any) string {
	t.Helper()
	out, _ := runGrep(context.Background(), args, dir)
	return out
}

func TestNativeGrep_SkipsHiddenVendoredBinary(t *testing.T) {
	dir := nativeTree(t)
	out := grepNative(t, dir, map[string]any{"pattern": "needle", "ignore_case": true})
	for _, want := range []string{"a.go:2:Needle 42", "a.go:7:needle 7", "b.txt:2:needle in b"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, bad := range []string{".hidden", "node_modules", "bin.dat"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s should be skipped: %s", bad, out)
		}
	}
}

func TestNativeGrep_RegexFeaturesAndCase(t *testing.T) {
	dir := nativeTree(t)
	out := grepNative(t, dir, map[string]any{"pattern": `needle \d+`})
	if !strings.Contains(out, "a.go:7:needle 7") || strings.Contains(out, "Needle 42") {
		t.Errorf("\\d / case-sensitive wrong: %s", out)
	}
	out = grepNative(t, dir, map[string]any{"pattern": "one|three", "include": "*.go"})
	if !strings.Contains(out, ":1:one") || !strings.Contains(out, ":3:three") || strings.Contains(out, "b.txt") {
		t.Errorf("alternation/include wrong: %s", out)
	}
}

func TestNativeGrep_ContextGroupsAndSeparator(t *testing.T) {
	dir := nativeTree(t)
	out := grepNative(t, dir, map[string]any{"pattern": "Needle 42|needle 7", "context": float64(1), "path": "a.go"})
	a := filepath.Join(dir, "a.go")
	for _, want := range []string{a + "-1-one", a + ":2:Needle 42", a + "-3-three", "--", a + "-6-six", a + ":7:needle 7", a + "-8-eight"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "-4-four") {
		t.Errorf("line 4 is outside both context windows:\n%s", out)
	}
	// Adjacent windows merge without a separator.
	out = grepNative(t, dir, map[string]any{"pattern": "three|four", "context": float64(1), "path": "a.go"})
	if strings.Contains(out, "--") || strings.Count(out, `\n`) != 3 {
		t.Errorf("adjacent groups should merge:\n%s", out)
	}
}

func TestNativeGrep_MaxResultsTruncates(t *testing.T) {
	dir := nativeTree(t)
	out := grepNative(t, dir, map[string]any{"pattern": "needle", "ignore_case": true, "max_results": float64(2)})
	if !strings.Contains(out, "1 more matching lines") {
		t.Errorf("expected a counted truncation marker after 2 matches:\n%s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("third match should have been cut:\n%s", out)
	}
}

func TestNativeGrep_NoMatchAndBadPattern(t *testing.T) {
	dir := nativeTree(t)
	out := grepNative(t, dir, map[string]any{"pattern": "zzzz-not-there"})
	if !strings.Contains(out, `"exit_code":1`) {
		t.Errorf("no match should report exit code 1: %s", out)
	}
	out = grepNative(t, dir, map[string]any{"pattern": "([unclosed"})
	if !strings.Contains(out, "invalid pattern") {
		t.Errorf("bad pattern not reported: %s", out)
	}
}

func TestNativeGrep_HonorsCancellation(t *testing.T) {
	dir := nativeTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _ := runGrep(ctx, map[string]any{"pattern": "needle"}, dir)
	if strings.Contains(out, "a.go:") {
		t.Errorf("cancelled search still produced matches: %s", out)
	}
}
