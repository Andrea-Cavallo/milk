// Package contextreduce implements the layered context-reduction ladder used
// when a prompt is crowding the model's context window (issue #236). As
// pressure rises, LevelFor reports which reduction level the caller should
// apply: stepping the system-prompt tier down and dropping optional guidance
// first, then compacting tool descriptions, and finally pruning the tool set
// to what is actually in use plus a protected core.
//
// Everything here is pure: the helpers compute reduction decisions and
// transformed copies of caller data. Applying a Level to a live prompt is the
// caller's job.
package contextreduce

import (
	"strings"
	"unicode/utf8"
)

// Level selects how aggressively a prompt should be reduced under context
// pressure. The levels are cumulative: each one includes the reductions of
// every level before it.
type Level int

const (
	// LevelNone applies no reduction: full instructions + full tools.
	LevelNone Level = iota
	// LevelInstructions steps the system-prompt tier down one level and
	// drops optional guidance blocks.
	LevelInstructions
	// LevelToolsCompact adds LevelInstructions reductions, plus trims the
	// project-instructions block and one-lines tool descriptions.
	LevelToolsCompact
	// LevelToolsPrune adds LevelToolsCompact reductions, plus prunes the
	// tool set to observed-in-use ∪ protected.
	LevelToolsPrune
)

// Policy holds the pressure thresholds (in percent of the context window) at
// which each reduction level kicks in, plus the knobs the reduction steps
// themselves need. Zero-valued threshold fields fall back to the
// DefaultPolicy values.
type Policy struct {
	Enabled                 bool
	InstructionThresholdPct int      // default 55
	ToolCompactThresholdPct int      // default 65
	ToolPruneThresholdPct   int      // default 75
	InstructionsTrimChars   int      // default 4000
	ProtectedTools          []string // default {"bash","read_file"}
}

const (
	defaultInstructionThresholdPct = 55
	defaultToolCompactThresholdPct = 65
	defaultToolPruneThresholdPct   = 75
	defaultInstructionsTrimChars   = 4000
)

// trimMarker replaces the dropped middle of a trimmed block. It is counted
// inside the caller's maxChars budget.
const trimMarker = "\n... (truncated) ...\n"

// compactDescCap is the rune cap applied to compacted tool descriptions.
const compactDescCap = 120

// DefaultPolicy returns the enabled policy with the documented defaults.
func DefaultPolicy() Policy {
	return Policy{
		Enabled:                 true,
		InstructionThresholdPct: defaultInstructionThresholdPct,
		ToolCompactThresholdPct: defaultToolCompactThresholdPct,
		ToolPruneThresholdPct:   defaultToolPruneThresholdPct,
		InstructionsTrimChars:   defaultInstructionsTrimChars,
		ProtectedTools:          []string{"bash", "read_file"},
	}
}

// LevelFor reports the reduction level for a prompt occupying promptTokens of
// a denominator-token context window. Pressure is promptTokens*100/denominator
// (integer division), so a threshold is hit exactly when pct >= threshold.
//
// LevelNone is returned when the policy is disabled or either argument is
// non-positive. Zero-valued threshold fields fall back to the DefaultPolicy
// values; thresholds are then clamped into 1..99 and ordered so that
// InstructionThresholdPct <= ToolCompactThresholdPct <= ToolPruneThresholdPct
// (a later threshold is raised to the earlier one rather than lowering the
// earlier one, so no reduction tier becomes more aggressive than requested).
func LevelFor(promptTokens, denominator int, p Policy) Level {
	if !p.Enabled || promptTokens <= 0 || denominator <= 0 {
		return LevelNone
	}
	instr := resolveThreshold(p.InstructionThresholdPct, defaultInstructionThresholdPct)
	compact := resolveThreshold(p.ToolCompactThresholdPct, defaultToolCompactThresholdPct)
	prune := resolveThreshold(p.ToolPruneThresholdPct, defaultToolPruneThresholdPct)
	if compact < instr {
		compact = instr
	}
	if prune < compact {
		prune = compact
	}
	pct := promptTokens * 100 / denominator
	switch {
	case pct >= prune:
		return LevelToolsPrune
	case pct >= compact:
		return LevelToolsCompact
	case pct >= instr:
		return LevelInstructions
	default:
		return LevelNone
	}
}

// resolveThreshold substitutes the default for a zero-valued field, then
// clamps the result into 1..99.
func resolveThreshold(v, def int) int {
	if v == 0 {
		v = def
	}
	if v < 1 {
		v = 1
	}
	if v > 99 {
		v = 99
	}
	return v
}

// StepDownTier steps a system-prompt tier name down one level: "full" becomes
// "standard", and everything else ("standard", "", "minimal", unknown) is
// already at the floor and becomes "minimal".
func StepDownTier(tier string) string {
	if tier == "full" {
		return "standard"
	}
	return "minimal"
}

// TrimBlock bounds s to maxChars using head+tail truncation: the first ~2/3
// and last ~1/3 of the budget are kept and joined by trimMarker, with the
// marker counted inside maxChars so the result never exceeds it. The cut is
// rune-safe. If len(s) <= maxChars or maxChars <= 0, s is returned unchanged;
// when maxChars is too small to fit the marker at all, the result falls back
// to plain rune-safe head truncation.
func TrimBlock(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	budget := maxChars - len(trimMarker)
	if budget <= 0 {
		return runeSafeHead(s, maxChars)
	}
	head := runeSafeHead(s, budget*2/3)
	tail := runeSafeTail(s, budget-len(head))
	return head + trimMarker + tail
}

// runeSafeHead returns s[:n] shortened so it does not end mid-rune.
func runeSafeHead(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n < 0 {
		n = 0
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// runeSafeTail returns s[len(s)-n:] advanced so it does not start mid-rune.
func runeSafeTail(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n < 0 {
		n = 0
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// CompactToolDescription reduces a tool description to a single line: the
// first sentence — text up to the first ". " or newline, whichever comes
// first — capped at 120 runes (cut with a trailing "…"), then TrimSpace'd.
// Empty input stays empty.
func CompactToolDescription(desc string) string {
	if desc == "" {
		return ""
	}
	cut := len(desc)
	if i := strings.Index(desc, ". "); i >= 0 {
		cut = i
	}
	if i := strings.IndexByte(desc, '\n'); i >= 0 && i < cut {
		cut = i
	}
	s := desc[:cut]
	if runes := []rune(s); len(runes) > compactDescCap {
		s = string(runes[:compactDescCap-1]) + "…"
	}
	return strings.TrimSpace(s)
}

// CompactTools returns a new slice of new deep-copied tool maps in which every
// "description" string — at the top level and at the nested "function" level
// of the standard OpenAI tool shape
// ({"type":"function","function":{"name":…,"description":…,"parameters":…}}) —
// is run through CompactToolDescription. The input and its nested maps are
// never mutated, and the copies share no maps with the input.
func CompactTools(tools []map[string]any) []map[string]any {
	if tools == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		cp := cloneMap(tool)
		compactDescriptionField(cp)
		if fn, ok := cp["function"].(map[string]any); ok {
			compactDescriptionField(fn)
		}
		out = append(out, cp)
	}
	return out
}

// compactDescriptionField rewrites m["description"] in place (on a copy) when
// it is a string. Non-string descriptions are left alone.
func compactDescriptionField(m map[string]any) {
	if desc, ok := m["description"].(string); ok {
		m["description"] = CompactToolDescription(desc)
	}
}

// cloneMap deep-copies a tool map (nested map[string]any and slice values are
// copied too) so the returned tools never alias the input.
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = cloneValue(v)
	}
	return cp
}

// cloneValue deep-copies the composite values reachable from a tool map.
// Scalars and other opaque values are shared as-is.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		cp := make([]any, len(t))
		for i, e := range t {
			cp[i] = cloneValue(e)
		}
		return cp
	case []map[string]any:
		cp := make([]map[string]any, len(t))
		for i, e := range t {
			cp[i] = cloneMap(e)
		}
		return cp
	default:
		return v
	}
}

// PruneTools keeps only the tools whose name — the top-level "name" or the
// nested "function"→"name" string — is in used ∪ protected, preserving input
// order. If used is nil/empty the input is returned unchanged; otherwise a new
// slice is built and the input is never mutated.
func PruneTools(tools []map[string]any, used map[string]bool, protected []string) []map[string]any {
	if len(used) == 0 {
		return tools
	}
	keep := make(map[string]bool, len(used)+len(protected))
	for name, isUsed := range used {
		if isUsed {
			keep[name] = true
		}
	}
	for _, name := range protected {
		keep[name] = true
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if toolKept(tool, keep) {
			out = append(out, tool)
		}
	}
	return out
}

// toolKept reports whether any of the tool's names — the top-level "name" or
// the nested "function"→"name" string — is in keep. Name-less tools are never
// kept.
func toolKept(tool map[string]any, keep map[string]bool) bool {
	if name, ok := tool["name"].(string); ok && name != "" && keep[name] {
		return true
	}
	if fn, ok := tool["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok && name != "" && keep[name] {
			return true
		}
	}
	return false
}
