package router

import (
	"context"
	"strings"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/obs"
	"github.com/scoutme/milk/internal/session"
)

// Confirmation-aware routing (issue #263, ADR-0065).
//
// Short "proceed/go ahead/yes" prompts carry no routing signals of their own,
// so the unconditional local_below_tokens shortcut (or a local verdict from the
// scorer) handed the *execution of the confirmed hard task* to the cheap
// primary agent. Instead: detect the confirmation, resolve what is being
// confirmed, and route on that content with the shortcut made marker-aware.

// confirmInfo is a parsed confirmation prompt.
type confirmInfo struct {
	// Scope is the new scope the prompt itself adds after the confirmation
	// phrase ("yes, do the big refactor" → "do the big refactor"), filler
	// stripped. Empty for pure confirmations ("proceed", "yes, go ahead").
	Scope string
}

// matchConfirmation reports whether prompt opens with a confirmation phrase
// and what scope (if any) it adds. Phrases match word-exact, case-insensitive,
// longest first, and may chain ("yes, please go ahead and do it" → pure).
func matchConfirmation(prompt string, phrases []string) (confirmInfo, bool) {
	if len(phrases) == 0 {
		phrases = config.DefaultConfirmationPhrases
	}
	words := confirmWords(prompt)
	if len(words) == 0 {
		return confirmInfo{}, false
	}
	i, matched := 0, false
	for i < len(words) {
		if n := matchPhraseAt(words, i, phrases); n > 0 {
			i += n
			matched = true
			continue
		}
		if isConfirmConnector(words[i]) {
			i++
			continue
		}
		break
	}
	if !matched {
		return confirmInfo{}, false
	}
	rest := words[i:]
	for len(rest) > 0 && isTailFiller(rest[0]) {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return confirmInfo{Scope: ""}, true
	}
	return confirmInfo{Scope: strings.Join(rest, " ")}, true
}

// resolveConfirmedScope decides what a pure confirmation refers to, in order:
//
//  1. the last agent message, when it proposes NEW scope ("want me to also
//     rename X?") — the question is what is being confirmed;
//  2. the pending task: the last non-confirmation user message (chained
//     confirmations walk back past their siblings; a tail-bearing one
//     contributes its scope as the task);
//  3. the last agent message as-is (a plan/proposal with no user task behind it);
//  4. the prompt itself — status-quo behaviour when there is no history.
//
// The bool is userAuthored: user words bind on their escalate markers; agent
// prose only binds on the specific escalate_keywords (see confirmationDecision).
func resolveConfirmedScope(sess *session.Session, prompt string, ci confirmInfo, cfg config.Config) (string, bool) {
	if ci.Scope != "" {
		return ci.Scope, true
	}
	var agentMsg string
	if sess != nil {
		for i := len(sess.History) - 1; i >= 0 && agentMsg == ""; i-- {
			t := sess.History[i]
			if t.Role == session.RoleAssistant && strings.TrimSpace(t.Content) != "" {
				agentMsg = t.Content
			}
		}
		if agentMsg != "" && hasNewScopeMarker(agentMsg, cfg.Rules.ConfirmationNewScopeMarkers) {
			return agentMsg, false
		}
		if task := lastSubstantiveUserScope(sess, prompt, cfg.Rules.ConfirmationPhrases); task != "" {
			return task, true
		}
		if agentMsg != "" {
			return agentMsg, false
		}
	}
	return prompt, true
}

// lastSubstantiveUserScope walks the history back for the last user message
// that is not itself a confirmation — pure confirmations are skipped, a
// tail-bearing one contributes its scope ("yes, do the big refactor" → the
// task is "do the big refactor"). prompt itself is skipped by text match in
// case the caller already recorded it.
func lastSubstantiveUserScope(sess *session.Session, prompt string, phrases []string) string {
	self := strings.TrimSpace(prompt)
	for i := len(sess.History) - 1; i >= 0; i-- {
		t := sess.History[i]
		if t.Role != session.RoleUser {
			continue
		}
		content := strings.TrimSpace(t.Content)
		if content == "" || content == self {
			continue
		}
		if ci, ok := matchConfirmation(content, phrases); ok {
			if ci.Scope != "" {
				return ci.Scope
			}
			continue
		}
		return content
	}
	return ""
}

// confirmationDecision routes the resolved confirmed scope. Differences from
// the plain rules layer, deliberate and bounded (ADR-0065):
//
//   - escalate markers in user-authored scope are conclusive: for a confirmed
//     task the hard/complex words are the only signal the user gave, and one
//     verb scores 4 < escalate_threshold 6 — left to the scorer the marker
//     would never bind on short scopes (which is exactly the bug);
//   - for agent-authored scope only escalate_keywords bind (agent prose uses
//     words like "reviewed"/"planned" incidentally);
//   - the short-prompt shortcut (local_below_tokens) is marker-aware instead
//     of unconditional — it still keeps small confirmed content local;
//   - everything else is the ordinary rules layer, so a long confirmed task
//     routes exactly as the same text would route typed directly.
func confirmationDecision(ctx context.Context, scope string, cfg config.Config, userAuthored bool) Decision {
	r := cfg.Rules
	lower := strings.ToLower(scope)
	below := r.LocalBelowTokens
	if below <= 0 {
		below = 30
	}
	tokens := approxTokens(scope)

	// Marker-aware short-prompt shortcut.
	if tokens <= below {
		if kw := escalateKeywordHit(lower, r.EscalateKeywords); kw != "" {
			return Decision{Target: TargetEscalation, Conclusive: true, Reason: "confirmation: keyword " + kw}
		}
		if userAuthored {
			if v := escalateVerbHit(lower, r.EscalateVerbs); v != "" {
				return Decision{Target: TargetEscalation, Conclusive: true, Reason: "confirmation: escalate verb " + v}
			}
		}
		return Decision{Target: TargetLocal, Conclusive: true, Reason: "confirmation: short confirmed content (local)"}
	}

	// Ordinary hard rules.
	if kw := escalateKeywordHit(lower, r.EscalateKeywords); kw != "" {
		return Decision{Target: TargetEscalation, Conclusive: true, Reason: "confirmation: keyword " + kw}
	}
	if tokens > r.EscalateAboveTokens && r.EscalateAboveTokens > 0 {
		return Decision{Target: TargetEscalation, Conclusive: true, Reason: "confirmation: long confirmed content"}
	}

	// Soft scoring on the scope.
	result := scoreSignals(scope, lower, cfg)
	obs.RecordScore(ctx, float64(result.TotalScore))
	var reasons []string
	for _, s := range result.Signals {
		reasons = append(reasons, s.Name)
	}
	reason := "confirmation signals: " + strings.Join(reasons, ", ")
	if result.TotalScore >= r.EscalateThreshold {
		return Decision{Target: TargetEscalation, Conclusive: true, Reason: reason}
	}
	if result.TotalScore <= r.LocalThreshold {
		return Decision{Target: TargetLocal, Conclusive: true, Reason: reason}
	}
	return Decision{Conclusive: false, Reason: reason}
}

// escalateKeywordHit returns the first escalate keyword matched in lower.
func escalateKeywordHit(lower string, keywords []string) string {
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return kw
		}
	}
	return ""
}

// escalateVerbHit returns the first escalate verb matched in lower.
func escalateVerbHit(lower string, verbs []string) string {
	for _, v := range verbs {
		if strings.Contains(lower, strings.ToLower(v)) {
			return v
		}
	}
	return ""
}

// hasNewScopeMarker reports whether text proposes new scope ("want me to also
// rename X?") rather than asking to continue the pending task. Single-word
// markers match on word boundaries ("too" must not fire on "tools").
func hasNewScopeMarker(text string, markers []string) bool {
	if len(markers) == 0 {
		markers = config.DefaultConfirmationNewScopeMarkers
	}
	words := confirmWords(text)
	joined := " " + strings.Join(words, " ") + " "
	lower := strings.ToLower(text)
	for _, m := range markers {
		if strings.Contains(m, " ") {
			if strings.Contains(lower, strings.ToLower(m)) {
				return true
			}
			continue
		}
		if strings.Contains(joined, " "+strings.ToLower(m)+" ") {
			return true
		}
	}
	return false
}

// confirmationConnectors are politeness/flow words consumed between
// confirmation phrases ("yes, please go ahead and do it").
var confirmationConnectors = []string{
	"and", "but", "so", "then", "now", "please", "thanks", "well",
	"grazie", "prego", "poi", "allora",
}

// confirmationTailFiller are leading words stripped from the scope tail after
// a confirmation phrase ("proceed with the refactor" → "refactor").
var confirmationTailFiller = []string{"with", "on", "the", "a", "an", "this", "that"}

// confirmWords normalizes text to lowercase word tokens with edge punctuation
// stripped ("proceed," → "proceed", "d'accordo" keeps its apostrophe).
func confirmWords(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,!?;:\"'()[]{}\u201c\u201d\u2018\u2019\u00ab\u00bb\u2026*#")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// matchPhraseAt matches the longest phrase (word sequence) at words[i],
// returning the number of words consumed, or 0.
func matchPhraseAt(words []string, i int, phrases []string) int {
	best := 0
	for _, p := range phrases {
		pw := confirmWords(p)
		if len(pw) == 0 || len(pw) <= best || i+len(pw) > len(words) {
			continue
		}
		ok := true
		for j := range pw {
			if words[i+j] != pw[j] {
				ok = false
				break
			}
		}
		if ok {
			best = len(pw)
		}
	}
	return best
}

func isConfirmConnector(w string) bool {
	for _, c := range confirmationConnectors {
		if w == c {
			return true
		}
	}
	return false
}

func isTailFiller(w string) bool {
	for _, c := range confirmationTailFiller {
		if w == c {
			return true
		}
	}
	return false
}
