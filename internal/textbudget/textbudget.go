// Package textbudget provides a small, shared head+tail truncation helper
// for bounding large text before it's spliced into a prompt. It exists so
// callers outside internal/workflow/interp (which has its own budgeted
// truncation helpers for template variables) don't have to reinvent the
// same head/tail-preserving truncation — see
// docs/prompt-context-management-review.md §8 rec #3.
package textbudget

import "fmt"

// SummarizeLong keeps the first ~2/3 and last ~1/3 of s within maxChars,
// replacing the middle with an omission marker, when s exceeds maxChars.
// Keeping both ends rather than just the head preserves whatever summary or
// verdict typically sits at the end of an agent's output, alongside the
// framing that typically sits at the start.
func SummarizeLong(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	keepHead := maxChars * 2 / 3
	keepTail := maxChars / 3
	return s[:keepHead] +
		fmt.Sprintf("\n\n[... %d chars omitted ...]\n\n", len(s)-keepHead-keepTail) +
		s[len(s)-keepTail:]
}
