package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The tool-activity manifest sits in the model's replayed context
// (local.formatOwnTurn), so a model sometimes echoes the annotation format
// into its own reply. The echo arrives as plain text and must render with
// the same dim [milk] tag milk writes itself — tag identical to milkTag(),
// everything else byte-identical.

func TestDimMilkAnnotationRows_PlainManifestGetsStandardTag(t *testing.T) {
	in := "reply text here\n\n[milk] tool activity in that turn: bash ×3, edit_file ×1; files: a.go\nmore text"
	got := dimMilkAnnotationRows(in)
	want := "reply text here\n\n" + milkTag() + " tool activity in that turn: bash ×3, edit_file ×1; files: a.go\nmore text"
	if got != want {
		t.Errorf("dimMilkAnnotationRows =\n%q\nwant\n%q", got, want)
	}
	// The tag bytes are exactly milkTag()'s — the dimmed and the echoed line
	// are indistinguishable on screen.
	if !strings.Contains(got, milkTag()+" tool activity") {
		t.Errorf("tag does not match milkTag() byte-for-byte: %q", got)
	}
}

func TestDimMilkAnnotationRows_AlreadyStyledAndForeignRowsUntouched(t *testing.T) {
	cases := []string{
		// milk's own annotation line: already dim on the tag — no double-dim.
		milkTag() + " usage: /help",
		// plain rows without an annotation
		"just a reply",
		"",
		// [milk] mid-line is model prose, not a line-leading annotation
		"see the [milk] tool activity line above",
		// rows carrying other styling are left alone
		green("agent:") + " text",
		// the annotation header alone without its mandatory space is not one
		"[milk]",
	}
	for _, in := range cases {
		if got := dimMilkAnnotationRows(in); got != in {
			t.Errorf("dimMilkAnnotationRows(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestDimMilkAnnotationRows_LegacyPrefixDimmed(t *testing.T) {
	in := "[Tool activity in that turn: bash ×8, edit_file ×2]"
	got := dimMilkAnnotationRows(in)
	if !strings.HasPrefix(got, dim(legacyAnnotationPrefix)) {
		t.Errorf("legacy annotation prefix not dimmed: %q", got)
	}
	if !strings.HasSuffix(got, " bash ×8, edit_file ×2]") {
		t.Errorf("legacy annotation body altered: %q", got)
	}
}

func TestDimMilkAnnotationRows_MixedRowsOnlyAnnotationsChange(t *testing.T) {
	in := "plain one\n[milk] tool activity in that turn: grep ×2\nplain two\n[milk] another one"
	got := dimMilkAnnotationRows(in)
	rows := strings.Split(got, "\n")
	if len(rows) != 4 {
		t.Fatalf("row count changed: %d", len(rows))
	}
	if rows[0] != "plain one" || rows[2] != "plain two" {
		t.Errorf("non-annotation rows changed: %q", rows)
	}
	if !strings.HasPrefix(rows[1], milkTag()) || !strings.HasPrefix(rows[3], milkTag()) {
		t.Errorf("annotation rows not dimmed: %q", rows)
	}
}

// The wiring: tintFullRows (the one render-stage choke point) applies it,
// and the styling is render-only — the buffer and the plain coordinate
// space (selection, copy) never see the escapes.
func TestDimMilkAnnotationRows_RenderStageAndPlainSpace(t *testing.T) {
	plain := "some reply\n\n[milk] tool activity in that turn: bash ×1"

	// Stage wiring: through tintFullRows the tag comes out dimmed.
	rendered := tintFullRows(ansi.Strip(ansi.Wrap(plain, 80, "")), 80)
	if !strings.Contains(rendered, milkTag()+" tool activity") {
		t.Errorf("tintFullRows did not dim the annotation tag: %q", rendered)
	}

	// Model-level: transcript renders dimmed, buffer stays plain.
	m := layoutTestModel(t, 100, 30)
	m.transcript.WriteString(plain)
	out := m.wrappedTranscript()
	if !strings.Contains(out, milkTag()+" tool activity") {
		t.Errorf("rendered transcript lacks the dim tag: %q", out)
	}
	if got := m.transcript.String(); got != plain {
		t.Errorf("render mutated the transcript buffer: %q", got)
	}
	for _, l := range m.transcriptPlainLines() {
		if l == plain || l == "[milk] tool activity in that turn: bash ×1" {
			// plain coordinate space: exactly the original text, no escapes
			continue
		}
		if strings.Contains(l, "\x1b") {
			t.Errorf("plain line kept ANSI escapes: %q", l)
		}
	}
	if got := strings.Join(m.transcriptPlainLines(), "\n"); !strings.Contains(got, "[milk] tool activity") {
		t.Errorf("plain space lost the annotation text: %q", got)
	}
}
