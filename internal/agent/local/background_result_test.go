package local

import "testing"

func TestParseBackgroundResult_NoTag_ReturnsUnchanged(t *testing.T) {
	raw := "the bug is in foo.go line 42"
	text, status, files := ParseBackgroundResult(raw)
	if text != raw {
		t.Errorf("expected text unchanged, got %q", text)
	}
	if status != "" || files != "" {
		t.Errorf("expected no status/files when no tag present, got status=%q files=%q", status, files)
	}
}

func TestParseBackgroundResult_WithFullTag(t *testing.T) {
	raw := "found the bug in foo.go.\n" + `<result status="ok" files_touched="foo.go,bar.go"/>`
	text, status, files := ParseBackgroundResult(raw)
	if text != "found the bug in foo.go." {
		t.Errorf("expected the tag stripped from text, got %q", text)
	}
	if status != "ok" {
		t.Errorf("expected status=ok, got %q", status)
	}
	if files != "foo.go,bar.go" {
		t.Errorf("expected files_touched=foo.go,bar.go, got %q", files)
	}
}

func TestParseBackgroundResult_StatusOnly(t *testing.T) {
	raw := "no relevant files found.\n" + `<result status="error"/>`
	text, status, files := ParseBackgroundResult(raw)
	if text != "no relevant files found." {
		t.Errorf("expected the tag stripped from text, got %q", text)
	}
	if status != "error" {
		t.Errorf("expected status=error, got %q", status)
	}
	if files != "" {
		t.Errorf("expected no files_touched, got %q", files)
	}
}

func TestParseBackgroundResult_TagNotAtEnd_NotStripped(t *testing.T) {
	// A <result .../> mentioned mid-text (e.g. discussing the convention
	// itself) is not the trailing structured tag and must be left alone.
	raw := `<result status="ok"/> is the convention, followed by more text.`
	text, status, _ := ParseBackgroundResult(raw)
	if text != raw {
		t.Errorf("expected text unchanged when the tag isn't trailing, got %q", text)
	}
	if status != "" {
		t.Errorf("expected no status parsed from a non-trailing tag, got %q", status)
	}
}

func TestParseBackgroundResult_EmptyInput(t *testing.T) {
	text, status, files := ParseBackgroundResult("")
	if text != "" || status != "" || files != "" {
		t.Errorf("expected all empty for empty input, got text=%q status=%q files=%q", text, status, files)
	}
}
