// Package sgr tracks ANSI SGR (Select Graphic Rendition) state through styled
// text, so callers that split, wrap, tint or highlight it row by row can keep
// every row self-contained instead of each re-implementing its own "what style
// is still open here" bookkeeping.
//
// Terminals carry SGR state across newlines, but nothing downstream of a word
// wrap does: a viewport can start its window on a continuation row, a per-row
// tint closes each row with a reset, a selection rebuilds a row from scratch.
// Text whose rows are each balanced — opened with the state inherited from
// above, closed at the end — survives all of that.
//
// Only the attributes a terminal UI actually emits are tracked: bold, dim,
// italic, underline, blink, reverse, hidden, strikethrough, and the foreground
// and background colours in every form (16-colour, 256-colour, truecolour).
// Anything else (overline, underline colour, …) is ignored by Apply.
package sgr

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Reset is the full SGR reset.
const Reset = "\x1b[0m"

const (
	attrBold uint8 = 1 << iota
	attrDim
	attrItalic
	attrUnderline
	attrBlink
	attrReverse
	attrHidden
	attrStrike
)

// attrCodes orders the attributes the way State.String emits them.
var attrCodes = [...]struct {
	bit  uint8
	code string
}{
	{attrBold, "1"}, {attrDim, "2"}, {attrItalic, "3"}, {attrUnderline, "4"},
	{attrBlink, "5"}, {attrReverse, "7"}, {attrHidden, "8"}, {attrStrike, "9"},
}

// State is the rendition active at some point in a styled text. The zero value
// is the terminal default (nothing set).
type State struct {
	attrs uint8
	// FG and BG hold the active colour as the SGR parameters that set it
	// ("31", "38;5;119", "48;2;24;24;28"); empty means the default colour.
	FG, BG string
}

// Includes reports whether every rendition other sets is also set in s —
// i.e. s still carries other's context (s may carry more).
func (s State) Includes(other State) bool {
	if other.attrs&^s.attrs != 0 {
		return false
	}
	if other.FG != "" && other.FG != s.FG {
		return false
	}
	if other.BG != "" && other.BG != s.BG {
		return false
	}
	return true
}

// Active reports whether any rendition is set.
func (s State) Active() bool { return s.attrs != 0 || s.FG != "" || s.BG != "" }

// Dim reports whether the dim (faint) attribute is active.
func (s State) Dim() bool { return s.attrs&attrDim != 0 }

// String returns one escape sequence that re-establishes the state from a
// clean slate, or "" when nothing is set.
func (s State) String() string {
	if !s.Active() {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, a := range attrCodes {
		if s.attrs&a.bit != 0 {
			parts = append(parts, a.code)
		}
	}
	if s.FG != "" {
		parts = append(parts, s.FG)
	}
	if s.BG != "" {
		parts = append(parts, s.BG)
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// Apply folds one SGR parameter list (the text between "ESC[" and "m") into
// the state. Parameters apply left to right; an empty list is a reset.
func (s *State) Apply(params string) {
	if params == "" {
		*s = State{}
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		// Colon-separated sub-parameter form (38:2::r:g:b, 4:3, …) arrives as
		// one token.
		if strings.Contains(p, ":") {
			s.applyColon(p)
			continue
		}
		n, err := strconv.Atoi(p)
		if p == "" {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			*s = State{}
		case n == 1:
			s.attrs |= attrBold
		case n == 2:
			s.attrs |= attrDim
		case n == 3:
			s.attrs |= attrItalic
		case n == 4:
			s.attrs |= attrUnderline
		case n == 5:
			s.attrs |= attrBlink
		case n == 7:
			s.attrs |= attrReverse
		case n == 8:
			s.attrs |= attrHidden
		case n == 9:
			s.attrs |= attrStrike
		case n == 22:
			s.attrs &^= attrBold | attrDim
		case n == 23:
			s.attrs &^= attrItalic
		case n == 24:
			s.attrs &^= attrUnderline
		case n == 25:
			s.attrs &^= attrBlink
		case n == 27:
			s.attrs &^= attrReverse
		case n == 28:
			s.attrs &^= attrHidden
		case n == 29:
			s.attrs &^= attrStrike
		case n >= 30 && n <= 37, n >= 90 && n <= 97:
			s.FG = p
		case n == 39:
			s.FG = ""
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			s.BG = p
		case n == 49:
			s.BG = ""
		case n == 38 || n == 48 || n == 58:
			color, used := extendedColor(parts[i:])
			i += used - 1
			switch {
			case color == "":
			case n == 38:
				s.FG = color
			case n == 48:
				s.BG = color
			}
		}
	}
}

func (s *State) applyColon(p string) {
	switch {
	case strings.HasPrefix(p, "38:"):
		s.FG = p
	case strings.HasPrefix(p, "48:"):
		s.BG = p
	case p == "4:0":
		s.attrs &^= attrUnderline
	case strings.HasPrefix(p, "4:"):
		s.attrs |= attrUnderline
	}
}

// extendedColor parses a 38/48 colour whose parameters start at parts[0]
// ("38","5","n" or "38","2","r","g","b") and returns it as the SGR parameter
// string plus the number of parts consumed. A malformed or truncated run
// yields "" and consumes everything, as terminals discard it.
func extendedColor(parts []string) (color string, used int) {
	switch {
	case len(parts) >= 3 && parts[1] == "5":
		return strings.Join(parts[:3], ";"), 3
	case len(parts) >= 5 && parts[1] == "2":
		return strings.Join(parts[:5], ";"), 5
	}
	return "", len(parts)
}

// Walk calls fn with the parameter list of every SGR sequence (ESC [ … m) in
// text, in order. Other escape sequences are skipped. An escape cut off at the
// end of text is ignored.
func Walk(text string, fn func(params string)) {
	for i := 0; i < len(text); {
		if text[i] != 0x1B || i+1 >= len(text) || text[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		for j < len(text) && (text[j] < 0x40 || text[j] > 0x7E) {
			j++
		}
		if j >= len(text) {
			return
		}
		if text[j] == 'm' {
			fn(text[i+2 : j])
		}
		i = j + 1
	}
}

// Scan applies every SGR sequence in text to s.
func (s *State) Scan(text string) { Walk(text, s.Apply) }

// Leading returns the state established by the run of SGR sequences at the
// very start of text, before any visible character — the styling a line opens
// with.
func Leading(text string) State {
	var st State
	for strings.HasPrefix(text, "\x1b[") {
		end := strings.IndexFunc(text[2:], func(r rune) bool { return r >= 0x40 && r <= 0x7E })
		if end < 0 || text[2+end] != 'm' {
			break
		}
		st.Apply(text[2 : 2+end])
		text = text[end+3:]
	}
	return st
}

// SelfContain makes every row of text (split on "\n") balanced: each row
// starts by re-opening the rendition still active from the rows above, and a
// row that ends with a rendition active is closed with a reset. A styled span
// that a word wrap split across rows therefore renders the same wherever the
// rows are cut, tinted, truncated or re-joined. Text whose rows are already
// balanced is returned unchanged, so applying it twice is a no-op.
func SelfContain(text string) string {
	if !strings.Contains(text, "\x1b[") {
		return text
	}
	rows := strings.Split(text, "\n")
	var st State
	for i, row := range rows {
		open := st.String()
		st.Scan(row)
		if open == "" && !st.Active() {
			continue
		}
		var sb strings.Builder
		sb.Grow(len(open) + len(row) + len(Reset))
		sb.WriteString(open)
		sb.WriteString(row)
		if st.Active() {
			sb.WriteString(Reset)
		}
		rows[i] = sb.String()
	}
	return strings.Join(rows, "\n")
}

// LastBackground returns the last background that match accepts among all the
// backgrounds active anywhere in row, or "" when the row never shows one. It
// answers "does this row sit on surface X, and which shade" for full-row
// painters, without them parsing SGR themselves.
func LastBackground(row string, match func(bg string) bool) string {
	var st State
	found := ""
	Walk(row, func(params string) {
		st.Apply(params)
		if st.BG != "" && match(st.BG) {
			found = st.BG
		}
	})
	return found
}

// WithBackground paints bg behind a row. The background is re-applied after
// every sequence that clears it (a reset, "0;…", 49), so styled spans inside
// the row — which close themselves with their own reset — cannot cut the
// tint back to the terminal default midway. Sequences that set another
// background win until they end.
func WithBackground(row, bg string) string {
	if bg == "" || row == "" {
		return row
	}
	var st State
	st.Scan(bg)
	var sb strings.Builder
	sb.WriteString(bg)
	last := 0
	for i := 0; i < len(row); {
		if row[i] != 0x1B || i+1 >= len(row) || row[i+1] != '[' {
			i++
			continue
		}
		j := i + 2
		for j < len(row) && (row[j] < 0x40 || row[j] > 0x7E) {
			j++
		}
		if j >= len(row) {
			break
		}
		if row[j] == 'm' {
			hadBG := st.BG != ""
			st.Apply(row[i+2 : j])
			if hadBG && st.BG == "" {
				sb.WriteString(row[last : j+1])
				sb.WriteString(bg)
				st.Scan(bg)
				last = j + 1
			}
		}
		i = j + 1
	}
	sb.WriteString(row[last:])
	sb.WriteString(Reset)
	return sb.String()
}

// Highlight wraps the visible runes [start, end) of row in the on/off pair
// while keeping the row's own styling intact: the row is walked, not stripped.
// on is re-asserted after any SGR sequence inside the range (a reset would
// otherwise end the highlight early). Indexes count visible runes, the same
// coordinate space as []rune(ansi.Strip(row)); out-of-range values clamp, and
// an empty range returns row unchanged.
func Highlight(row string, start, end int, on, off string) string {
	if start < 0 {
		start = 0
	}
	if end <= start {
		return row
	}
	var (
		sb    strings.Builder
		state byte
		p     = ansi.GetParser()
		pos   int
		in    bool
	)
	defer ansi.PutParser(p)
	sb.Grow(len(row) + len(on) + len(off))
	for rest := row; len(rest) > 0; {
		seq, _, n, next := ansi.DecodeSequenceWc(rest, state, p)
		state, rest = next, rest[n:]
		if seq == "" {
			continue
		}
		if seq[0] == 0x1B || seq[0] == 0x9B || seq[0] == 0x9D {
			sb.WriteString(seq)
			if in && isSGR(seq) {
				sb.WriteString(on)
			}
			continue
		}
		for _, r := range seq {
			if pos == start {
				sb.WriteString(on)
				in = true
			}
			if pos == end {
				sb.WriteString(off)
				in = false
			}
			sb.WriteRune(r)
			pos++
		}
	}
	if in {
		sb.WriteString(off)
	}
	return sb.String()
}

func isSGR(seq string) bool {
	return len(seq) >= 3 && seq[0] == 0x1B && seq[1] == '[' && seq[len(seq)-1] == 'm'
}
