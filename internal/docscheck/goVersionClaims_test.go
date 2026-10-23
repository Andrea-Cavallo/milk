// Package docscheck holds repo-hygiene tests that keep duplicated literals in
// documentation aligned with their source of truth.
//
// Currently guarded: the Go version. go.mod is the single place a human types
// it. Every other site is either checked here (docs prose, badges) or derives
// from go.mod at runtime (install-from-source.sh) or at build time
// (setup-go's go-version-file). Run with the normal suite: go test ./...
//
// To guard a new claim site, add it to the tables below — that is a deliberate
// one-line decision, not an oversight.
package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the repository root relative to this package's directory.
const repoRoot = "../.."

var (
	goDirectiveRE = regexp.MustCompile(`(?m)^go\s+(\d+(?:\.\d+){1,2})\s*$`)
	// claimRE matches human-facing Go version claims: "Go 1.26.2", "Go-1.26.2"
	// (shields badge URL form), with an optional trailing "+".
	claimRE = regexp.MustCompile(`\bGo[- ](\d+\.\d+(?:\.\d+)?)\+?`)
	// literalClaimRE matches a hardcoded version next to "Go" in prose or an
	// error message (used to prove install-from-source.sh has none).
	literalClaimRE = regexp.MustCompile(`\bGo \d+\.\d+(?:\.\d+)?`)
)

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// goModVersion returns the version declared by go.mod's `go` directive.
func goModVersion(t *testing.T) string {
	t.Helper()
	m := goDirectiveRE.FindStringSubmatch(readFile(t, "go.mod"))
	if m == nil {
		t.Fatal("go.mod: no `go X.Y[.Z]` directive found")
	}
	return m[1]
}

// TestGoVersionClaimsMatchGoMod pins every allowlisted human-facing Go version
// claim to the go.mod value, so bumping go.mod turns into a one-line doc update
// instead of silent drift (which produced real "Go 1.21+" instructions long
// after the module required newer Go).
func TestGoVersionClaimsMatchGoMod(t *testing.T) {
	want := goModVersion(t)
	// Files where a human-facing requirement claim may appear. ADRs and code
	// comments are deliberately excluded: they record history, not requirements.
	claimFiles := []string{
		"README.md",
		"CLAUDE.md",
		"docs/start/getting-started.md",
	}
	for _, file := range claimFiles {
		text := readFile(t, file)
		for _, m := range claimRE.FindAllStringSubmatchIndex(text, -1) {
			claim := text[m[2]:m[3]]
			if claim == want {
				continue
			}
			line := strings.Count(text[:m[0]], "\n") + 1
			t.Errorf("%s:%d: claims Go %s but go.mod requires Go %s — update the claim (or make it version-less, like the README badge)",
				file, line, claim, want)
		}
	}
}

// TestWorkflowsPinGoToGoMod forbids a hardcoded `go-version:` pin in Actions
// workflows; setup-go must take its version from go.mod (go-version-file),
// which is fresh by construction.
func TestWorkflowsPinGoToGoMod(t *testing.T) {
	dir := filepath.Join(repoRoot, ".github", "workflows")
	matches, err := filepath.Glob(filepath.Join(dir, "*.y*ml"))
	if err != nil {
		t.Fatalf("glob workflows: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no workflow files found under .github/workflows — layout changed?")
	}
	for _, wf := range matches {
		text, err := os.ReadFile(wf)
		if err != nil {
			t.Fatalf("read %s: %v", wf, err)
		}
		if strings.Contains(string(text), "go-version:") {
			t.Errorf("%s: hardcoded go-version pin — use go-version-file: go.mod instead", filepath.Base(wf))
		}
	}
}

// TestInstallFromSourceDerivesGoVersionFromGoMod proves the source installer
// carries no hardcoded Go version: its floor must come from the checked-out
// go.mod at install time (works for old tags pinned via MILK_VERSION too).
func TestInstallFromSourceDerivesGoVersionFromGoMod(t *testing.T) {
	text := readFile(t, "install-from-source.sh")
	if m := literalClaimRE.FindStringIndex(text); m != nil {
		line := strings.Count(text[:m[0]], "\n") + 1
		t.Errorf("install-from-source.sh:%d: hardcoded Go version claim %q — derive it from go.mod (check_go_version) instead",
			line, text[m[0]:m[1]])
	}
	for _, marker := range []string{"version_ge", "go.mod"} {
		if !strings.Contains(text, marker) {
			t.Errorf("install-from-source.sh: missing %q — expected a go.mod-derived version pre-check", marker)
		}
	}
}
