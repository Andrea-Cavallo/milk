package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scoutme/milk/internal/textbudget"
)

// ExportOptions controls optional content in an export. The zero value is
// the default export: everything the session stored.
type ExportOptions struct {
	// NoReasoning strips assistant reasoning (Turn.Thinking) from the
	// export — the text fences and the JSON "thinking" fields alike
	// (issue #277). Reasoning is INCLUDED by default: the export is the
	// audit trail of why the agent decided something, and it is
	// deterministic by construction — only session data is read, so TUI
	// fold/click state (ADR-0067) can never leak in and two exports of
	// the same session are byte-identical no matter how the thinking
	// blocks were folded while browsing.
	NoReasoning bool
}

// exportReasoningMax bounds one reasoning block in a text export:
// textbudget keeps the head ~2/3 and tail ~1/3 with an omission note, so a
// 10k-line think is summarized instead of dominating the file — the same
// 8 KB per-message budget the history replay uses.
const exportReasoningMax = 8192

// ExportJSON serialises the session as indented JSON (reasoning included —
// the session stores it, the export shows it).
func ExportJSON(s *Session) ([]byte, error) {
	return ExportJSONWithOptions(s, ExportOptions{})
}

// ExportJSONWithOptions serialises the session as indented JSON honouring
// opts. With NoReasoning the document is walked generically (json.Number —
// no numeric precision is lost) and each history entry's "thinking" key is
// dropped; the session itself is never mutated.
func ExportJSONWithOptions(s *Session, opts ExportOptions) ([]byte, error) {
	if !opts.NoReasoning {
		return json.MarshalIndent(s, "", "  ")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if root, ok := v.(map[string]any); ok {
		if hist, ok := root["history"].([]any); ok {
			for _, e := range hist {
				if m, ok := e.(map[string]any); ok {
					delete(m, "thinking")
				}
			}
		}
	}
	return json.MarshalIndent(v, "", "  ")
}

// ExportText renders the session as a human-readable transcript (plain text, no ANSI).
func ExportText(s *Session) string {
	return exportText(s, false, ExportOptions{})
}

// ExportTextColorized renders the session as a human-readable transcript with ANSI color
// highlighting on turn headers. Use this when writing to a terminal; use ExportText for
// file output.
func ExportTextColorized(s *Session) string {
	return exportText(s, true, ExportOptions{})
}

// ExportTextWithOptions is ExportText honouring opts (issue #277).
func ExportTextWithOptions(s *Session, opts ExportOptions) string {
	return exportText(s, false, opts)
}

// ExportTextColorizedWithOptions is ExportTextColorized honouring opts.
func ExportTextColorizedWithOptions(s *Session, opts ExportOptions) string {
	return exportText(s, true, opts)
}

// exportText is the shared implementation for the text export variants.
// When colorize is true, ANSI escape codes are applied to the rule and header line of
// each turn; body content is always emitted as-is.
func exportText(s *Session, colorize bool, opts ExportOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session: %s\n", s.ID)
	if s.Name != "" {
		fmt.Fprintf(&b, "name:    %s\n", s.Name)
	}
	fmt.Fprintf(&b, "cwd:     %s\n", s.CWD)
	fmt.Fprintf(&b, "created: %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "state:   %s\n", s.State)
	fmt.Fprintf(&b, "turns:   %d\n", len(s.History))

	const rule = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

	// ansi wraps text in an ANSI escape sequence when colorize is true.
	ansi := func(s, code string) string {
		if !colorize {
			return s
		}
		return code + s + "\033[0m"
	}

	for i, t := range s.History {
		ts := t.Timestamp.Format("2006-01-02 15:04:05")
		if i > 0 {
			fmt.Fprintln(&b)
		}

		// Role-specific ANSI codes: headerCode is bold+color for the role label;
		// ruleCode is the same hue without bold for the horizontal rule.
		// Colors match the live chat colors exactly:
		//   user       → bold white  (neutral, not confused with any agent)
		//   primary    → bold green  (matches live primary agent label)
		//   escalation → bold blue   (matches live escalation agent label)
		//   tool result→ dim yellow
		//   thinking   → bold magenta
		var headerCode, ruleCode string
		switch t.Role {
		case RoleUser:
			headerCode, ruleCode = "\033[1;37m", "\033[37m"
		case RoleAssistant:
			switch t.Agent {
			case AgentLocal: // primary
				headerCode, ruleCode = "\033[1;32m", "\033[32m"
			case AgentEscalation:
				headerCode, ruleCode = "\033[1;34m", "\033[34m"
			default:
				headerCode, ruleCode = "\033[1;36m", "\033[36m"
			}
		case RoleToolResult:
			headerCode, ruleCode = "\033[2;33m", "\033[33m"
		default: // thinking or unknown roles
			headerCode, ruleCode = "\033[1;35m", "\033[35m"
		}

		fmt.Fprintln(&b, ansi(rule, ruleCode))

		dimTS := ansi(ts, "\033[2m")
		sep := ansi("  ·  ", "\033[2m")

		switch t.Role {
		case RoleUser:
			fmt.Fprintf(&b, "%s%s%s\n%s\n", dimTS, sep, ansi("user", headerCode), t.Content)
		case RoleAssistant:
			agent := string(t.Agent)
			fmt.Fprintf(&b, "%s%s%s\n", dimTS, sep, ansi(agent, headerCode))
			// Reasoning as a marked section between the prompt and the
			// reply (issue #277): chronological — the model thought before
			// it acted — greppable (<reasoning>…</reasoning>) and
			// skippable, budgeted like any other content. The body is the
			// session's own bytes (only newlines at the very end are
			// trimmed so the closing marker sits flush); markers carry the
			// thinking hue when colorized.
			if !opts.NoReasoning && t.Thinking != "" {
				body := textbudget.SummarizeLong(t.Thinking, exportReasoningMax)
				body = strings.TrimRight(body, "\n")
				fmt.Fprintf(&b, "%s\n%s\n%s\n",
					ansi("  <reasoning>", "\033[1;35m"), body, ansi("  </reasoning>", "\033[1;35m"))
			}
			if len(t.ToolCalls) > 0 {
				for _, tc := range t.ToolCalls {
					fmt.Fprintf(&b, "  » %s: %s\n", tc.Name, tc.Arguments)
				}
			} else {
				fmt.Fprintf(&b, "%s\n", t.Content)
			}
		case RoleToolResult:
			content := t.Content
			if len(content) > 300 {
				content = content[:297] + "..."
			}
			fmt.Fprintf(&b, "%s%s%s\n  → %s\n", dimTS, sep, ansi("tool result", headerCode), content)
		}
	}
	return b.String()
}
