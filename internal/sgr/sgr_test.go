package sgr

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestApplyAndString(t *testing.T) {
	tests := []struct {
		name   string
		params []string
		want   string
	}{
		{"zero value", nil, ""},
		{"dim", []string{"2"}, "\x1b[2m"},
		{"bold then dim, canonical order", []string{"2", "1"}, "\x1b[1;2m"},
		{"reset clears", []string{"1;31", "0"}, ""},
		{"empty params is a reset", []string{"1", ""}, ""},
		{"reset mid-list keeps later params", []string{"1;0;3"}, "\x1b[3m"},
		{"22 clears bold and dim", []string{"1;2;3", "22"}, "\x1b[3m"},
		{"256 fg", []string{"38;5;119"}, "\x1b[38;5;119m"},
		{"fg+bg combined", []string{"38;5;119;48;5;22"}, "\x1b[38;5;119;48;5;22m"},
		{"truecolor bg", []string{"48;2;24;24;28"}, "\x1b[48;2;24;24;28m"},
		{"39 only clears fg", []string{"31;42", "39"}, "\x1b[42m"},
		{"49 only clears bg", []string{"31;42", "49"}, "\x1b[31m"},
		{"fg index 48 is not a bg command", []string{"38;5;48"}, "\x1b[38;5;48m"},
		{"bg index 0 is not a reset", []string{"48;5;0"}, "\x1b[48;5;0m"},
		{"later colour replaces earlier", []string{"31", "32"}, "\x1b[32m"},
		{"malformed extended colour is ignored", []string{"38;5"}, ""},
		{"colon truecolor", []string{"38:2::1:2:3"}, "\x1b[38:2::1:2:3m"},
		{"unknown params ignored", []string{"53;58;5;1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s State
			for _, p := range tt.params {
				s.Apply(p)
			}
			if got := s.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
			if s.Active() != (tt.want != "") {
				t.Fatalf("Active() = %v for %q", s.Active(), tt.want)
			}
		})
	}
}

func TestWalkSkipsNonSGR(t *testing.T) {
	var got []string
	Walk("a\x1b[2mb\x1b[2Jc\x1b[38;5;1md\x1b[", func(p string) { got = append(got, p) })
	if strings.Join(got, "|") != "2|38;5;1" {
		t.Fatalf("got %q", got)
	}
}

func TestSelfContain(t *testing.T) {
	t.Run("plain text untouched", func(t *testing.T) {
		if got := SelfContain("a\nb"); got != "a\nb" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("balanced rows untouched", func(t *testing.T) {
		in := "\x1b[2ma\x1b[0m\n\x1b[1mb\x1b[0m"
		if got := SelfContain(in); got != in {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("span split across rows", func(t *testing.T) {
		got := SelfContain("\x1b[2mone\ntwo\x1b[0m three")
		want := "\x1b[2mone\x1b[0m\n\x1b[2mtwo\x1b[0m three"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("unclosed at the end", func(t *testing.T) {
		got := SelfContain("\x1b[31mred")
		if got != "\x1b[31mred\x1b[0m" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("blank row inside a span keeps the background", func(t *testing.T) {
		got := SelfContain("\x1b[48;5;235mone\n\ntwo\x1b[0m")
		want := "\x1b[48;5;235mone\x1b[0m\n\x1b[48;5;235m\x1b[0m\n\x1b[48;5;235mtwo\x1b[0m"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		once := SelfContain("\x1b[1;38;5;9mone\ntwo\nthree\x1b[0m\nplain")
		if twice := SelfContain(once); twice != once {
			t.Fatalf("not idempotent:\n%q\n%q", once, twice)
		}
	})
	t.Run("visible text preserved", func(t *testing.T) {
		in := "\x1b[2mone\ntwo\x1b[0m\nthree"
		if ansi.Strip(SelfContain(in)) != ansi.Strip(in) {
			t.Fatal("visible text changed")
		}
	})
}

func TestSelfContainSurvivesWrap(t *testing.T) {
	var b strings.Builder
	for _, w := range strings.SplitAfter("alpha beta gamma delta epsilon zeta eta theta iota kappa", " ") {
		b.WriteString("\x1b[2m")
		b.WriteString(w)
		b.WriteString("\x1b[0m")
	}
	wrapped := SelfContain(ansi.Wrap(b.String(), 20, ""))
	for i, row := range strings.Split(wrapped, "\n") {
		var st State
		st.Scan(row)
		if st.Active() {
			t.Errorf("row %d ends open: %q", i, row)
		}
		if ansi.Strip(row) != "" && !strings.Contains(row, "\x1b[2m") {
			t.Errorf("row %d lost its dim: %q", i, row)
		}
	}
}

func TestWithBackground(t *testing.T) {
	const bg = "\x1b[48;2;24;24;28m"
	tests := []struct{ name, row, want string }{
		{"plain", "abc", bg + "abc" + Reset},
		{"literal reset", "\x1b[2ma\x1b[0mb", bg + "\x1b[2ma\x1b[0m" + bg + "b" + Reset},
		{"short reset", "\x1b[2ma\x1b[mb", bg + "\x1b[2ma\x1b[m" + bg + "b" + Reset},
		{"reset-then-set", "\x1b[0;31ma", bg + "\x1b[0;31m" + bg + "a" + Reset},
		{"49 clears the tint", "a\x1b[49mb", bg + "a\x1b[49m" + bg + "b" + Reset},
		{"fg-only change leaves the tint alone", "\x1b[31ma\x1b[39mb", bg + "\x1b[31ma\x1b[39mb" + Reset},
		{"own bg wins until its reset", "\x1b[48;5;22ma\x1b[0mb", bg + "\x1b[48;5;22ma\x1b[0m" + bg + "b" + Reset},
		{"fg index 0 is not a reset", "\x1b[38;5;0ma", bg + "\x1b[38;5;0ma" + Reset},
		{"empty row", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithBackground(tt.row, bg); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	if got := WithBackground("abc", ""); got != "abc" {
		t.Fatalf("empty bg should be a no-op, got %q", got)
	}
}

func TestHighlight(t *testing.T) {
	const on, off = "<", ">"
	tests := []struct {
		name       string
		row        string
		start, end int
		want       string
	}{
		{"plain range", "abcdef", 1, 4, "a<bcd>ef"},
		{"to the end", "abc", 1, 99, "a<bc>"},
		{"empty range", "abc", 2, 2, "abc"},
		{"negative start clamps", "abc", -5, 2, "<ab>c"},
		{"styling is kept outside the range", "\x1b[2mab\x1b[0mcd", 2, 4, "\x1b[2mab\x1b[0m<cd>"},
		{"on is re-asserted after a reset inside the range", "ab\x1b[2mcd\x1b[0mef", 1, 5, "a<b\x1b[2m<cd\x1b[0m<e>f"},
		{"non-SGR escapes pass through", "a\x1b[2Kb", 0, 2, "<a\x1b[2Kb>"},
		{"wide runes count once", "世界ab", 1, 3, "世<界a>b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Highlight(tt.row, tt.start, tt.end, on, off); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHighlightKeepsVisibleText(t *testing.T) {
	row := "\x1b[1;31mhello\x1b[0m \x1b[48;5;22mwörld\x1b[0m 世界"
	got := Highlight(row, 3, 9, "\x1b[7m", "\x1b[27m")
	if ansi.Strip(got) != ansi.Strip(row) {
		t.Fatalf("visible text changed: %q", ansi.Strip(got))
	}
}

func TestLastBackground(t *testing.T) {
	isDiff := func(bg string) bool { return bg == "48;5;22" || bg == "48;5;52" }
	tests := []struct{ name, row, want string }{
		{"no escapes", "plain", ""},
		{"green diff row", "\x1b[38;5;119;48;5;22m+a\x1b[0m", "48;5;22"},
		{"last accepted wins", "\x1b[48;5;22ma\x1b[48;5;52mb\x1b[0m", "48;5;52"},
		{"fg index 22 is not a background", "\x1b[38;5;22ma", ""},
		{"other background", "\x1b[48;5;99ma", ""},
		{"background seen even if reset later", "\x1b[48;5;22ma\x1b[0mb", "48;5;22"},
		{"marker as text", "48;5;22", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastBackground(tt.row, isDiff); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLeading(t *testing.T) {
	tests := []struct{ text, want string }{
		{"plain", ""},
		{"\x1b[2mdim", "\x1b[2m"},
		{"\x1b[1m\x1b[31mred bold", "\x1b[1;31m"},
		{"\x1b[2mA\x1b[1mnot leading", "\x1b[2m"},
		{"\x1b[2Kcursor then text", ""},
		{"\x1b[0m\x1b[3mreset then italic", "\x1b[3m"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Leading(tt.text).String(); got != tt.want {
			t.Errorf("Leading(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
