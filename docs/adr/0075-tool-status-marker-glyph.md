# ADR-0075: Tool & status marker glyph: ⚙ → »

- **Status:** Accepted
- **Date:** 2026-10-23
- **Amends:** ADR-0026 (hint-line glyph only — format unchanged), ADR-0070 (Telegram tool-line glyph)
- **Related:** user report — Windows `cmd` default font renders the marker oversized, overlapping the text after it

## Context

Every quiet machinery line used `⚙` (U+2699) as its marker: TUI tool hints
(`» …` in the making — `⚙ bash: go test …`), the `calling agent` notice,
workflow/background lifecycle notices, the status-bar badges
(`[⚙ 2 background agents running]`, `[⚙ workflow …]`), smolagent step and
observation lines, session export, and Telegram's tool-use lines.

Windows terminals with their default fonts don't carry that glyph: neither
the legacy raster fonts nor **Consolas** contain U+2699, so the console falls
back to Segoe UI Symbol/Emoji, which draws taller than the cell — the gear
bleeds into the text that follows (reported against `cmd`'s default font).
The glyph is decorative, so a font-safe replacement is a pure win.

## Decision

Replace every *rendered* `⚙` marker with **`»`** (U+00BB, right-pointing
double angle quotation mark):

- present in Latin-1/cp1252 **and** every OEM codepage, and in raster fonts,
  Consolas and Cascadia Mono alike — always a single cell, never a fallback;
- semantically reads as "runs into / executes": `» bash: go test ./…`;
- visually distinct from the prompt label `❯ ` (single heavy chevron, bright)
  even though both sit in the left gutter — reviewed and accepted; the dim
  weight of tool lines carries the role distinction;
- zero collisions: `»` had no existing usage in milk, unlike `·` (already
  milk's universal separator — status segments, help hints, Telegram's
  `·` between name and summary) which was explicitly rejected.

Swapped in: `internal/agent/local` (tool hints, `calling agent`),
`cmd/milk/repl.go` + `cmd/milk/status.go` (lifecycle notices, status badges),
`truncateToolSummary` width accounting, `internal/agent/smolagent`,
`internal/session/export`, and `internal/oversight/telegram`'s
`toolUseMessage`.

## Consequences

- ADR-0026's *format* (one dim, single-line `name: summary` hint) is unchanged
  — only the glyph is amended; both records carry a pointer to this ADR.
- Telegram's tool line becomes `» _Bash_ · \`grep …\`` (format table in
  ADR-0070 shows the historical glyph).
- Persisted history keeps showing old `⚙` lines exactly as written — nothing
  parses the marker back, so old transcripts are cosmetic-only stale.
- Fallback ladder if a font ever lacks `»` as well (practically impossible —
  it predates Unicode): plain `*`, the ASCII candidate from the review that
  also considered `►`, `•`, `·` and text-presentation `⚙︎`.
- Format-pinning tests (Telegram `toolstyle_test`, hint-width fixtures,
  rendering fixtures) updated to the new marker.
