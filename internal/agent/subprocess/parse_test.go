package subprocess

import (
	"io"
	"strings"
	"testing"
)

func TestPlainTextParser_BasicLines(t *testing.T) {
	input := "hello\nworld\n"
	var out strings.Builder
	p := &PlainTextParser{}
	res, err := p.Parse(strings.NewReader(input), &out, ParseOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("expected both lines in output, got %q", got)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Errorf("expected Text to contain input, got %q", res.Text)
	}
}

func TestPlainTextParser_SkipLine(t *testing.T) {
	input := "keep\nskip me\nkeep too\n"
	var out strings.Builder
	p := &PlainTextParser{
		SkipLine: func(line string) bool {
			return strings.HasPrefix(line, "skip")
		},
	}
	res, err := p.Parse(strings.NewReader(input), &out, ParseOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "skip me") {
		t.Errorf("skipped line appeared in output: %q", out.String())
	}
	if strings.Contains(res.Text, "skip me") {
		t.Errorf("skipped line appeared in Text: %q", res.Text)
	}
	if !strings.Contains(res.Text, "keep") {
		t.Errorf("kept lines missing from Text: %q", res.Text)
	}
}

func TestPlainTextParser_EventHook(t *testing.T) {
	input := "line1\nline2\n"
	var out strings.Builder
	var hooked []string
	p := &PlainTextParser{
		EventHook: func(line string, w io.Writer, tb *strings.Builder) {
			hooked = append(hooked, line)
			tb.WriteString("[" + line + "]\n")
		},
	}
	_, err := p.Parse(strings.NewReader(input), &out, ParseOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooked) != 2 {
		t.Errorf("expected 2 hook calls, got %d", len(hooked))
	}
}

func TestPlainTextParser_EmptyInput(t *testing.T) {
	var out strings.Builder
	p := &PlainTextParser{}
	res, err := p.Parse(strings.NewReader(""), &out, ParseOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "" {
		t.Errorf("expected empty Text, got %q", res.Text)
	}
}

func TestPlainTextParser_EndsWithQ(t *testing.T) {
	var out strings.Builder
	p := &PlainTextParser{}
	res, err := p.Parse(strings.NewReader("Is this right?\n"), &out, ParseOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.EndsWithQ {
		t.Errorf("expected EndsWithQ=true for question-ending text")
	}
}

func TestParseMilkNDJSON_SystemWarning_DisplayedButNotInText(t *testing.T) {
	input := `{"type":"system","session_id":"s1","warning":"mcp server 'x': connect failed"}` + "\n" +
		`{"type":"text_delta","text":"hello"}` + "\n" +
		`{"type":"result","is_error":false}` + "\n"
	var out strings.Builder
	res, err := ParseMilkNDJSON(strings.NewReader(input), &out, ParseOpts{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SessionID != "s1" {
		t.Errorf("SessionID: want s1, got %q", res.SessionID)
	}
	if !strings.Contains(out.String(), "connect failed") {
		t.Errorf("warning must be displayed to the user, out=%q", out.String())
	}
	if strings.Contains(res.Text, "connect failed") {
		t.Errorf("warning must not leak into ParseResult.Text, text=%q", res.Text)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Errorf("final text missing, text=%q", res.Text)
	}
}
