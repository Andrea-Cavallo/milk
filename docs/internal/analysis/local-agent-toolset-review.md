# Local-agent toolset review

Date: 2026-10-19. Scope: the built-in tools of milk's own tool loop (`internal/agent/local`). Externally managed agents (`claude-cli`, aider, smolagents) bring their own tools and are out of scope.

References compared: Claude Code, opencode (`../opencode`, `packages/opencode/src/tool`), MiMo-Code (`../MiMo-Code`, an opencode fork, same directory). Claude Code is from the reviewer's own tool set, not from source.

Method: static read of `tools.go` and `local.go`, plus two read-only inventories of the reference repos. **Nothing in this report was live-tested.** Items marked "verify" need a reproduction before a fix is written.

Reading this cold:
- **Line numbers are a snapshot** (taken on `main` at 928e0ef, 2026-10-19) and will drift. Search for the named function or symbol instead of trusting `file:line`.
- **Reference repos** are sibling checkouts (`../opencode`, `../MiMo-Code`; tools in `packages/opencode/src/tool/`, registry in `registry.ts`). Their state may have moved since; re-check a claim there before relying on it. The file pointers cited (`edit.ts`, `gate.ts`) are the ones the inventory surfaced.
- **Claude Code** facts come from the reviewer's knowledge of its tool set, not from source; they are marked with a dagger in the comparison table.
- Project conventions for doing the work (feature branches, conventional commits, live-test before commit, `task run` for a fresh build, tests must isolate `HOME`/`MILK_CONFIG_ROOT`) live in `CLAUDE.md` and the maintainer's notes, not here.

## 0. Status

Update this table as work lands; the rest of the document is the original analysis and is not rewritten.

| Phase | Branch (planned) | Status | Notes |
|---|---|---|---|
| 1. Correctness fixes (B1-B5) | `fix/local-tools-correctness` | not started | B1/B2 need reproduction first (section 4.1) |
| 2. Fuzzy `edit_file` | `feat/edit-file-fuzzy` | not started | |
| 3. Search and shell | `feat/search-shell-tools` | not started | ADR needed for background shell |
| 4. Post-edit check, `web_fetch` | `feat/post-edit-check-web-fetch` | not started | |
| 5. Optional extras | per-item issues | not started | |

## 1. Verdict

The set is **not complete**. Milk has strong orchestration, memory and self-documentation tools that the references lack. Its core coding tools (edit, search, shell, web) are thinner than all three references, and there are a few probable correctness bugs in how a batch of tool calls is executed and how paths are resolved.

The gaps affect every primary agent, whatever its provider: milk is built around a cheap primary plus a deep escalation agent, and the primary can be a local server or any cloud backend. They bite hardest on small local models, which milk has been tested with (Qwen2.5-Coder 7B/3B, Gemma 4 E4B per `CLAUDE.md`) and has explicit tuning guidance for (`docs/guides/providers.md`: `system_prompt_tier: "minimal"`, `included_tools`). The expectation that small models fail mostly on edit-match drift, regex dialect and ordering mistakes is the reviewer's general experience, not something documented in milk; each of those is a tool-quality problem either way.

## 2. Current inventory

| Group | Tools |
|---|---|
| Files | `read_file`, `write_file`, `edit_file`, `delete_file`, `move_file`, `list_dir`, `open_file` |
| Search | `find_files`, `grep` |
| Shell / network | `bash`, `http_get`, `http_request` |
| Orchestration | `spawn_background_agent`, `cancel_background_agent`, `start_workflow`, `escalate`, agent-as-tool (`agent_<name>`), MCP tools |
| Memory / state | `record_memory`, `get_memory`, `list_memory`, `forget_memory`, `create_task`, `update_task`, `list_tasks`, `complete_task` |
| Session / self | `get_session_context`, `get_context_stats`, `export_session`, `current_need`, `milk_config_help`, `get_metrics`, `search_signals` |

Permission-gated: `bash`, `write_file`, `edit_file`, `delete_file`, `move_file`, `http_get`, `http_request` (`toolNeedsPermission`, `local.go:2463`).

## 3. Reference inventories (summary)

Claude Code column (†) is from memory of its tool set, not from source.

| Capability | Claude Code † | opencode | MiMo-Code | milk |
|---|---|---|---|---|
| Fuzzy edit matching | no (exact) | 9 replacers | 9 replacers + closest-match hint | **no** |
| Post-write formatter + LSP diagnostics | no | yes | yes | **no** |
| `multiedit` | no | no (dropped) | yes (not registered) | **no** |
| `apply_patch` | no | yes (GPT models) | yes (GPT models) | **no** (referenced in code, not defined) |
| Ripgrep grep / glob with caps | yes | yes (100 cap) | yes (mtime-sorted) | system `grep`/`find`, no caps |
| Background / interactive shell | `run_in_background` + output/kill | no | PTY `interactive`, `workdir` | **no** |
| Web fetch | yes | yes (md/text/html, 5 MB) | yes | raw `http_get`, 8 KB default |
| Web search | yes | Exa/Parallel | Exa / plugin | **no** |
| Ask-user question tool | yes | yes | yes | **no** (only permission asks) |
| Image / PDF read | yes | yes | yes + `view-image` | **no** |
| Notebook edit | yes | no | yes | no |
| LSP navigation tool | yes | experimental | experimental | no |
| Skills tool | yes | yes | yes + search | no |
| Cron / scheduling | yes | no | yes (experimental) | no |
| Sub-agents | Agent tool | `task` (resumable, background) | `actor` (spawn/run/wait/cancel/send) | `spawn_background_agent`, agent-as-tool |
| Task tracking | TodoWrite | `todowrite` | `task` (hierarchical) | `create_task` family |
| Batch concurrency | read-only parallel | per-tool ask | FIFO gate, only read/grep/glob parallel, fail-cascade | **everything concurrent** |
| Read-before-edit enforced in code | prompt-level | prompt-level | prompt-level | none |

Where milk leads: memory (percepts, consolidation), session-context tools, self-documentation (`milk_config_help`), native workflows, agent-as-tool, spill-to-disk of oversized tool output and tool-trail replay between turns.

## 4. Findings

### 4.1 Probable bugs (verify first)

**How to reproduce B1-B4** (each should be turned into a regression test once confirmed):
- **B1:** with a local agent and a scratch directory, make the model (or a mock inference server returning a scripted tool-call batch) emit in one response `write_file x.txt "a"` followed by `bash "cat x.txt"`; also two `edit_file` calls on the same file with different `old_string`s. Expect the `cat` to see the new content and both edits to land; the bug shows as an empty or missing file, or one edit lost. Scripted batches are easiest through `internal/agent/local`'s existing multi-call tests (`multicall_test.go`) rather than a live model.
- **B2:** start `milk serve --acp` from directory A, send `session/new` with cwd B, then run a prompt that calls `bash "pwd"` and `read_file` on a relative path that exists only in B. The bug shows as `pwd` printing A and the read failing. Also check what the ACP launcher (editor) sets as the process cwd.
- **B3:** call the `grep` tool with `pattern: "foo|bar"` on a file containing both words; the bug shows as no matches. Same for `a+`.
- **B4:** `read_file` a 10-line file with `offset: 5, limit: 1`; the output is numbered `6`, not `5` or `1`.

**B1. Side-effecting tools in one batch run concurrently.**
`executeToolCalls` (`local.go:2724`) starts one goroutine per call, including writes and bash. Only the permission prompts are ordered. Failure scenarios: `edit_file a.go` plus `bash "go build"` in one response builds before the edit lands; two `edit_file` calls on the same file race on read-modify-write and one edit is lost. Small models emit multi-call batches routinely (the stream detector explicitly supports one call per plan step). MiMo-Code's `gate.ts` runs only read/grep/glob in parallel and treats every other call as a barrier, cancelling the rest of the batch on failure.

**B2. Relative paths ignore the session cwd in most tools.**
`dispatchTool` passes `sess.CWD` only to `find_files` and `grep`. `read_file`, `write_file`, `edit_file`, `list_dir`, `delete_file`, `move_file` and `bash` use the process cwd (`bash` sets no `cmd.Dir`; there is no `os.Chdir` anywhere in `cmd/` or `internal/` except the file index). Harmless in the TUI where process cwd equals session cwd. Over ACP, `session/new` carries its own cwd, so relative paths and shell commands may land in the wrong directory. Verify how the ACP process is launched and whether session cwd can differ from process cwd.

**B3. `grep` uses POSIX basic regex.**
`runGrep` never passes `-E`, but the tool advertises "Regex pattern". Models write `foo|bar`, `a+`, `a?`; GNU basic grep treats those as literals, so the search silently returns nothing. The model then concludes the code isn't there.

**B4. `read_file` offset is 0-based but output is 1-based.**
The schema says "0-based" while the output is numbered from 1. A model that sees line 120 and requests `offset=120` skips it. opencode and MiMo use 1-indexed offsets.

**B5. Dead references to `glob` and `apply_patch`.**
`local.go` handles both names in several switches (lines 2033, 2068, 3178, 3188, 3204) and `summary_render.go:79`, but neither tool is defined. Harmless today; misleading, and a sign `apply_patch` was once planned.

### 4.2 Capability gaps

**G1. `edit_file` is exact-match only.** No fuzzy fallback (trimmed lines, indentation flexibility, escape normalization), no "closest match" hint on failure, no CRLF preservation, non-atomic write. This is the dominant failure mode for small models. Both opencode and MiMo ship a nine-step replacer chain; MiMo adds a fuzzy suggestion that is never applied silently.

**G2. No post-write feedback.** The model learns about compile errors only if it runs a build. Both references run the formatter and append diagnostics to the edit result.

**G3. `bash` is blocking-only.** No `workdir`, no background mode, no PTY. A dev server, watcher or REPL blocks until the 600 s cap. Claude Code has `run_in_background` with output/kill tools; MiMo has `interactive` and `workdir`.

**G4. `grep` / `find_files` are thin.** No case-insensitive flag, context lines, include/type filter, multiline, result cap or output mode. `find_files` matches basenames only (no `**`). Neither honors `.gitignore`; only `.git` and `.claude` are excluded, so `vendor/` and `node_modules/` flood results. Both depend on system `find`/`grep` (weak on Windows, where `tools_windows.go` exists).

**G5. Weak web access.** `http_get` returns raw bytes capped at 8 KB by default, with no HTML-to-markdown and no truncation hint. It duplicates `http_request`. There is no web search; an MCP server is the likely route.

**G6. No `multiedit` / `apply_patch`.** Multi-hunk changes cost one round trip each.

**G7. No structured `ask_user` tool.** The model can end its turn with a question, but cannot present options or block mid-turn the way Claude Code's `AskUserQuestion` and the `question` tools of opencode and MiMo do.

**G8. `read_file` cannot return images or PDFs.** `/attach` only covers user-supplied files, so a vision-capable model cannot view a screenshot it produced.

**G9 (low). Not present:** skills tool, `.ipynb` cell editing, cron/scheduling, LSP navigation, sub-agent status/wait/send (milk has spawn/cancel only).

**Not a gap:** read-before-edit enforcement. None of the references enforces it in code, only in the tool description.

## 5. Plan

Each phase is a separate branch (`fix/...` or `feat/...`), conventional commits, and stops for live testing before commit and merge. Each phase ends with unit tests, a build, and a live run in the TUI.

### Phase 1: correctness fixes (`fix/local-tools-correctness`)
- **B1:** in `executeToolCalls`, run consecutive read-only calls (`read_file`, `list_dir`, `grep`, `find_files`, memory reads, `get_*`) in parallel and treat every other call as a barrier executed in order. Decide whether a failed barrier call cancels the rest of the batch (MiMo does; start with cancel-on-failure for write/bash and record the choice in an ADR). Update the "Concurrent tool dispatch" section of `docs/guides/tooling.md`, which currently promises all-concurrent execution. Keep result order, per-tool timeouts and Ctrl-C propagation.
- **B2:** thread `sess.CWD` through every path-taking tool and set `cmd.Dir` in `runBash`; add a regression test with a cwd different from the process cwd. Also check the tool-agent and workflow paths, which call `os.Getwd`.
- **B3:** pass `-E` (or `-P` where available) to `grep`; test `a|b`, `a+`.
- **B4:** make `offset` 1-based in schema and implementation, or fix the description. Prefer 1-based to match the references.
- **B5:** remove the dead `glob`/`apply_patch` cases, or decide to add the tools in a later phase and keep them.
- Exit criteria: new unit tests for each; a live multi-call batch (edit then build) shows ordered execution.

### Phase 2: robust `edit_file` (`feat/edit-file-fuzzy`)
- Add a replacer chain after the exact match fails, ported in spirit from opencode's `edit.ts` (line-trimmed, indentation-flexible, whitespace-normalized, escape-normalized, trimmed-boundary). Require a unique match unless `replace_all`.
- On total failure return the closest block as a hint, never applied silently (MiMo behavior).
- Preserve CRLF; write via temp file plus rename.
- Exit criteria: table-driven tests with whitespace/indent drift; a live test with a small local model editing indented code.

### Phase 3: search and shell (`feat/search-shell-tools`)
- `grep`: ripgrep when available (fallback to `grep -E`), with `ignore_case`, `context`, `include`, `max_results`, `.gitignore` respect, and a "truncated, N more" marker. Keep the existing parameters working.
- `find_files`: `**` globs, `.gitignore`, result cap, mtime ordering.
- `bash`: add `workdir`; add `run_in_background` with `bash_output` and `kill_bash`, wired into the existing background-job panel and ADR-0047 live buffer. Write an ADR first (background shell lifecycle, permissions, and cleanup at exit).
- Exit criteria: a live test starting a dev server, hitting it, and killing it.

### Phase 4: feedback and web (`feat/post-edit-check-web-fetch`)
- Post-edit check: per-agent opt-in config `post_edit_command` (for example `gofmt -l`, `go vet`, `tsc --noEmit`) run after `edit_file`/`write_file`, with output appended to the tool result and capped. Real LSP is out of scope for now.
- `web_fetch`: HTTPS upgrade, HTML to markdown, `max_bytes`/format parameters, spill of oversized bodies like other tool output. Fold `http_get` into it. Document an MCP search server as the web-search route instead of building one.
- Exit criteria: live fetch of a docs page; post-edit output visible in the transcript.

### Phase 5: nice to have (separate issues, no commitment)
- `multiedit` (atomic, one file) and possibly `apply_patch` for models that handle patch syntax well.
- `ask_user` tool, implemented over the ADR-0052 ask layer so it works in the TUI, ACP and Telegram.
- Image support in `read_file` for vision-capable agents.
- Sub-agent `status`/`wait`/`send` to complement spawn/cancel.
- Skills, notebooks, scheduling, LSP navigation only on demand.

### Code anchors (all under `internal/agent/local/` unless noted)

| Concern | Where |
|---|---|
| Tool schema list (add new tools here) | `schemas()` in `tools.go`; base list, then `filterTools` (honors `limits.included_tools`/`excluded_tools`), then dynamic tools (otel, session, task, agent) appended after filtering |
| Tool execution switch | `dispatchTool()` in `tools.go`; per-tool `run*` functions below it (`runBash`, `runGrep`, `runFindFiles`, `runReadFile`, `runEditFile`, ...) |
| Batch execution (B1) | `executeToolCalls()` and `dispatchOneTool()` in `local.go`; permissions are collected synchronously first, then one goroutine per call |
| Permission gating | `toolNeedsPermission()` in `local.go` (also drives `SideEffectingTool`, which decides whether a failed turn is recorded in history) and `checkPermission` |
| Edit preview / transcript line | `toolDiff()`, `printToolLine()`, `toolArgSummary()` in `local.go`; `summary_render.go` for the compaction summary |
| Tool result cap and spill | `spill.go` (`spillToolOutput`); per-agent cap is `AgentToolResultMaxByteCount` in `internal/config` |
| Context-reduction ladder | `reduction.go` and `internal/contextreduce` (`ProtectedTools` defaults to `bash`, `read_file`; add any new core tool there if it must survive pruning) |
| Tool-call format parsing | `toolparse.go`, `detect.go`, `toolrecover.go` (leaked-markup recovery validates against the request's tool schemas, so new tools get that for free) |
| Background jobs / live buffer | `background.go`, `jobstore.go`, `internal/livebuf`; TUI side in `cmd/milk/attach.go`, `cmd/milk/background_ui.go` |
| Ask layer (for a future `ask_user`) | `cmd/milk/permask.go` (ADR-0052) |
| Tool docs | `docs/guides/tooling.md`; `internal/selfdocs` indexes it for `milk_config_help` |
| Existing contract/scripted-call tests | `memory_tools_contract_test.go`, `oob_tools_test.go`, `multicall_test.go`, `tools_test.go` |

### Cross-cutting
- Respect `limits.included_tools` / `limits.excluded_tools` and the ADR-0058 reduction ladder for every new tool, and add one-line descriptions for the low-pressure layers.
- Update `docs/guides/tooling.md` and the `CLAUDE.md` tool notes with each phase.
- Add to the tool-set contract tests so a tool that is dispatched but not defined (the B5 pattern) fails CI.

## 6. Risks and open questions
- B1 changes execution semantics. A barrier model is slower for batches of independent writes; accepting that is probably right, but it should be an explicit decision in an ADR.
- B2 depends on how ACP processes are spawned; confirm before claiming it is a bug.
- A ripgrep dependency needs a fallback; the repo currently relies only on POSIX `grep`/`find`.
- Fuzzy edit matching can apply a wrong edit if a replacer is too permissive; keep the uniqueness requirement and test false-positive cases.
- Background bash needs a cleanup story (exit, session switch, crash) before it is exposed.
