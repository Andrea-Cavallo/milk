package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/memory"
)

// applyEditCall is the edit_file entry point tests exercise end-to-end.
func applyEditCall(t *testing.T, content, oldStr, newStr string, replaceAll bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	args := `{"path":"` + f + `","old_string":` + jsonQuote(oldStr) + `,"new_string":` + jsonQuote(newStr)
	if replaceAll {
		args += `,"replace_all":true`
	}
	args += `}`
	res, _ := dispatchTool(context.Background(), "edit_file", args, nil, nil, memory.ConsumerAll, "", nil)
	got, _ := os.ReadFile(f)
	return res, string(got)
}

// jsonQuote escapes a string for embedding in tool-call arguments.
func jsonQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func TestApplyEdit_Chain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		content  string
		oldStr   string
		newStr   string
		want     string
		strategy string
	}{
		{
			name:     "exact",
			content:  "alpha\nbeta\ngamma\n",
			oldStr:   "beta",
			newStr:   "BETA",
			want:     "alpha\nBETA\ngamma\n",
			strategy: "exact",
		},
		{
			name:     "trailing-whitespace-drift",
			content:  "alpha   \nbeta\t\ngamma\n",
			oldStr:   "alpha\nbeta",
			newStr:   "AB",
			want:     "AB\ngamma\n",
			strategy: "line-trimmed",
		},
		{
			name:     "indentation-flexible",
			content:  "func f() {\n\t\treturn  1\n\t}\n",
			oldStr:   "return 1",
			newStr:   "\t\treturn 2",
			want:     "func f() {\n\t\treturn 2\n\t}\n",
			strategy: "indentation-flexible",
		},
		{
			name:     "whitespace-normalized",
			content:  "call(  a,   b )\n",
			oldStr:   "call( a, b )",
			newStr:   "call(a, b)",
			want:     "call(a, b)\n",
			strategy: "whitespace-normalized",
		},
		{
			name:     "escape-normalized",
			content:  "line one\nline two\n",
			oldStr:   `line one\nline two`, // literal \n two-char sequences
			newStr:   "one and two",
			want:     "one and two\n",
			strategy: "escape-normalized",
		},
		{
			name:     "trimmed-boundary",
			content:  "  body text  \n",
			oldStr:   "\nbody text\n",
			newStr:   "NEW",
			want:     "  NEW  \n",
			strategy: "trimmed-boundary",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, count, strategy, err := applyEdit(tc.content, tc.oldStr, tc.newStr, "f.txt", false)
			if err != nil {
				t.Fatalf("applyEdit: %v", err)
			}
			if count != 1 {
				t.Errorf("count = %d, want 1", count)
			}
			if got != tc.want {
				t.Errorf("content:\n got %q\nwant %q", got, tc.want)
			}
			if strategy != tc.strategy {
				t.Errorf("strategy = %q, want %q", strategy, tc.strategy)
			}
		})
	}
}

// A fuzzy match must never be ambiguous: several equally plausible blocks
// fail with the ambiguity error, and nothing is applied.
func TestApplyEdit_FuzzyAmbiguousRejected(t *testing.T) {
	content := "    return 1\n    return 1\n"
	_, _, _, err := applyEdit(content, "return 1", "return 2", "f.txt", false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}

	// replace_all applies both.
	got, count, _, err := applyEdit(content, "return 1", "return 2", "f.txt", true)
	if err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if count != 2 || got != "    return 2\n    return 2\n" {
		t.Errorf("replace_all got %q (count %d)", got, count)
	}
}

// On total failure the closest block is returned as a hint and the file is
// never changed (MiMo behavior: suggest, don't apply).
func TestApplyEdit_ClosestMatchHintNotApplied(t *testing.T) {
	content := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	got, count, _, err := applyEdit(content, "func main() {\n\tfmt.Println(\"goodbye\")\n}", "X", "f.txt", false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if count != 0 || got != content {
		t.Errorf("nothing may be applied on failure (count=%d)", count)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Closest match") || !strings.Contains(msg, "hello") {
		t.Errorf("hint must show the closest block, got %q", msg)
	}
	if !strings.Contains(msg, "never applies fuzzy matches silently") {
		t.Errorf("hint must say it was not applied, got %q", msg)
	}
}

// CRLF files keep CRLF line endings across an edit, including in the
// replacement text.
func TestApplyEdit_PreservesCRLF(t *testing.T) {
	content := "a\r\nb\r\nc\r\n"
	got, _, _, err := applyEdit(content, "b", "x\ny", "f.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\r\nx\r\ny\r\nc\r\n"
	if got != want {
		t.Errorf("CRLF not preserved:\n got %q\nwant %q", got, want)
	}
}

// The exact-match semantics survive unchanged: uniqueness required unless
// replace_all.
func TestApplyEdit_ExactAmbiguous(t *testing.T) {
	_, _, _, err := applyEdit("x\nx\n", "x", "y", "f.txt", false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

// writeFileAtomic writes through a temp file + rename and keeps the existing
// file mode.
func TestWriteFileAtomic_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(f, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f); string(got) != "new" {
		t.Errorf("content = %q", got)
	}
	if info, _ := os.Stat(f); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".milk-edit-*"))
	if len(files) != 0 {
		t.Errorf("temp files left behind: %v", files)
	}
}

// End-to-end through the tool: a whitespace-drifted edit lands and reports the
// fuzzy strategy.
func TestEditFile_FuzzyEndToEnd(t *testing.T) {
	res, got := applyEditCall(t, "call(  a,   b )\n", "call( a, b )", "call(a, b)", false)
	if isToolError(res) {
		t.Fatalf("edit_file failed: %s", res)
	}
	if !strings.Contains(res, "fuzzy match") {
		t.Errorf("result must name the fuzzy strategy, got %s", res)
	}
	if got != "call(a, b)\n" {
		t.Errorf("file = %q", got)
	}
}

// End-to-end through the tool: a failed edit returns the hint and leaves the
// file untouched.
func TestEditFile_HintEndToEnd(t *testing.T) {
	res, got := applyEditCall(t, "hello world\n", "goodbye world", "X", false)
	if !isToolError(res) {
		t.Fatalf("expected error, got %s", res)
	}
	if !strings.Contains(res, "Closest match") || !strings.Contains(res, "hello world") {
		t.Errorf("hint missing from result: %s", res)
	}
	if got != "hello world\n" {
		t.Errorf("file must be untouched, got %q", got)
	}
}
