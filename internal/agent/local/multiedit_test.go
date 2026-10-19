package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/memory"
)

// Issue #251 contract: multiedit applies a batch of edits to one file
// all-or-nothing, with exactly edit_file's per-edit matching rules.

func multiEditArgs(path string, edits ...map[string]any) map[string]any {
	raw := make([]any, len(edits))
	for i, e := range edits {
		raw[i] = e
	}
	return map[string]any{"path": path, "edits": raw}
}

func res(t *testing.T, out string) string {
	t.Helper()
	var r toolResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("bad tool result: %s", out)
	}
	if r.Error != "" {
		t.Fatalf("unexpected error: %s", r.Error)
	}
	return r.Output
}

func errText(t *testing.T, out string) string {
	t.Helper()
	var r toolResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("bad tool result: %s", out)
	}
	if r.Error == "" {
		t.Fatalf("expected an error, got output: %s", out)
	}
	return r.Error
}

func TestMultiEditAppliesInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Edit 2 matches text produced by edit 1 — sequential semantics.
	out, _ := runMultiEdit(multiEditArgs(path,
		map[string]any{"old_string": "alpha", "new_string": "ALPHA-RENAMED"},
		map[string]any{"old_string": "ALPHA-RENAMED\nbeta", "new_string": "first-two-lines-replaced"},
	), "")
	got := res(t, out)
	if !strings.Contains(got, "(2 edits)") {
		t.Fatalf("report: %s", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "first-two-lines-replaced\ngamma\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestMultiEditAtomicOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	orig := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runMultiEdit(multiEditArgs(path,
		map[string]any{"old_string": "one", "new_string": "ONE"},
		map[string]any{"old_string": "does not exist anywhere", "new_string": "x"},
		map[string]any{"old_string": "three", "new_string": "THREE"},
	), "")
	e := errText(t, out)
	if !strings.Contains(e, "edit 2 of 3 not applied") || !strings.Contains(e, "no changes were written") {
		t.Fatalf("error should name the failing edit and the atomicity: %s", e)
	}
	if !strings.Contains(e, "Closest match") {
		t.Fatalf("error should carry the closest-match hint: %s", e)
	}
	data, _ := os.ReadFile(path)
	if string(data) != orig {
		t.Fatalf("file changed despite failure: %q", data)
	}
}

func TestMultiEditAmbiguousFailsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	orig := "x\nx\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runMultiEdit(multiEditArgs(path,
		map[string]any{"old_string": "x", "new_string": "y"},
	), "")
	if e := errText(t, out); !strings.Contains(e, "ambiguous") {
		t.Fatalf("want ambiguity error: %s", e)
	}
	data, _ := os.ReadFile(path)
	if string(data) != orig {
		t.Fatalf("ambiguous edit must not write: %q", data)
	}
}

func TestMultiEditFuzzyAndReplaceAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("alpha x x x\nkeep   spaces   here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runMultiEdit(multiEditArgs(path,
		// Single-space needle vs triple-space content → whitespace-tolerant.
		map[string]any{"old_string": "keep spaces here", "new_string": "KEPT"},
		map[string]any{"old_string": "x", "new_string": "y", "replace_all": true},
	), "")
	got := res(t, out)
	if !strings.Contains(got, "edit 1 fuzzy match") {
		t.Fatalf("fuzzy match must be reported: %s", got)
	}
	if !strings.Contains(got, "edit 2: 3 replacements") {
		t.Fatalf("replace_all count must be reported: %s", got)
	}
	data, _ := os.ReadFile(path)
	want := "alpha y y y\nKEPT\n"
	if string(data) != want {
		t.Fatalf("file = %q want %q", data, want)
	}
}

func TestMultiEditPreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\r\nc\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runMultiEdit(multiEditArgs(path,
		map[string]any{"old_string": "b", "new_string": "B"},
	), "")
	res(t, out)
	data, _ := os.ReadFile(path)
	if string(data) != "a\r\nB\r\nc\r\n" {
		t.Fatalf("CRLF not preserved: %q", data)
	}
}

func TestMultiEditValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := runMultiEdit(map[string]any{"path": path, "edits": []any{}}, "")
	if e := errText(t, out); !strings.Contains(e, "edits must not be empty") {
		t.Fatalf("empty edits: %s", e)
	}
	out, _ = runMultiEdit(multiEditArgs(path, map[string]any{"old_string": "", "new_string": "y"}), "")
	if e := errText(t, out); !strings.Contains(e, "old_string must not be empty") {
		t.Fatalf("empty old_string: %s", e)
	}
	out, _ = runMultiEdit(map[string]any{"path": path, "edits": []any{"not-an-object"}}, "")
	if e := errText(t, out); !strings.Contains(e, "edits[0] must be an object") {
		t.Fatalf("bad edit shape: %s", e)
	}
}

func TestMultiEditInSchemaAndGatedAsWrite(t *testing.T) {
	tools := schemas(&memory.Store{}, t.TempDir(), nil, nil, &mockTaskStore{}, nil)
	found := false
	for _, s := range tools {
		if toolName(s) == "multiedit" {
			found = true
		}
	}
	if !found {
		t.Fatal("multiedit missing from schemas()")
	}
	if !toolNeedsPermission("multiedit") {
		t.Fatal("multiedit is a write — it must be permission-gated")
	}
	if !isCancellingTool("multiedit") {
		t.Fatal("multiedit is a write — a failed call must cancel the rest of the batch")
	}
	if !SideEffectingTool("multiedit") {
		t.Fatal("multiedit must be reported side-effecting")
	}
}

func TestMultiEditDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(multiEditArgs(path, map[string]any{"old_string": "one", "new_string": "two"}))
	out, _ := dispatchTool(context.Background(), "multiedit", string(args), nil, nil, memory.ConsumerAll, "", nil)
	if got := res(t, out); !strings.Contains(got, "edited") {
		t.Fatalf("dispatch: %s", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "two\n" {
		t.Fatalf("file = %q", data)
	}
}
