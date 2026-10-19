package main

import (
	"slices"
	"strings"
	"testing"
)

// The registry (slashcmd.go) is the single source for the command language
// both hosts speak. These tests are the invariants that grant it: the host
// handler tables stay in sync with the registry in both directions, the help
// text and completion set cover the same commands, and every listing a user
// or a client sees is rendered from registry data.

// TestRegistry_ACPSurface pins the ACP binding table to the registry in both
// directions: every ACP-hosted spec has exactly one handler binding, every
// binding resolves to a registry spec, and what available_commands_update
// advertises is exactly the registry's non-hidden ACP rows.
func TestRegistry_ACPSurface(t *testing.T) {
	bound := map[string]bool{}
	for _, c := range acpCommandTable() {
		if bound[c.name] {
			t.Errorf("duplicate binding for %s", c.name)
		}
		bound[c.name] = true
		spec, ok := specFor(c.name)
		if !ok {
			t.Errorf("binding %q is not in the registry", c.name)
			continue
		}
		if !spec.in(hostACP) {
			t.Errorf("binding %q exists but the registry says hosts=%d (not ACP)", c.name, spec.hosts)
		}
		if c.run == nil && c.runTurn == nil {
			t.Errorf("binding %q has neither run nor runTurn", c.name)
		}
		if !spec.hidden && spec.desc == "" {
			t.Errorf("advertised command %q has no desc", c.name)
		}
	}
	for _, s := range commandRegistry {
		if !s.in(hostACP) {
			continue
		}
		if !bound[s.name] {
			t.Errorf("registry spec %q is ACP-hosted but has no handler binding", s.name)
		}
	}
}

// TestRegistry_TUISurface pins the TUI's recognition and help surface to the
// registry: extractSlashCommand's set is exactly the registry's names, and
// the help text documents every visible TUI-hosted command.
func TestRegistry_TUISurface(t *testing.T) {
	if !slices.Equal(slashCommands, registryNames()) {
		t.Error("slashCommands must be registryNames() — the registry is the recognition set")
	}
	regNames := map[string]bool{}
	for _, s := range commandRegistry {
		regNames[s.name] = true
		for _, a := range s.aliases {
			regNames[a] = true
		}
	}
	for _, name := range slashCommands {
		if !regNames[name] {
			t.Errorf("slashCommands entry %q not in registry", name)
		}
	}
	help := buildInteractiveHelp()
	for _, s := range commandRegistry {
		if !s.in(hostTUI) || s.hidden {
			continue
		}
		// The name must appear in help — a shared line may document it
		// alongside a sibling ("/exit  /quit").
		if !strings.Contains(help, s.name) {
			t.Errorf("help text does not mention %q", s.name)
		}
	}
}

// TestRegistry_GrammarConsistency pins the argument grammar: every help line
// belongs to the command it is filed under, and the compact ACP hint never
// invents a literal the TUI's help lines do not show.
func TestRegistry_GrammarConsistency(t *testing.T) {
	for _, s := range commandRegistry {
		if s.help == "" {
			continue
		}
		for _, line := range strings.Split(s.help, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
				continue // continuation line
			}
			own := fields[0] == s.name || slices.Contains(s.aliases, fields[0])
			if !own {
				t.Errorf("%s: help line starts with %q — filed under the wrong command", s.name, fields[0])
			}
		}
		for _, tok := range hintLiterals(s.hint) {
			if !strings.Contains(s.help, tok) {
				t.Errorf("%s: ACP hint literal %q never appears in its help lines", s.name, tok)
			}
		}
	}
	// Completion variants come from the same generated help, so they see the
	// same grammar: every variant key is a registry command.
	for cmd := range cmdVariants {
		if !regName(cmd) {
			t.Errorf("cmdVariants key %q is not a registry name", cmd)
		}
	}
}

// TestRegistry_ListingsFromRegistry pins that what clients see is registry
// data: /help and available_commands_update describe exactly the
// non-hidden ACP rows, with the registry's desc and hint.
func TestRegistry_ListingsFromRegistry(t *testing.T) {
	want := map[string]cmdSpec{}
	for _, s := range commandRegistry {
		if s.in(hostACP) && !s.hidden {
			want[s.name] = s
		}
	}
	advertised := acpAdvertisedCommands()
	if len(advertised) != len(want) {
		t.Errorf("advertised %d commands, registry has %d non-hidden ACP specs", len(advertised), len(want))
	}
	for _, a := range advertised {
		name := "/" + a.Name
		spec, ok := want[name]
		if !ok {
			t.Errorf("advertised %q is not a non-hidden registry ACP spec", name)
			continue
		}
		if a.Description != spec.desc {
			t.Errorf("%s: description %q, registry says %q", name, a.Description, spec.desc)
		}
		wantHint := ""
		if spec.hint != "" {
			wantHint = spec.hint
		}
		gotHint := ""
		if a.Input != nil {
			gotHint = a.Input.Hint
		}
		if gotHint != wantHint {
			t.Errorf("%s: hint %q, registry says %q", name, gotHint, wantHint)
		}
	}
	// /help (the ACP listing) shows every advertised signature.
	out, _ := acpHelp(nil, "")
	for _, s := range want {
		sig := s.name
		if s.hint != "" {
			sig += " " + s.hint
		}
		if !strings.Contains(out, sig+" — "+s.desc) {
			t.Errorf("acpHelp does not list %q", sig)
		}
	}
}

func regName(cmd string) bool {
	_, ok := specFor(cmd)
	return ok
}

// hintLiterals extracts the literal (non-placeholder) words of a compact
// argument grammar like "[list|start <task>|stop <id>]" — the words a user
// must type. Tokens touched by brackets or angle brackets are placeholders
// ("<id|prefix|name>", "[name]", …) and are skipped; what remains must show
// up in the command's help lines.
func hintLiterals(hint string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(hint, func(r rune) bool {
		return r == ' ' || r == '|' || r == ']' || r == '['
	}) {
		if strings.ContainsAny(tok, "<>[]") {
			continue // placeholder
		}
		out = append(out, tok)
	}
	return out
}

// TestRegistry_HintsCoverArguedUsage pins the tip-derivation contract: a
// command whose help lines show argument usage carries a hint. The hint is
// what registryTips renders into "{/name <hint>}" and what ACP advertises
// as the input grammar — an argued command without one would advertise
// itself bare on both surfaces (the /server gap).
func TestRegistry_HintsCoverArguedUsage(t *testing.T) {
	for _, s := range commandRegistry {
		if s.hint != "" {
			continue
		}
		for _, line := range strings.Split(s.help, "\n") {
			fields := strings.Fields(line)
			// An argument follows the name immediately as a <placeholder>
			// or [optional] part — description prose never starts that way.
			if len(fields) > 1 && fields[0] == s.name && strings.ContainsAny(fields[1], "<[") {
				t.Errorf("%s: help line %q shows arguments but the registry has no hint — add one", s.name, strings.TrimSpace(line))
			}
		}
	}
}
