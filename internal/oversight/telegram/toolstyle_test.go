package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestToolMessages_QuietFormat pins the exact Tier-1 lines (issue #290):
// italic tool name, middle-dot, inline-code summary, never bold — except
// error results, which stay bold on purpose.
func TestToolMessages_QuietFormat(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"use with summary", toolUseMessage("Bash", "grep -rn foo"), "⚙ _Bash_ · `grep -rn foo`"},
		{"use without summary", toolUseMessage("Read", ""), "⚙ _Read_"},
		{"result ok", toolResultMessage("Read", "40 lines", false), "📤 _Read_ · `40 lines`"},
		{"result ok without summary", toolResultMessage("Read", "", false), "📤 _Read_"},
		{"result error stays bold", toolResultMessage("Bash", "exit 1: no such file", true),
			"❌ *Bash* error · `exit 1: no such file`"},
		{"result error without summary", toolResultMessage("Bash", "", true), "❌ *Bash* error"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, c.got, c.want)
		}
	}
}

// Escaping rides escMD as before (underscore is MarkdownV2-active in the
// italic tool name too — the name must be escaped there as well).
func TestToolMessages_EscapesMarkdown(t *testing.T) {
	// The italic name runs through escMD ('_' is active outside code)…
	if got, want := toolUseMessage("a_b", "x*y"), "⚙ _a\\_b_ · `x*y`"; got != want {
		t.Errorf("escape name:\n got: %q\nwant: %q", got, want)
	}
	if got, want := toolResultMessage("a*b", "c_d", true), "❌ *a\\*b* error · `c_d`"; got != want {
		t.Errorf("escape err name:\n got: %q\nwant: %q", got, want)
	}
	// …while code content only escapes the two characters MarkdownV2
	// treats as special inside code spans ('\\' and '`') — '-', '.' etc.
	// stay as-is (escMD there would over-escape with no purpose).
	if got, want := toolUseMessage("Bash", "a\\b`c"), "⚙ _Bash_ · `a\\\\b\\`c`"; got != want {
		t.Errorf("escape code:\n got: %q\nwant: %q", got, want)
	}
}

// Newlines collapse to a single-line code span (multi-line MarkdownV2 code
// parses unreliably), and both caps keep the unchunked tool message far
// below Telegram's 4096-unit limit — pre-#290 an oversized preview made the
// whole send fail silently.
func TestToolMessages_FlattenAndCap(t *testing.T) {
	got := toolUseMessage("Bash", "line1\r\nline2\nline3")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("summary still contains raw newlines: %q", got)
	}
	if !strings.Contains(got, "line1 ⏎ line2 ⏎ line3") {
		t.Errorf("newlines not flattened: %q", got)
	}

	long := strings.Repeat("x", 5000)
	use := toolUseMessage("Bash", long)
	if !strings.HasSuffix(use, "…`") || !strings.Contains(use, "…") {
		t.Errorf("use summary not trimmed: %q", use)
	}
	if n := utf16Len(use); n >= 4000 {
		t.Errorf("use message %d units — can exceed the 4096 limit", n)
	}
	res := toolResultMessage("Bash", long, false)
	if n := utf16Len(res); n >= 4000 {
		t.Errorf("result message %d units — can exceed the 4096 limit", n)
	}
	// The trim is rune-safe (never splits a multi-byte character).
	if !utf8.ValidString(use) || !utf8.ValidString(res) {
		t.Error("trim produced invalid UTF-8")
	}
}

// End-to-end: the notifier ships exactly the builder's line — and an empty
// result summary still sends nothing (the pre-existing rule).
func TestNotifyToolMessages_ShipBuiltLines(t *testing.T) {
	s := newStatusServer()
	defer s.srv.Close()
	n := statusNotifier(s, func(c *Config) { c.BusyIndicator = false })

	n.NotifyToolUse(context.Background(), "Bash", "grep -rn foo")
	n.NotifyToolResult(context.Background(), "Bash", "exit 1: no such file", true)
	n.NotifyToolResult(context.Background(), "Read", "", false) // empty → nothing

	waitForStatus(t, 2*time.Second, "both tool messages", func() bool {
		return s.msgCount() == 2
	})
	if got := s.message(0); got != toolUseMessage("Bash", "grep -rn foo") {
		t.Errorf("use message = %q", got)
	}
	if got := s.message(1); got != toolResultMessage("Bash", "exit 1: no such file", true) {
		t.Errorf("error message = %q", got)
	}
}
