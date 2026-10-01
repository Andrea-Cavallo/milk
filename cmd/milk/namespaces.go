package main

import (
	"strings"

	"github.com/scoutme/milk/internal/workflow"
)

// Parameter name spaces for slash-command completion (#166).
//
// Command signatures in interactiveHelp mark parameter positions with
// <placeholder> tokens. When Tab lands on such a position, completion is
// resolved from the canonical source registered here — one lookup per name
// space, read live from config/state — instead of hardcoding name lists per
// command. New commands participate by using a <placeholder> whose text maps
// below (or by adding the parameter to freeTextParams when free text is the
// intent); TestParamNamespacesCoverHelpPlaceholders enforces that no
// placeholder silently escapes both lists.

const (
	nsAgent    = "agent"
	nsMCP      = "mcp"
	nsTool     = "tool"
	nsPanel    = "panel"
	nsWorkflow = "workflow"
)

// nsByPlaceholder maps the text inside <...> to a name space.
var nsByPlaceholder = map[string]string{
	"agent":       nsAgent,
	"server":      nsMCP,
	"server-name": nsMCP,
	"tool":        nsTool,
	"tool-agent":  nsTool,
	"panel":       nsPanel,
	"workflow":    nsWorkflow,
}

// nsByCmdScoped resolves generic placeholders (<name>) per command, so that
// "<name>" means agent names under /agent, MCP server names under /mcp, etc.
// Keyed "<command> <inner>" — command tokens include the slash.
var nsByCmdScoped = map[string]string{
	cmdAgent + " name":    nsAgent,
	cmdMCP + " name":      nsMCP,
	cmdPanel + " name":    nsPanel,
	cmdWorkflow + " name": nsWorkflow,
}

// freeTextParams lists placeholder texts that are intentionally completed as
// free text (paths, messages, ids, …). Membership here is a deliberate
// decision, not an accident: the drift test fails for any <placeholder> in
// the help text that is neither name-spaced nor listed here.
var freeTextParams = map[string]bool{
	"msg":              true,
	"fact":             true,
	"fact to remember": true,
	"pat":              true,
	"pat|#id":          true,
	"path":             true,
	"file":             true,
	"count":            true,
	"task":             true,
	"desc":             true,
	"description":      true,
	"prefix":           true,
	"verb":             true,
	"id":               true,
	"role":             true,
}

// namespaceForParam returns the name space a <placeholder> refers to for the
// given command, or "" when the parameter is free text.
func namespaceForParam(cmd, inner string) string {
	if ns, ok := nsByCmdScoped[cmd+" "+inner]; ok {
		return ns
	}
	return nsByPlaceholder[inner]
}

// paramExpectation describes what completion expects at one signature
// position: literal alternatives (subcommands, keywords like "for"/"as",
// option words like "global") and/or name spaces whose members to complete.
type paramExpectation struct {
	literals   []string
	namespaces []string
}

// parseSigPosition classifies the signature token at a completion position:
// it appends literal alternatives and/or name spaces to out. Free-text
// tokens (pat, msg, description=<desc>, --<role>, …) contribute nothing.
func parseSigPosition(cmd, tok string, out *paramExpectation) {
	w := strings.TrimPrefix(tok, "[")
	if w == "" {
		return
	}
	if strings.HasPrefix(w, "<") {
		// Placeholder, possibly followed by literal alternatives:
		//   "<name>"              → name space only
		//   "<agent>|global]"     → name space + literal "global"
		gt := strings.IndexByte(w, '>')
		if gt < 0 {
			return // malformed — treat as free text
		}
		inner := w[1:gt]
		if ns := namespaceForParam(cmd, inner); ns != "" {
			out.namespaces = append(out.namespaces, ns)
		}
		rest := strings.TrimPrefix(w[gt+1:], "|")
		rest = strings.TrimSuffix(rest, "]")
		if rest != "" {
			for _, alt := range strings.Split(rest, "|") {
				if alt = strings.TrimSpace(alt); alt != "" {
					out.literals = append(out.literals, alt)
				}
			}
		}
		return
	}
	if strings.Contains(w, "<") {
		// Mixed literal+placeholder token (description=<desc>, --<role>):
		// completing it would dispatch placeholder text — treat as free text.
		return
	}
	// Pure literal, possibly optional or an alternation:
	//   "[for" → "for"; "primary|escalation]" → primary, escalation.
	w = strings.TrimSuffix(w, "]")
	if w == "" {
		return
	}
	for _, alt := range strings.Split(w, "|") {
		if alt = strings.TrimSpace(alt); alt != "" {
			out.literals = append(out.literals, alt)
		}
	}
}

// sigWordMatchesTyped reports whether a typed word at an earlier signature
// position is consistent with that position's token: placeholders accept any
// typed value, literals must match (case-insensitive, either direction of
// prefix, to tolerate partially typed keywords).
func sigWordMatchesTyped(cmd, tok, typed string) bool {
	if strings.Contains(tok, "<") {
		return true
	}
	var exp paramExpectation
	parseSigPosition(cmd, tok, &exp)
	if len(exp.literals) == 0 {
		return true // degenerate token — don't over-reject
	}
	lower := strings.ToLower(typed)
	for _, alt := range exp.literals {
		la := strings.ToLower(alt)
		if strings.HasPrefix(lower, la) || strings.HasPrefix(la, lower) {
			return true
		}
	}
	return false
}

// prefixFold reports whether candidate completes partial (case-insensitive).
func prefixFold(partial, candidate string) bool {
	return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(partial))
}

// buildParamMatches completes the parameter at the cursor position within the
// signatures of the slash command in words. afterSpace means the cursor sits
// after whitespace, so the position is the next word (no partial to filter).
// Returns a value-mode tabBuild: matches are concrete values, not signatures.
func buildParamMatches(words []string, afterSpace bool, lookup paramLookup) tabBuild {
	if lookup == nil || len(words) == 0 {
		return tabBuild{}
	}
	// Locate the slash command — first slash token among the words.
	cmdIdx := -1
	for i, w := range words {
		if isSlashCmdToken(w) {
			cmdIdx = i
			break
		}
	}
	if cmdIdx < 0 {
		return tabBuild{}
	}
	rel := words[cmdIdx:]
	vs := cmdVariants[rel[0]]
	if len(vs) == 0 {
		return tabBuild{}
	}
	pos := len(rel) - 1
	partial := ""
	if !afterSpace {
		partial = rel[pos]
	} else {
		pos = len(rel)
	}
	if pos < 1 {
		return tabBuild{}
	}

	var exp paramExpectation
	seenLit := map[string]bool{}
	seenNS := map[string]bool{}
	for _, v := range vs {
		sigWords := strings.Fields(v.sig)
		if pos >= len(sigWords) {
			continue // this variant ends before the cursor position
		}
		ok := true
		for i := 1; i < pos; i++ {
			if i >= len(sigWords) || !sigWordMatchesTyped(rel[0], sigWords[i], rel[i]) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		var got paramExpectation
		parseSigPosition(rel[0], sigWords[pos], &got)
		for _, ns := range got.namespaces {
			if !seenNS[ns] {
				seenNS[ns] = true
				exp.namespaces = append(exp.namespaces, ns)
			}
		}
		for _, l := range got.literals {
			kl := strings.ToLower(l)
			if !seenLit[kl] {
				seenLit[kl] = true
				exp.literals = append(exp.literals, l)
			}
		}
	}

	// Collect values: name-space members first, then literals; filter by the
	// partial token (if any) and dedupe case-insensitively.
	var matches []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || !prefixFold(partial, s) {
			return
		}
		if seen[strings.ToLower(s)] {
			return
		}
		seen[strings.ToLower(s)] = true
		matches = append(matches, s)
	}
	for _, ns := range exp.namespaces {
		for _, val := range lookup(ns) {
			add(val)
		}
	}
	for _, l := range exp.literals {
		add(l)
	}
	if len(matches) == 0 {
		return tabBuild{}
	}
	label := ""
	if len(exp.namespaces) == 1 {
		label = exp.namespaces[0]
	}
	return tabBuild{matches: matches, valueMode: true, nsLabel: label}
}

// paramLookup resolves a parameter name space to its current member names.
// Values are read live from config/state — there is no separate cache to
// drift from what dispatch accepts (config load, /reload, and any in-session
// config mutation are picked up automatically on the next completion).
func (m model) paramLookup(ns string) []string {
	if m.st == nil {
		return nil
	}
	cfg := &m.st.cfg
	switch ns {
	case nsAgent:
		var out []string
		for _, a := range cfg.Agents {
			if a.Name != "" {
				out = append(out, a.Name)
			}
		}
		if len(out) == 0 {
			// Mirror execAgentList: fall back to the effective default agent.
			if n := cfg.ActiveAgent().Name; n != "" {
				out = append(out, n)
			}
		}
		return out
	case nsMCP:
		var out []string
		for _, s := range cfg.MCPServers {
			if s.Name != "" {
				out = append(out, s.Name)
			}
		}
		return out
	case nsTool:
		var out []string
		seen := map[string]bool{}
		add := func(name string) {
			if name == "" || seen[strings.ToLower(name)] {
				return
			}
			seen[strings.ToLower(name)] = true
			out = append(out, name)
		}
		for _, e := range cfg.AgentTools {
			add(e.Agent)
		}
		for _, ac := range cfg.Agents {
			for _, e := range ac.Tools {
				add(e.Agent)
			}
		}
		return out
	case nsPanel:
		var out []string
		for _, r := range panelRenderOrder {
			if n := panelSubName(r); n != "" {
				out = append(out, n)
			}
		}
		return out
	case nsWorkflow:
		reg, _ := workflow.LoadRegistry()
		if reg == nil {
			return nil
		}
		return reg.Names()
	}
	return nil
}
