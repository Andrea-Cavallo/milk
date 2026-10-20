package workflow

import (
	"sort"
	"strings"
)

// WorkflowVerdict is the structured outcome of an evaluator turn.
type WorkflowVerdict int

const (
	VerdictUnknown         WorkflowVerdict = iota
	VerdictGoodToGo                        // sprint accepted; advance or finish
	VerdictNeedsRefinement                 // retry same sprint
	VerdictSprintDone                      // sprint deliverables complete; advance unconditionally
)

func (v WorkflowVerdict) String() string {
	switch v {
	case VerdictGoodToGo:
		return "good_to_go"
	case VerdictNeedsRefinement:
		return "needs_refinement"
	case VerdictSprintDone:
		return "sprint_done"
	default:
		return "unknown"
	}
}

// ParseVerdictKey extracts a verdict from response by matching the stage's
// OWN declared rule keys — case-insensitively, scanning lines from the end
// so the final verdict line wins (the same line discipline ParseVerdict
// uses). The fallback key "unknown" is never searched for (prose contains
// the word); callers use "unknown" when this returns "". The longest key
// wins when one line matches several, so "all_done" beats "done".
//
// This is what makes declaration-defined verdicts real: ParseVerdict only
// knows the three historical keywords, while a stage may declare any words
// it prompts for (team's all_done / more_work, issue #197).
func ParseVerdictKey(response string, rules map[string]VerdictRule) string {
	keys := make([]string, 0, len(rules))
	for k := range rules {
		if k == "unknown" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	lines := strings.Split(response, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		for _, k := range keys {
			if strings.Contains(line, strings.ToLower(k)) {
				return k
			}
		}
	}
	return ""
}

// ParseVerdict extracts a structured verdict from the evaluator's text response.
// It scans lines from the end of the response and returns the verdict of the
// last line that contains a recognised keyword (case-insensitive). Scanning
// from the end ensures that the evaluator's final verdict line wins over any
// discussion of verdicts earlier in the reasoning section.
func ParseVerdict(response string) WorkflowVerdict {
	lines := strings.Split(response, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		switch {
		case strings.Contains(line, "good_to_go") || strings.Contains(line, "good to go"):
			return VerdictGoodToGo
		case strings.Contains(line, "sprint_done") || strings.Contains(line, "sprint done"):
			return VerdictSprintDone
		case strings.Contains(line, "needs_refinement") || strings.Contains(line, "needs refinement"):
			return VerdictNeedsRefinement
		}
	}
	return VerdictUnknown
}
