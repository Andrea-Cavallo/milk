# 75. Bubble Tea v2 UI Modernization

Date: 2026-10-23

Status: Accepted

## Context

The TUI ran on Bubble Tea v1.3, Bubbles v1.0 and Lip Gloss v1.1. Three recurring problems traced back to that stack: Ctrl+Enter needed raw-CSI sniffing (`isCtrlEnterCSI`), the terminal background was a blocking query that ran at package init, and every overlay (toasts) was hand-written escape surgery on rendered rows. The v2 line fixes these at the source. Plan and per-phase notes: `docs/internal/plans/bubbletea-v2-ui-modernization-plan.md`.

## Decision

Move to `charm.land/bubbletea/v2`, `charm.land/bubbles/v2` and `charm.land/lipgloss/v2`, in seven phases, each shippable on its own:

1. **Core.** `View()` returns `tea.View` and declares the alt screen. Keys are `tea.KeyPressMsg`, pastes are `tea.PasteMsg` (routed by `routePaste`), mouse messages are flattened once into `mouseEvent` (`mouse.go`) so the selection and panel handlers stay unchanged.
2. **Theme.** `Init` sends `tea.RequestBackgroundColor`; `BackgroundColorMsg` sets `darkBackground` and `refreshThemeColors()` rebuilds the chrome styles, which are now built only by `rebuildStyles()`.
3. **Terminal integration.** The view declares focus reporting and a window title (`milk · <agent> · working|needs input`). While blurred, tips rotation and project/git polling pause. Copy sets the terminal clipboard through `tea.SetClipboard`.
4. **Overlays.** `overlay.go` composes layers with the Lip Gloss compositor; toasts use it and the command palette (`palette.go`, Ctrl+O) floats over the main area.
5. **Key registry.** `keymap.go` holds the global shortcuts as `key.Binding`; Update matches through it and `/keys` renders it with `bubbles/help`.
6. **Widgets.** The `ctx:` status fragment gets a `bubbles/progress` meter on wide terminals.
7. **Selection spike.** Closed without adoption, see below.

### Deliberately not done

- **Permission ask as a modal layer.** It is shared with ACP and Telegram (ADR-0052) and its y/n/a/d flow, queue and timers are covered by many tests. A modal copy would duplicate the prompt for no gain.
- **`bubbles/table` for `/usage`.** `obs.FormatTokenUsage` already prints an aligned table and is shared with plain-text hosts.
- **`harmonica` panel animation.** Animating panel width re-wraps the transcript and re-syncs the layout every frame. `harmonica` stays only as the transitive dependency of `bubbles/progress`.
- **`huh`, `bubbles/list`, `filepicker`.** Their jobs have host-neutral cores shared with ACP (`initwizard_core.go`, completion, `fileindex.go`).
- **`ultraviolet` for selection.** It has no selection primitive. The backlog item was stale: selection is already app-managed and anchored to content lines, not screen rows (ADR-0024), with drag autoscroll.
- **Declarative mouse mode.** Mouse tracking is still written as raw escapes because v2's declarative modes cannot express "1000 at rest, 1002 only while dragging" (see `enableMouseTracking`).

## Consequences

- Lip Gloss v2 always emits full color; downsampling moved to the output writer. Tests that forced a color profile no longer need to.
- `glamour` still depends on Lip Gloss v1 transitively; the two coexist.
- Ctrl+Enter arrives as `ctrl+enter` on terminals with extended key protocols; the CSI sniffing stays as a safety net.
