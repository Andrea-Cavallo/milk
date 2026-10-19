# 63. Background shells: lifecycle, permissions, cleanup

Date: 2026-10-19

Status: Accepted

## Context

`docs/internal/analysis/local-agent-toolset-review.md` (G3): milk's `bash` tool was blocking-only — no `workdir`, no background mode. A dev server, file watcher or REPL blocks until the 600 s cap and occupies the whole turn. Claude Code has `run_in_background` with output/kill tools; MiMo-Code has an interactive PTY and `workdir`. The review asked for an ADR before exposing background shells, specifically for the lifecycle, permission and cleanup story ("background bash needs a cleanup story (exit, session switch, crash) before it is exposed").

## Decision

Three tools, all model-facing:

- **`bash(command, workdir?, run_in_background?)`** — `workdir` overrides the session cwd for one command; `run_in_background: true` starts the command via `sh -c` in its own process group and returns a `shell_id` immediately (ADR-0063 shape in `internal/agent/local/bashbg.go`).
- **`bash_output(shell_id, tail_lines?)`** — status line (`running`/`exited (code N)`/`killed`) plus the retained output, optionally just the tail.
- **`kill_bash(shell_id)`** — stops the shell's process group.

Lifecycle (`shellRegistry`, package-global):

- **Outlives the turn, never outlives milk.** Shells run under the registry's own context like background agent jobs (ADR-0043) — a turn ending does not stop them. `local.StopBackgroundShells()` is called from the milk process exit path (`cmd/milk/main.go`'s deferred cleanup), so no shell survives the session. Shells are not persisted or re-attached across restarts: a milk restart orphans nothing (exit kill) and recovers nothing (no stale IDs).
- **Bounded.** At most 16 concurrent shells (a new start beyond the cap fails with a hint to `kill_bash`); every shell is auto-killed after 1 hour (`bgShellMaxLifetime`) so a forgotten dev server cannot live forever; output accumulates in a byte-capped `internal/livebuf.Buffer` (256 KB, oldest dropped) so a chatty process cannot exhaust memory.
- **Reaped after exit.** A finished shell's output stays readable via `bash_output` for 30 minutes (`bgShellRetainAfterExit`), then the registry drops it (also opportunistically when the registry fills up). `bash_output` on an unknown/reaped ID errors with the ID semantics spelled out.

Permissions: `run_in_background` rides the existing `bash` permission gate (the ask summarizes the command as usual) — starting a background shell is exactly as side-effecting as running one. `bash_output` is read-only and ungated (it is in the parallel-safe read run of ADR-0062); `kill_bash` is ungated but a barrier call: it can only touch shells this agent started, never arbitrary processes. `timeout_seconds` does not apply to background shells (the lifetime cap and `kill_bash` bound them); the schema says so.

## Consequences

- TUI/ACP panel wiring for background shells (live buffer attach, `/bg` integration — the review's "wired into the existing background-job panel and ADR-0047 live buffer") is **not** in this change: shells are model-owned state with no human command surface yet. Follow-up issue if wanted.
- A crash of milk (not a clean exit) skips the exit kill; the 1-hour lifetime cap is the backstop for orphans.
- The registry is process-global, so a shell started by a background job's fork is visible to the parent agent's `bash_output` — deliberate: shells are workspace-scoped (session cwd), and IDs are namespaced (`s1`, `s2`, …) per process.

Implementation: [`internal/agent/local/bashbg.go`](../../internal/agent/local/bashbg.go), `runBash`/`runBashOutput`/`runKillBash` in [`internal/agent/local/tools.go`](../../internal/agent/local/tools.go).
