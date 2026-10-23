# Bubble Tea v2 UI modernization plan

Branch: `claude/bubbletea-ui-modernization-kyrnje`

## Starting point

- `bubbletea v1.3.10`, `lipgloss v1.1.1`, `bubbles v1.0.0`.
- Used today: `textarea`, `viewport`, `tea.Tick`, `tea.Batch`, `tea.ExecProcess`, mouse, bracketed paste, hand-written OSC 52 (`transcript.go`), `lipgloss.HasDarkBackground()` (`theme.go`).
- Migration surface: `repl.go` (about 5000 lines), 96 non-test files in `cmd/milk`, `tea.KeyMsg` and `tea.MouseMsg` in about 20 files.

## Decision: what we use and what we skip

### Use

| Feature | Why | Phase |
|---|---|---|
| Bubble Tea v2, declarative `View` | Base for everything else. Replaces `WithAltScreen`, `EnterAltScreen`, `EnableBracketedPaste`. | 1 |
| New cell renderer + synchronized output (mode 2026) | Less flicker while streaming with side panels open. | 1 |
| `BackgroundColorMsg` + `lipgloss.LightDark` | Replaces the blocking `HasDarkBackground()`. Skins pick light or dark correctly. | 2 |
| `KeyPressMsg` + keyboard enhancements | Real `Ctrl+Enter` / `Shift+Enter`. Retires `zprobe_ctrlenter_test.go` workarounds. | 1 |
| `PasteMsg` | Native paste, simpler paste-taint handling. | 1 |
| `FocusMsg` / `BlurMsg` | Pause tips rotation and 3s git/project ticks when the window is in the background. | 3 |
| `SetClipboard` | Replaces the manual OSC 52 code. | 3 |
| `SetWindowTitle` | Tab title with agent and state. | 3 |
| Lip Gloss layers (`Layer`, `Canvas`) | Toasts, permission asks and a command palette as real overlays. | 4 |
| `key.Binding` + `bubbles/help` | One key registry. Help bar adapts to context. | 5 |
| `bubbles/progress` | Context usage bar, background jobs, workflows. | 6 |
| `bubbles/table` | `/usage` with aligned columns (tok/s, TTFT). | 6 |
| `harmonica` | Spring animation for panel open/close only. Small and subtle. | 6 |
| `teatest` | Golden-file TUI tests, replacing fragile probe tests over time. | all |

### Skip (for now)

| Feature | Reason |
|---|---|
| `huh` | The `/config init` and `/setup` wizards share a host-neutral core with ACP (`initwizard_core.go`). A TUI-only form library would fork that logic. |
| `bubbles/list` | Slash completion and session lists are shared with ACP and the CLI. Our own renderer stays. |
| `bubbles/filepicker` | The project panel already runs on `fileindex.go`. |
| Gradients | Cosmetic. Revisit after the skin settles. |
| `charmbracelet/log` | `slog` + OTel already cover logging. |
| Terminal progress bar (OSC 9;4) | Nice to have. Only if Windows Terminal testing shows value. |

### Spike, decide later

- `ultraviolet` for app-managed text selection (open backlog item: selection highlight sticks to screen coordinates while scrolling). Spike after phase 4. Adopt only if it removes the current workaround.

## Phases

Each phase builds, passes `go vet`, `go test ./cmd/milk/...` and ships on its own.

1. **v2 core migration.** Switch imports to `charm.land/*/v2`. `View()` returns `tea.View`. `KeyMsg` to `KeyPressMsg`, `MouseMsg` split by action, `PasteMsg`. Move alt screen and mouse mode into the view. Update `go.mod`, keep tests green. Files: `repl.go`, `layout.go`, `attach.go`, `commands.go`, `workflow_cmd.go`, `bg_agent_wizard.go`, `direct_bash.go`, panels.
2. **Theme.** `BackgroundColorMsg` plus `LightDark` tokens in `theme.go`. Remove the `HasDarkBackground()` calls.
3. **Terminal integration.** Focus/blur gating of ticks, `SetClipboard`, `SetWindowTitle`.
4. **Overlays.** Toasts and permission prompt on layers. Optional command palette behind a shortcut.
5. **Key registry.** Single `KeyMap`, `help` bar, derive `tips.go` shortcut tips from it.
6. **Widgets.** `progress` for ctx and jobs, `table` for `/usage`, `harmonica` for panel transitions.
7. **Selection spike** (`ultraviolet`).

## Compatibility rules

- No change to slash command names, config keys or session files.
- ACP and CLI hosts are untouched: only `cmd/milk` TUI files change.
- Update `CLAUDE.md` tech stack and ADRs where behavior changes (renderer, theme detection, toasts).
- Manual check per phase on Windows Terminal (PowerShell), plus one Linux terminal.
