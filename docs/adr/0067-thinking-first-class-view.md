# ADR-0067: Thinking as a first-class view — out-of-band think blocks with fold containers

*Date: 2026-10-19*
*Issue: #208*

## Context

Reasoning used to be a mixin, not a view. `appendThinkingStreamed` spliced
dim-styled text straight into the transcript buffer and opened a raw `ESC[2m`
that `appendTranscript` had to close later; the TUI carried a second full
buffer (`transcriptNoThink`) in lockstep, pre-rendered with
`[thinking… Ctrl+T to show]` placeholders, and `activeTranscript()` flipped
between the two on `showThinking`. Consequences (issue #208):

- The only boundary between "what the model thought" and "what the model
  said" was dim styling — easy to lose at line wraps and on some terminal
  themes. The open-escape/close-escape bookkeeping was fragile for exactly
  this reason (self-terminated per-chunk pairs could orphan an `m`
  terminator at a wrap boundary).
- Long reasoning flooded the transcript: no collapse, no per-block fold, no
  independent scroll target.
- Scrolling/selection treated thinking and output as one undifferentiated
  blob, because they *were* one string.

Everything downstream of the transcript — the per-turn colorize cache keyed
on byte offsets (`wrappedTranscript`), `danglingEscapeStart` clipping,
selection coordinates over wrapped lines, `retractStreamed`'s verbatim
`LastIndex` surgery — operates on one string. That ruled out a rewrite of the
render stack into fully-typed content parts.

## Decision

**One buffer, plus out-of-band blocks** (`cmd/milk/thinkblocks.go`):

- A `thinkBlock` holds its reasoning text in its *own* buffer and an
  `anchor` (byte offset in the transcript it renders after). Reasoning never
  enters the transcript buffer, so the render stack keeps working on one
  plain string and the ANSI escape bookkeeping is gone entirely — dim
  styling is applied per line at render time by `colorize()`/`dim()`, which
  already closes the reset before every newline.
- `transcriptDisplay()` splices each block in at its anchor as a **fold
  container**: a one-line header (`▸ thinking · N lines` folded,
  `▾ thinking · N lines` expanded, `· streaming` on a block opened mid-stream)
  plus the body only when expanded (on the thinking surface — see the third
  Update below). Everything downstream (colorize + wrap +
  selection) runs on the display string exactly like it ran on the old
  `activeTranscript()` variant. `transcriptNoThink` and `activeTranscript()`
  are gone; `thinkingActiveInTurn` is replaced by "an open block exists".

Display rules (as amended by the 2026-10-19 Update below):

- Every block always renders its header row — there is no hidden mode and no
  placeholder line. The header is information-only: glyph, line count, fold
  state (`· streaming` on a block the user opened mid-stream), never a key
  hint.
- Blocks are **folded by default — streaming or complete** (the issue's
  requirement: long reasoning must not flood the transcript). There is no
  auto-open: while reasoning streams in the folded header's line count
  updates live ("▸ thinking · N lines", the same line a completed block
  shows), and only an explicit open (header click / Ctrl+T) shows the body.
  A block the user never touched stays folded when it closes.
- **Click a fold header** to expand/collapse that block (mouse release
  without drag, hit-tested against header lines in the selection coordinate
  space — see ADR-0080 for the mapping invariants) — the same arm/act rhythm
  as panel clicks; drag selection over reasoning text is untouched. **Ctrl+T**
  toggles the most recent block (the streaming one, if any) — the keyboard
  form of clicking its header. `/think expand` / `/think collapse` pin all
  blocks; `userSet` pins survive later auto-folds.
- `/think view` swaps the main area to a **thinking-only stream** (issue
  #208's "dedicated thinking view"): every block's header plus full body in
  order, over the static preview attach view (ADR-0047).

Consumption/display separation is preserved: loop detection
(`FeedReasoningChunk`), the session turn record (`currentTurnThinking`) and
the ACP `agent_thought_chunk` stream all consume reasoning from their own
accumulators regardless of what the renderer shows. The ACP `think` option
(config `show_reasoning`) is unchanged and remains the data-plane relay
toggle for `agent_thought_chunk` updates; `/think expand|collapse|view` are
TUI-only, and TUI fold state is independent of the relay toggle.

Two by-products of the model:

- `retractStreamed` surgery now touches only regular text; block anchors
  shift with the replacement (`adjustThinkAnchors`). Thinking interleaved
  into the stream can no longer prevent the retracted span from being found
  verbatim.
- `reasoningPromotedMsg` (local agent promotes a reasoning-only completion to
  the turn's answer, `local.go` emptyFallback) now **drops** the turn's
  blocks: the promoted text re-streams through the content path, and the old
  inline model rendered it twice (dim + plain). Session storage was already
  deduplicated; the view now is too.

## Alternatives considered

- **Status quo (two buffers)** — every append/retract/seed path must write
  both in lockstep and toggling is a whole-buffer flip; the issue's core
  complaint. Rejected.
- **Full typed content blocks (text/thinking/tool parts), per-part render
  components** — what opencode/MiMo-Code do. Cleanest end state, but milk's
  colorize cache, selection, retract surgery and history seed are all
  string-based over a monolithic buffer; the rewrite risk is exactly what the
  primary's design pass flagged. The span/anchor model gets the issue's
  actual wins (structure, folds, region rendering) without it. Tool turns
  stay inline (a follow-up candidate).
- **A separate side panel (like memory/tasks)** — rejected for the main
  surface: reasoning is turn-adjacent and reads best next to the turn it
  explains. `/think view` covers the "inspect all reasoning" case.

## Prior art

- **opencode / MiMo-Code** (`packages/tui`, `context/thinking.ts` +
  `routes/session/index.tsx`): transcript is typed parts
  (`text`/`tool`/`reasoning`, `PART_MAPPING`) each rendered by its own
  component. `ReasoningPart` is exactly the fold container adopted here:
  per-block `expanded` signal, collapsed by default in the default `hide`
  mode ("minimal thinking mode with click-to-expand"), a one-line header
  (`+ Thought: <title> · <duration>` / `- Thought: …`, colon labels — their
  fix #28562), dim/muted markdown body indented under it, click-to-toggle on
  the header, `reasoningSummary()` promoting a bolded first block to the
  header title. Adopted: fold container + click-to-expand + collapsed-by-
  default. Not adopted: `**title**`-summary extraction (milk models don't
  emit reasoning summaries) and duration in the header.
- **Claude Code** (proprietary): thinking display is a *mode of the stream*
  (`thinking_display` ∈ `summarized`/`omitted`/`highlights`…, `set_max_thinking_tokens`),
  not dim-inline text — reasoning is summarized or omitted by default and
  inspected deliberately. Confirms the issue's direction: `/think on` should
  mean "reasoning is inspectable", not "reasoning is dumped inline".

## Consequences

- Fold-state changes invalidate the offset-keyed colorize cache
  (`invalidateThinkDisplay`); the cached fast-suffix path still covers
  streaming growth (an open block is always the transcript's last content).
- History seeding still skips reasoning (`writeSeedTurn`) — seeds render no
  blocks, unchanged.
- Headers wrap at viewport widths below ~30 columns; clicks on wrapped
  continuation lines don't toggle (the header's first line is the hit
  target).
- Nice-to-haves not covered: per-block copy, search including/excluding
  reasoning, persisted expand state across sessions (state is per-session,
  per-block, in-memory).

## Update (2026-10-19): information-only headers, Ctrl+T toggles the last block, hidden mode and `/think on|off` removed

A review of the shipped fold model (user decision, same day) tightened the
TUI surface. The Decision above is amended in place; this records what
changed and why:

- **Headers are information-only.** `▸ thinking · N lines` is status — how
  much reasoning happened. The `· Ctrl+T` suffix was keybinding tutorial: it
  teaches once and then costs a line forever. Key hints live in `/help` and
  the idle tips line, which already advertise Ctrl+T. A header shows a
  count, not a tutorial.
- **Ctrl+T toggles the most recent block** (the streaming one if open, else
  the last completed block) instead of flipping global visibility. The
  global toggle was a sledgehammer over per-block state that already exists:
  it flipped everything retroactively, including blocks the user had already
  arranged. A local action is predictable — the block that just landed opens
  (or closes) — and useful in both directions (expand when folded, fold when
  the stream is flooding).
- **The TUI hidden mode is gone** (`showThinking=false`, the
  `[thinking… Ctrl+T to show]` placeholder). Its only justification over
  folded-headers-always was "not even one row" — too thin for a second axis:
  reasoning runs are ~1–3 header rows *per turn*, and the folded header
  already says the one thing the placeholder said (there was reasoning),
  with more precision (how many lines) and more access (click to expand).
  Every block always renders its header row.
- **`/think on|off` removed outright** — no deprecation aliases. Bare
  `/think` is a status line (block count + fold state); `/think
  expand|collapse|view` remain. The data plane is untouched: the ACP `think`
  session option and the `show_reasoning` config key keep their wire
  contract as the thought-relay toggle (`agent_thought_chunk` updates, ACP
  history replay), loop detection keeps consuming reasoning regardless, and
  TUI fold state is independent of all of it.

## Update (2026-10-19, later the same day): streaming blocks are folded by default too

User decision after live-testing the fold model: the streaming block must not
start fold-open. "I don't want 'current' reasoning to be fold-open at the
beginning and then folded. If not explicitly open, I want to see the single
line with updated row count as when collapsed." The Decision above is amended
in place again:

- **No auto-open, ever.** "Expanded by default" ceases to exist as a state:
  a new block is created folded (`expanded: false`), exactly like a completed
  one. While reasoning streams in, the folded `▸ thinking · N lines` header
  is the whole visual — and the line count *is* the live signal, ticking up
  as chunks arrive. The earlier "watch it live" rule inverted the default for
  everyone to serve a use case (watching raw reasoning stream) that is one
  keystroke away — and works better this way: Ctrl+T opens the streaming
  block and keeps it open.
- **User-open is pinned against close-on-stream-end.** A block opened while
  streaming (`userSet`) stays open when streaming ends; only blocks the user
  never touched remain folded. `closeOpenThinkBlock`'s auto-fold now only
  enforces the default for untouched blocks (which were folded all along).
- **The `· streaming` marker follows the open state.** A folded streaming
  block renders the same header line as a completed block (the updating
  count is the tell); `▾ thinking · N lines · streaming` appears only on a
  block the user opened while it is still receiving chunks.

Rationale: the transcript's default rhythm stays quiet — the header row
already reports that (and how much) reasoning happened. Live-watching is an
explicit per-block opt-in, and the folded header's updating count carries the
"something is happening" signal without dumping the body on screen.

## Update (2026-10-19, third pass): fold-status inheritance, turn-boundary tidy, the thinking surface, streaming-render fix

Live-testing feedback (same day) refined the fold model once more — and fixed
a render bug the model had introduced:

- **A new block keeps the folding status of the previous one.** Fold state is
  one session-wide bit (`model.thinkFoldOpen`), set only by explicit fold
  actions (header click, Ctrl+T, `/think expand|collapse`). New blocks are
  created in it — open-and-pinned while the user is watching, folded
  otherwise — so open one block and the rest arrive open, fold one and the
  next arrive folded. (Reading the status from the *previous block's row*
  would have been self-defeating in combination with the turn-boundary tidy
  below, which folds that row.)
- **A new turn collapses the previous turns' blocks** — while the session's
  fold status is folded (`dispatchAgent` runs `collapseThinkBlocksBefore`,
  which no-ops when `thinkFoldOpen` is set). `/think expand` means "expand
  all" and thereby defines the current default: a tidy that folded the very
  blocks it just expanded would contradict it, so in that mode the tidy does
  not run at all. Display-only either way: the tidy deliberately does not
  touch `thinkFoldOpen`.
- **The thinking surface.** Fold containers render on their own background
  (`ansiThinkBg`, 256-color dark grey) at every fold state — the thinking
  region reads as a different surface from the standard transcript. Header
  stays dim on the surface; the body is full-contrast (the background, not
  dimming, marks reasoning now). Applied at render time only: the transcript
  and block buffers stay plain. (Made a full-row surface in the fourth pass
  below — at this point the background still spanned only the glyphs.)
- **Streaming-render fix (garbling).** The containers introduced a real bug:
  `wrappedTranscript`'s fast path appended only the raw tail past its cached
  prefix, but an open block's container is re-rendered from scratch on every
  chunk — the header's live line count (digit rolls) and the body's per-line
  styling (a growing partial line moves its trailing reset) rewrite bytes
  *inside* the cached region, so the appended suffix started at shifted
  offsets: stray escape fragments and stale counts between the streaming
  line and the ones above it. Fix: `transcriptDisplayAt` reports where the
  open block's container begins and the cache commit is capped there — the
  stable prefix is committed, the streaming tail is re-wrapped fresh on
  every render (still the cheap path; arriving-line counting for the
  re-colorize threshold is tracked separately so the uncommitted tail does
  not re-count itself every render). `transcriptPlainLines` now renders
  through the same path, so click hit-testing covers the streaming block.

## Update (2026-10-19, fourth pass): the surface is a full-row one

Live-testing feedback: the surface read as "a different color as font
background" — the 256-color grey only painted behind the glyphs, because the
per-line styling wrapped just the line's text. Short fold headers painted a
~20-column strip; word-wrap continuation rows of long reasoning lines
painted nothing past their last word.

- **Every row of a container paints the full viewport width.** After wrapping
  (`tintThinkSurface`), every row that renders on the surface — the header,
  body lines, blank body lines, and word-wrap continuation rows alike — is
  padded to the viewport width with background-coloured spaces and
  re-wrapped in the surface code (`withPanelBackground`'s per-line
  convention, the same treatment the panel tint uses). The rows are final at
  that point, so the padding cannot re-wrap; the pad is render-time only and
  `transcriptPlainLines` strips and trims it back off, so hit-testing,
  selection and copy stay plain and the transcript/block buffers are
  untouched.
- **Padding is post-wrap on purpose** (vs padding the raw display): the raw
  display bytes stay exactly as the render cache and `transcriptDisplayAt`'s
  open-container boundary know them, so the streaming-commit cap and the
  width-change invalidation (`colorizeVPWidth`) are untouched by this change
  — the cache stores the untinted wrap and the width-dependent pad is applied
  to the returned render. Blank body lines now carry their surface code pair
  (`thinkSurfaceLines`) so they can't punch a hole in the surface, and rows
  are classified as surface rows by their SGR state (an open surface run
  carries across the newline into wrap continuation rows).
- **`colorizeFull` routes container rows around glamour.** glamour needs its
  input ANSI-stripped, which erased the surface entirely in `full` mode and
  reflowed the body; styled spans (surface rows, dim notes) are now preserved
  verbatim through `colorizeCodeBlocks`, and whitespace-only gaps between
  spans pass through instead of being collapsed (which glued adjacent rows
  together).
