# Busy-input disposition: queue or fork — UI-facing design

> **Status: DESIGN PROPOSAL** (pre-ADR, pre-implementation). Companion doc:
> [busy-queue-or-fork-tech-design.md](busy-queue-or-fork-tech-design.md) (the
> tech design; state model, drain order, test plan live there).
>
> This doc answers: *what does the user see and press — how does mid-turn input
> visibly become either a queued prompt for the live agent or a background
> agent request, and how is the queue kept legible and reversible?*

## 1. Mental model

**"Say it now, run it when the turn ends — or fork it aside."** While the
agent works, the input line stays fully usable. Two keys give two destinations:

- **Enter = hand it to *this* agent.** The prompt waits its turn and runs the
  moment the current one finishes, in the same conversation. For corrections,
  "also do X", next steps — anything that belongs to the thread you're in.
- **Ctrl+Enter = fork it out.** A background agent researches it in parallel
  (the existing ADR-0043 flow, `Ctrl+J` fallback unchanged). For self-contained
  side questions.

Nothing is lost and nothing fires by accident: a queued prompt is visible
(receipt line + status chip), reviewable (`/queue`), and removable
(`/queue drop`) for as long as the current turn runs.

## 2. Key bindings (while a turn is running)

| Key | Action |
|---|---|
| **Enter** | Queue the input as the live agent's next prompt (FIFO) |
| **Ctrl+Enter** / **Ctrl+J** | Spawn a background agent from the input (unchanged; the "which agent?" question appears when several are configured — `background agent [...] (blank = <default>):`) |
| Enter with an active completion/hint | Accepts the completion (unchanged, checked first) |
| Enter on an empty input | `agent is responding — Ctrl+C to interrupt` (unchanged) |
| **Shift+Enter / Alt+Enter / Ctrl+N** | New line in the input (unchanged) — queued prompts may be multi-line |
| `!cmd` + Enter | Runs immediately in the shell pane (unchanged) |
| Busy-safe slash + Enter | Runs immediately (unchanged); **`/queue` joins this set** |
| Other slash + Enter | `<cmd> unavailable while agent is responding` (unchanged — commands are never queued) |
| **Ctrl+C** | Interrupt the turn (queue survives) |

Nothing changes when idle: Enter submits immediately; a non-empty queue can
only exist while turns are running or draining.

## 3. Where the queue is visible

1. **Receipt line** (transcript, at queue time) — one line, no echo of the
   full text (the turn itself carries the text at flush — see §4):
   `[milk] ⋯ queued for next turn — "also check the tests" · /queue to review`
   Preview: first non-empty line, rune-safe hard cap at 60 display columns,
   `…` suffix; multi-line shows `⋯ 3 lines` instead of a preview; entries with
   attachments add `+ 2 attachments`. When the queue holds more than one item
   the receipt shows `(3)`.
2. **Status-bar chip** — the left cluster's `[…]` family (same slot as
   `[» N background agent(s) running]`): `[» 3 queued]` while busy, and at
   idle during the drain window if any remain. Dim, like the background
   agent chip.
3. **`/queue`** — the review surface (§6).
4. **Flush echo** — the queued prompt appears as a real user turn with the
   source label `[queued] ` (dim, exactly the `[telegram] ` precedent at
   `handleAgentDone`'s remote drain), e.g. `[queued] also check the tests` —
   so a turn never answers a prompt the transcript can't show. Caveat shared
   with `[telegram]`: the modern theme's `userEcho` renders a `YOU` header and
   drops the label, so there provenance is carried by the receipt line from
   queue time (acceptable — identical to the remote-input surface).

No toast (ADR-0048 scope rule: input-adjacent feedback is transcript + status
bar, not lifecycle noise). No background-panel rows — queued prompts belong to
the live agent, not to jobs. The textarea's `❯ ` prompt label is unchanged;
the chip is the queue's persistent indicator.

## 4. Copy deck (verbatim strings)

| Surface | String |
|---|---|
| Receipt (queue time) | `[milk] ⋯ queued for next turn — "<preview>" · /queue to review` (append ` (N)` when N > 1; `+ N attachments` when attachments were captured) |
| Flush label | `dim("[queued]") + " "` prefix on the user echo (mirrors `[telegram] `; dropped by the modern theme's `YOU` header like that one) |
| Status chip | `[» N queued]` (pluralize like `background agent`) |
| Queue full (busy hint) | `queue is full (20) — /queue to manage` |
| Old Enter-with-text hint | **removed** (Enter queues now). Fork discoverability moves to the idle tip + the receipt tail on first use |
| `/queue` empty | `[milk] queue is empty — prompts typed while a turn runs wait here` |
| `/queue` row | `1 ⋯ 3 lines · +1 attachment · queued 12s ago   also check the tests…` |
| `/queue show <n>` | full text, uncapped (the `/bg show` convention) |
| `/queue drop` usage | `[milk] usage: /queue drop <n|last|all>` |
| `/queue drop` echo | `[milk] dropped queued prompt 2` / `[milk] cleared 3 queued prompt(s)` |
| Session switch with queue | `[milk] dropped 2 queued prompt(s) — they were typed into the previous conversation` |
| Empty-input busy hint | `agent is responding — Ctrl+C to interrupt` (unchanged) |

## 5. Walkthroughs

**A. Queue a follow-up mid-turn** (turn running: "refactor the parser…"):

```
❯ add a test for the quoting edge case          ← typed while busy, Enter
[milk] ⋯ queued for next turn — "add a test for the quoting edge case" · /queue to review
… agent keeps working …                         [» 1 queued] in the status bar
[milk] ✓ parser refactor done                   (turn ends)
[queued] add a test for the quoting edge case   (flushes as the next turn)
```

**B. Queue and fork during the same turn**:

```
❯ also rename foo.go                            ← Enter → queued
[milk] ⋯ queued for next turn — "also rename foo.go" · /queue to review
❯ what does this CVE affect?                    ← Ctrl+Enter → background agent
background agent: job_3 spawned (what does this CVE affect?)   [» 1 queued] [» 1 background agent running]
```

The fork's result arrives via the existing delivery (toast + transcript +
follow-up turn); the queued prompt runs at the next turn end regardless.

**C. Change of mind while the turn runs**:

```
❯ actually skip the tests                      ← Enter → queued
[milk] ⋯ queued for next turn — "actually skip the tests" · /queue to review
/queue                                          → 1 ⋯ 1 line · queued 8s ago   actually skip the tests
/queue drop last                                → [milk] dropped queued prompt 1
```

## 6. `/queue` surface

`/queue` is busy-safe (it only reads/mutates the queue) and tab-completes like
every registry command. Listing is one line per entry, oldest first, with a
stable 1-based index (the index printed at queue time is the *position*; drop
uses the current listing's index). `/queue show <n>` prints the full text of
one entry under its receipt; `/queue drop <n|last|all>` and `/queue clear`
are the mutations. Help line:

```
  /queue                 list prompts queued for the live agent (typed while a turn runs)
  /queue show <n>        print one queued prompt in full
  /queue drop <n|last|all>  drop one or all queued prompts (/queue clear = drop all)
```

## 7. Interaction rules (UI-relevant)

- **Completion first.** Enter-as-accept keeps priority over queueing
  (unchanged) — mid-turn tab completion must not be hijacked.
- **Multi-line prompts** keep their newlines through queue and flush; the
  receipt shows `⋯ N lines` when the preview would mislead.
- **Permission prompts** (`Allow? [Y/n/a/d]`, questions) own the input while
  pending — Enter answers, never queues; queueing resumes afterwards.
- **Interrupts**: Ctrl+C kills the turn, not the queue; the chip stays until
  the queue drains or is dropped. After an interrupted turn the queued prompt
  still runs (explicit user intent; visible and removable beforehand).
- **Accidental queueing is cheap**: the receipt is a confirmation, and until
  the current turn ends `/queue drop last` is a full undo. (By contrast the
  old double-Enter fork trigger was retired in ADR-0043 precisely because
  forks are *not* undoable — the reversible action is the one on the default
  key.)
- **Paste provenance**: a pasted block that begins with `/learn …` or `!…`
  queues as inert text (issue #151 rule preserved end-to-end).
- **Attachments**: images/files staged in the input are captured with the
  queue entry (receipt shows `+ N attachments`) and land on exactly that
  prompt at flush.

## 8. Terminal & accessibility constraints

- The receipt and chip are dim-styled but their meaning is carried by words
  (`queued`, `dropped`), never by colour or glyphs alone (`⋯`, `»` are
  decorative, the `» N background agent(s) running` precedent).
- No new modifier chords beyond Ctrl+Enter/Ctrl+J (already covered by
  `zprobe_ctrlenter_test.go`'s CSI encodings and the universal Ctrl+J
  fallback). Queueing needs **no** extended key protocol at all — plain Enter
  works in every terminal, which is half the point.
- All queue management is keyboard-reachable (`/queue`); no mouse dependency.

## 9. Empty, capped and error states

| State | UI |
|---|---|
| Queue empty | `/queue` prints the empty line (§4); no chip; nothing else changes |
| Queue at cap (20) | busy hint `queue is full (20) — /queue to manage`; input text stays in the textarea (nothing is lost) |
| Drop out of range | `[milk] no queued prompt N — /queue lists them` |
| Session switch drops queue | notice with the count (§4) |
| `queue_while_busy: false` | Enter-with-text shows the legacy hint `agent is working — Ctrl+Enter to spawn a background agent with this`; Ctrl+Enter unchanged |

## 10. Docs surfaces touched on implementation

- `docs/guides/operations.md` — *Keyboard shortcuts* table: the Enter row
  gains "…or queues it for the live agent while a turn runs"; the Ctrl+Enter
  row cross-links the queue; a short **Prompt queue** subsection next to
  *Background sub-agents* with the `/queue` table.
- `docs/guides/workflows.md` — the `!` bang note's parenthetical about the
  busy flow ("not the Ctrl+Enter to spawn a background agent flow") gains
  "or the Enter-to-queue flow".
- `tips.go` curated tip: `{/bg start <task>} runs a background agent — {Ctrl+Enter} while a turn runs starts one too` → add `{Enter} while a turn runs queues it for this agent`.
- `docs/reference/spec.md` — `queue_while_busy` config key.
- ADR-0043 *Update* note + ADR-0082 at implementation.
