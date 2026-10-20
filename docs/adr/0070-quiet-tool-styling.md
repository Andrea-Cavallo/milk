# ADR-0070: Quiet tool styling in the Telegram view

- **Status:** Accepted
- **Date:** 2026-10-20
- **Related:** ADR-0026 (TUI dim tool hints — the semantic precedent), issue #290, issue #253 (result caps)

## Context

The TUI renders tool hints dim (ADR-0026): tool traffic is *machinery*, the
agent's prose is the conversation. Telegram has no dim attribute — the only
de-emphasis its message entities offer are **italic** (lighter weight),
**inline code** (monospace, reads smaller), spoilers (hidden until tapped)
and HTML blockquotes (grey bar, needs a different parse mode).

Today's tool lines are the *loudest* messages in the chat — bold name +
code block — while the `💬` response body is plain text. Tool traffic is
also the most frequent type, so a remote session reads as machinery.

Two latent bugs live in the same lines: `NotifyToolUse` ships **uncapped
and unchunked** (an oversized `cliToolArgSummary` preview makes the send
silently fail — Telegram's 4096-unit limit), and summaries containing
newlines produce multi-line MarkdownV2 code spans, which parse
unreliably.

## Decision

**Tier 1 — quiet single-line tool messages, staying MarkdownV2:**

| Event | Format |
|---|---|
| tool use | `⚙ _Bash_ · \`grep -rn foo\`` (bare `⚙ _Bash_` with no summary) |
| result (ok) | `📤 _Bash_ · \`2 matches\`` |
| result (error) | `❌ *Bash* error · \`exit 1…\`` — **stays bold**; failures are never quietly dimmed |

- The tool name is *italic* (escMD — `_` is active outside code); the
  summary is an inline code span escaped only per the MarkdownV2 rule for
  code (`\` and `` ` `` — full `escMD` would over-escape `-`, `.`, … with
  no purpose and no visible benefit inside code).
- **Flatten then cap**, rune-safe: newlines → ` ⏎ ` (single line), tool-use
  preview ≤ 200 runes, result ≤ 497 (the issue-#253 cap). Together with
  the unchunked-send path this guarantees every tool message stays far
  under the 4096-unit limit.
- Builders are pure (`toolUseMessage`, `toolResultMessage` — mirroring
  `turnStartMessage`) and table-tested; `NotifyToolUse`/`NotifyToolResult`
  only wrap them. The state flip for the busy status line is unaffected.

**Unchanged:** `🔄` turn notices, `💬` responses, `🔐`/`❓` asks, the
busy-status line — user-facing messages keep full weight. Empty result
summaries still send nothing (pre-existing rule).

## Rejected (for now, revisited on demand)

- **Tier 2 — spoiler wraps** (`||…||`) for bulky results: literal "dim",
  but hidden until tapped; annoys for one-liners. Threshold idea (~80
  runes) recorded in issue #290 if wanted later.
- **Tier 3 — HTML `<blockquote>`**: loudest "secondary content" cue, but
  needs a per-message parse mode through the outbound queue and
  re-derived chunk budgets. Real plumbing; only worth it if Tier 1 does
  not feel quiet enough.
- **A `remote_oversight.tool_style` knob**: the TUI dims tool hints
  unconditionally — same precedent; add a knob only if someone asks.

## Consequences

- The remote chat reads as conversation with tooling as quiet tags;
  errors stay visually loud. Existing chunking and status-line tests are
  untouched (no test pinned the old tool strings).
- Tool-event send failures from oversized previews are structurally
  impossible (cap asserted in tests).
