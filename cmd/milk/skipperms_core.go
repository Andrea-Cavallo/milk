package main

import "strings"

// /skip-permissions' grammar, shared by the TUI (execSkipPerms) and the ACP
// server (acpSkipPerms) — issue #188's slash-table row: one parse, one result
// wording. The hosts differ only in storage (a model field vs an atomic) and
// styling (the TUI milk-tags and reds the line; ACP sends it plain as a slash
// result).
//
// Grammar (unified here; the TUI's old default case answered a bogus argument
// with the state report instead of a usage line):
//
//	on|off   set the state and report it (changed = true)
//	(empty)  report the current state
//	other    usage line, state unchanged
//
// changed tells the caller this was a real toggle, so it can also clear the
// session allow-all/deny-all mode (issue #302): skip-permissions and the mode
// are both "stop asking" states and must not stack into a confusing pair.
func execSkipPermsCore(sub string, cur bool) (next bool, msg string, changed bool) {
	switch strings.TrimSpace(sub) {
	case "on":
		return true, "dangerously_skip_permissions ON — tool calls are auto-approved without asking", true
	case "off":
		return false, "dangerously_skip_permissions OFF — agents prompt before running tools", true
	case "":
		state := "off"
		if cur {
			state = "on"
		}
		return cur, "dangerously_skip_permissions is " + state + "  (use /skip-permissions on|off)", false
	}
	return cur, "usage: /skip-permissions [on|off]", false
}
