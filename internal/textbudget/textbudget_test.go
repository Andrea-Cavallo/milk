package textbudget

import (
	"strings"
	"testing"
)

func TestSummarizeLong_WithinLimit(t *testing.T) {
	s := "short"
	if got := SummarizeLong(s, 100); got != s {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestSummarizeLong_PreservesHeadAndTail(t *testing.T) {
	s := "HEAD" + strings.Repeat("x", 1000) + "TAIL"
	got := SummarizeLong(s, 100)
	if !strings.HasPrefix(got, "HEAD") {
		t.Errorf("expected result to start with HEAD, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Errorf("expected result to end with TAIL, got %q", got[len(got)-20:])
	}
	if len(got) >= len(s) {
		t.Errorf("expected truncated result to be shorter than input")
	}
}

func TestSummarizeLong_ZeroMaxCharsNoOp(t *testing.T) {
	s := strings.Repeat("x", 100)
	if got := SummarizeLong(s, 0); got != s {
		t.Errorf("expected unchanged when maxChars is 0, got %q", got)
	}
}
