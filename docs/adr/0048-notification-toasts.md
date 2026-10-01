# 48. Notification Toasts: Displaced, Timestamped, Dismissable Events

Date: 2026-10-01

Status: Accepted

## Context

Issue #162: milk's own informational events were appended straight into the transcript — thinking-visibility switches (`/think on|off`, Ctrl+T), panel open/close confirmations (`/panel …`, F1-F4), ADR-0044's auto-open panels, background-job spawn/completion, config reloads, MCP OAuth completion, credential refreshes. In the transcript these events:

- carry **no timestamp** — you can't tell when a credential refresh happened an hour ago or just now;
- **can't be dismissed** — they sit in the conversation view forever (or scroll away silently, so an event you missed is gone);
- **pollute the conversation** — they aren't part of the dialogue, but they read as if they were, interleaved between prompt and response.

The issue drew a precise scope boundary: **tool calls stay in the transcript**, because their execution is bound to the turn that requested them. Only events *unrelated to the current turn and conversation* — mode/visibility state changes, panel opens, async job lifecycle — belong elsewhere. It also required two dismissal modes (interactive **and** time-based), persistent history with a dedicated command, per-event timestamps, and a slash-command hint shown next to each open toast ("the command you would use to act on it / see the rest").

Two shapes were considered and rejected:

- **A dedicated layout band** (rows carved out of the viewport for toasts). Rejected: every toast would shrink `viewportHeight`, reflow panel geometry, and force PTY pane and attach-view resizing — permanent layout cost for a transient overlay. (The same class of concern drove ADR-0047's rejection of a permanent split.)
- **Writing toasts into the viewport content**. Rejected: the viewport is rebuilt by `setViewportContent` on a throttled schedule during streaming (commit `e6e7c80`) — toasts would flicker, lag, or be silently overwritten by the next rebuild, and they'd become part of scrollback/selection/escalation context, exactly what the scope boundary forbids.

## Decision

A small toast subsystem in `cmd/milk/notify.go`, rendered as a **pure render-time overlay** — toasts are painted over the top-right of the already-rendered main-area block in `View()`, never part of any buffer.

### Event model

Every event passes through three stages (`toastEvent{at, text, hint, expiresAt}`):

1. **History ring** (`toastHistoryCap = 500`) — every event ever, timestamped; surfaced by `/notifications` (`HH:MM:05` timestamps, hint appended) and `/notifications clear`. Dismissal never erases history. A bare `/notifications` is exactly `/notifications list toastHistoryShow` (20) — older entries are reachable via `/notifications list <count>`, or `/notifications list` with no count for the entire history unfiltered; the shown count is reported as `(last N)` only when the view was actually truncated (`handleNotificationsCmd`/`listNotifications`, `TestNotificationsListCmd`). (The `(last N)` note itself was briefly computed from the history's full length *before* the slice that produced the shown entries, so a 68-entry history printing 20 lines claimed "(last 68)"; fixed, pinned by `TestHandleNotificationsCmd_LastNoteMatchesShownCount`.)
2. **Waiting queue** (`toastQueueCap = 50`) — FIFO; overflow drops the oldest *waiting* toast (it's in history either way).
3. **Visible set** (`maxVisibleToastsFor(chatHeight)`, floored at `minVisibleToasts = 3`, growing one slot per `toastVisibleRowDivisor = 10` rows of chat height; TTL `toastTTL = 6s`) — promotion stamps a **fresh** TTL, so a queued event never expires unseen: its lifetime starts when it becomes visible, not when it was enqueued.

Two dismissal paths, as required:

- **Time-based**: an expiry tick (`armToastTick`) fires at the earliest deadline and promotes the next queued toast.
- **Interactive**: **Ctrl+G** dismisses everything open (global binding, works in busy/prompt/attached modes and in PTY shell mode), keeping history.

### Generation-guarded tick (the #168 lesson, applied proactively)

`toastTickMsg` carries a `gen` that must match `model.toastGen`; `armToastTick` bumps the generation, and dismissal bumps it again. A tick scheduled before a dismissal or re-arm is therefore ignored rather than clearing the armed flag of a newer in-flight tick — the exact class of bug issue #168 caught in `dragResetMsg`, where a generation existed but was never checked and a stale timeout froze selection mid-drag. `TestStaleToastTick_Ignored` pins this.

Because emitters (`handleThinkCmd`, `autoOpenPanel`, `refreshMCPToolSets`…) are value-receiver helpers that predate toasts, `notify()` deliberately returns nothing: `Update` wraps `updateInner` and arms the expiry tick after every message, so emitters stay fire-and-forget as long as the caller returns the mutated model.

### Rendering

`toastContent` builds one toast's dim `HH:MM` timestamp · message · blue slash-command hint (event-specific, e.g. `/think off`, `/panel background`, `/mcp reconnect <server>`; fallback `/notifications`), truncating the message so the whole thing fits the available width; the plain-text budget is computed first so the styled result fits without wrapping. The `ctrl+g dismiss` affordance gets its own footer row, right after the last visible toast, rather than being folded into that toast's own message row — it applies to every open toast, not just one message. `toastDismissLine` builds that row: `"N more pending · ctrl+g dismiss"` (dot separated) when `toastQueue` is non-empty — events waiting beyond what's currently visible — or just `"ctrl+g dismiss"` otherwise; it degrades by dropping the pending-count prefix before ever touching the affordance itself.

`overlayToasts` then positions every row — visible-toast messages and the dismiss footer alike — at one shared start column (`sharedCol = max(boxWidth/2, boxWidth-(maxLen+2*toastContentPadding))`, where `maxLen` is the longest of them) so the block reads as a consistent ragged-right list instead of each row individually right-aligning to its own, different, start column (`TestOverlayToasts_RowsShareStartColumn`, `TestOverlayToasts_DismissGetsOwnRow`, `TestOverlayToasts_PendingCountOnDismissRow`). `toastContentPadding = 1` reserves one blank column before and after the content within the band itself — `padToastRow` is called with `leftPad = toastContentPadding` (not 0), and the content's own measurement/truncation budget is `boxWidth - 2*toastContentPadding` throughout, so content never touches either edge of its own painted band (`TestOverlayToasts_ContentHasPaddingColumns`). When the halfway clamp leaves less room than a message row's natural length, that row is re-budgeted (not just truncated) against the smaller width (still minus the reserved padding), so the message truncates further rather than the already-built string getting blindly chopped.

The painted band itself only spans `avail = boxWidth - sharedCol` columns — from the shared column to the right edge — rather than the full box width: everything left of the shared column, including what would otherwise be blank background-tinted filler, is left as the original underlying content, untouched (`TestOverlayToasts_BandStartsAtSharedColumn`). This is the overlay covering the least area needed rather than always painting the whole box regardless of message length. `padToastRow` fills the band with the background tint via `withPanelBackground` (re-applied after every embedded reset, per ADR-0044) so dim/blue spans don't punch holes in it. `overlayToasts` paints each toast over the top-right of the main-area block — viewport, attach view, or PTY screen — **consuming zero layout rows**: `viewportHeight`, panel geometry, PTY sizing, and attach height are untouched, pinned by `TestView_ToastOverlayFloating` (same line count with and without toasts). The band's right edge is further inset by `toastSideMargin = 2` columns from the confined span's own right edge, so it reads as a floating card rather than a strip flush against the transcript's own edge (`TestOverlayToasts_PreservesSideMargins`).

### Scope boundary (what moves, what stays)

| Moves to toast | Stays in transcript |
|---|---|
| `/think on|off` state changes, Ctrl+T toggle (bare `/think` *query* stays — it's an explicit answer) | tool calls and their results (turn-bound) |
| `/panel …` on/off confirmations, F1-F4 equivalents | turn output, streaming content |
| ADR-0044 auto-open panels — **closed→open transition only** (repeated workflow-chunk calls stay silent), respecting `panelManualOverride` | warnings and errors (e.g. OAuth failure still prints a persistent transcript line) |
| background agent spawned / completed / failed | explicit query answers (`/notifications`, `/history`, `/export`…) |
| config reloaded, MCP servers reconnected, MCP OAuth completed (hint `/mcp reconnect <server>`) | workflow start/complete/error breadcrumbs (ADR-0047) |
| credentials refreshed (Bedrock/token — previously only a status-bar flicker) | |

New emitters must make the same call: *informational and turn-unrelated → toast; bound to a turn, or a warning/error the user must not miss → transcript*.

## Consequences

- The transcript contains only conversation and turn-bound material again; events are displaced but never lost (history ring + `/notifications`).
- Emitters are one `m.notify(text, hint)` call; registration is mechanical — and easy to forget: the `/clear` incident (#164) showed that a new command must be added to extraction, completion variants, dispatch, help, and busy-safe lists. `TestNotificationsRegistration` pins all of it for `/notifications`.
- Tests: `notify_test.go` (queue/TTL/dismiss/tick-generation/render/history) plus migrated emitter assertions in `background_ui_test.go` that now also assert the transcript does **not** receive the migrated strings — future re-pollution fails loudly.
- Floating toasts overlay the top-right of the *transcript* (viewport+separator span, `mainWidth()`); transcript content in that corner is visually covered while a toast is open (dismissible, 6s TTL) — accepted trade-off versus consuming layout rows. Side panels (memory/tasks/background/workflow), when open, sit to the right of that span and are reattached untouched on every toast row, so an open panel's title (row 0) is never covered.
