package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapRowsKeepsStyleOnEveryRow(t *testing.T) {
	var b strings.Builder
	for _, w := range strings.SplitAfter("alpha beta gamma delta epsilon zeta eta theta iota kappa", " ") {
		b.WriteString(ansiDim)
		b.WriteString(w)
		b.WriteString(ansiReset)
	}
	b.WriteString("plain")
	rows := strings.Split(wrapRows(b.String(), 20), "\n")
	if len(rows) < 3 {
		t.Fatalf("expected wrapping, got %q", rows)
	}
	for i, row := range rows[:len(rows)-1] {
		if !strings.HasPrefix(row, ansiDim) || !strings.HasSuffix(row, ansiReset) {
			t.Errorf("row %d is not a self-contained dim row: %q", i, row)
		}
	}
	if got, want := ansi.Strip(strings.Join(rows, "")), strings.ReplaceAll(ansi.Strip(b.String()), " ", ""); strings.ReplaceAll(got, " ", "") != want {
		t.Errorf("visible text changed: %q", got)
	}
}
