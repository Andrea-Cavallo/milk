# 64. Background shells in the TUI and ACP: one merged background surface

Date: 2026-10-19

Status: Accepted

## Context

ADR-0063 shipped background shells (model-side lifecycle) but deferred the human surface: "TUI/ACP panel wiring for background shells (live buffer attach, `/bg` integration) is **not** in this change: shells are model-owned state with no human command surface yet." Without it, a dev server started via `bash run_in_background` is invisible to the human: no row to look at, no way to watch its output except asking the model to `bash_output` it, and no way to stop it except asking the model to `kill_bash`. Background *jobs* (ADR-0043) already have all of that: the F3 background panel with click-to-attach live views (ADR-0047), `/bg list|show|stop`, and ACP tool-call rows with streamed live output.

The question was how to surface shells without inventing a parallel UI. The user's constraint: merge into the existing background panel rather than adding a new one.

## Decision

**Shells are a second row class in the one existing background surface — same panel, same attach machinery, same `/bg`, same ACP row shape.** No new panel, command family, or protocol vocabulary.

- **One merged panel (`F3` / `/panel background`, now titled "background").** `backgroundPanelLayout(jobs, shells)` renders an `agents (n)` section and a `shells (n)` section (each shown only when non-empty; `(none)` when both are). The layout is the single source for both rendering and click mapping (`bgPanelRow` carries the row's click target), recomputed on every click — so a shell or job that started or finished between render and click cannot shift the line→target mapping (the same rule ADR-0047 set for job rows). Shell rows: `▶` running, `■` stopping (killed, not yet exited), `✓` clean exit, `✗` non-zero exit/error/killed, plus the command as label and an elapsed badge — the job-row grammar.
- **Attach reuses ADR-0047 verbatim.** A shell row double-clicks to the same live-attach view (new `attachShell` kind) over the shell's `livebuf.Buffer` (exactly what `bash_output` tails). Esc detaches; the buffer keeps accumulating.
- **`/bg` unifies IDs.** `/bg` and `/bg list` show both tables; `/bg show <id>` works for job and shell IDs (shell: status line + current output, capped like a job result); `/bg stop <id>` cancels a job or kills a shell (`/bg stop` is the UI's `kill_bash`). No new verbs.
- **ACP mirrors the job rows.** Each shell gets one synthetic tool-call row (`shell:<id>`, name `background_shell`) via `acp.BackgroundShellCall`: `in_progress` while running, `completed`/`failed`/`cancelled` (killed) at exit, live output streamed into the row via the existing `streamLive`/`ToolCallContentPoll` machinery, output flushed before the terminal status row. `/bg` works identically over ACP.
- **Lifecycle events fan out.** The shell registry is process-global (the tool contract — every agent sees every shell), unlike the per-session job Manager. `local.AddShellListener(onStart, onFinish)` therefore supports *multiple* listeners (one per TUI / ACP session, unregistered on session close), fires synchronously in start→finish order, and is the only new seam the UI layers need.

Consequences chosen deliberately:

- **No transcript block for a finished shell** (unlike a finished job's result block). A job's *result* is turn output the model produced for the user; a shell's output is a log the model reads on demand. Surfacing it in the transcript would be noise — the panel row + attach view + `/bg show` are the read paths. Lifecycle is toast-only ("background shell s1 exited 0").
- **Shells keep the background panel "live"** (`panelContentFinalized` is false while any shell runs) — a running dev server is active background work, and the ADR-0059 manual-override reset then behaves the same as for jobs.
- **Not persisted** (ADR-0063 unchanged): no stale rows after restart, no session association — shells are process/workspace-scoped, and the merged panel shows the process's shells regardless of which agent or session started them.

## Consequences

- The human can now see, watch, and stop model-started background shells with zero new vocabulary; editors over ACP see them as ordinary tool-call rows.
- The click-to-attach line mapping moved from "line 2+i is job i" to a shared layout struct — slightly more code, but the render/click drift hazard is gone structurally.
- A session that closes unregisters its shell listener; the TUI registers one for the process lifetime.

Implementation: [`internal/agent/local/bashbg.go`](../../internal/agent/local/bashbg.go) (`ShellInfo`, `BackgroundShells`, `ShellLive`, `BackgroundShell`, `KillBackgroundShell`, `StartBackgroundShell`, `AddShellListener`), [`cmd/milk/panel_background.go`](../../cmd/milk/panel_background.go), [`cmd/milk/attach.go`](../../cmd/milk/attach.go), [`cmd/milk/commands.go`](../../cmd/milk/commands.go) (`/bg`), [`cmd/milk/acp_features.go`](../../cmd/milk/acp_features.go) (`setupShells`), [`internal/transport/acp/map.go`](../../internal/transport/acp/map.go) (`BackgroundShellCall`).
