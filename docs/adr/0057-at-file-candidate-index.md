# 57. @-File Completion Reads a Cached Candidate Index

Date: 2026-10-13

Status: Accepted

## Context

Issue #198: typing `@sess` suggested nothing in a repo that has `docs/.../acp-session-resume-plan.md`. `@`-completion walked the tree on the UI path on every hint rebuild and Tab press, with a visit budget (200–2000 entries), skipped only `.git`/`.claude`, and filtered `.gitignore` *after* the walk with a `git check-ignore` process per rebuild. In this repo the ignored `.mimocode/node_modules` (≈3,400 files), `website/.venv` (≈18,800) and `website/site` sort before `cmd/` and `docs/`, so the budget was spent on entries that were then thrown away. The three passes also disagreed on matching (contains vs prefix).

## Research

How other harnesses feed the same feature (sources read for this ADR):

| Harness | Candidate source | Ignore handling | Refresh | Matching |
|---|---|---|---|---|
| OpenCode | native fff index, fallback `rg --files` built once in a forked fiber | the tool itself; `.git` excluded | built once, queried synchronously per key | fuzzy + frecency; 20 results, non-git capped at 100k |
| MiMo-Code | `rg --files` → cached `{files, dirs}` | ripgrep | lazy: refreshed on use | `fuzzysort`; hidden dirs sort last |
| Codex CLI | `ignore::WalkBuilder` run once per `@` token, incremental | during descent, git-scoped | one session per token; keystrokes only update the query; cancel flag every 1024 entries | nucleo, score then path; 20 results |
| Gemini CLI | `fdir` crawl, 20,000-file cap | directory filter during the crawl | TTL cache, chokidar watcher + 150 ms debounce, result cache narrowed by query prefix | fzf; basename-prefix, then match position, then shorter path |
| aider | HEAD tree + index (`git`), tracked files only | gitignore only implicitly | per commit; completer rebuilt per prompt, in a thread | prefix, ≥3 chars; untracked files invisible; no non-git support |
| Claude Code | documented only as fast traversal / bundled ripgrep; `respectGitignore`; custom `fileSuggestion` command (5 s, 15 results) | gitignore, `.ignore`/`.rgignore` | pre-warmed at startup, session cache with background refresh (changelog) | filename match above folder-only match |

Common to all: the list is built once, off the keystroke path, by the ignore engine itself; keystrokes are in-memory queries; candidates and results are capped; refresh is lazy or debounced; basename matches outrank folder matches. Claude Code's changelog doubles as a list of failure modes to avoid: re-scanning every turn in non-git directories, files created mid-session not appearing, freshly initialised repos listing only config files, and directories with more than 100 entries not being searched.

## Decision

`cmd/milk/fileindex.go`:

- **Candidates from git, not a walk.** `git ls-files -z --cached --others --exclude-standard` (one process, in the background) returns tracked plus untracked-not-ignored files with nested `.gitignore`, `.git/info/exclude` and global excludes honoured by git. Untracked files are included (unlike aider; fixes Claude Code's fresh-repo case). Directories are derived from the paths. Files deleted from the work tree but still tracked are dropped with an `Lstat` on the few results shown.
- **Non-git fallback:** a walk bounded by entry count and a 5 s deadline that prunes ballast directory names (`node_modules`, `.venv`, `target`, `dist`, `build`, `vendor`, caches, …) while descending — never after.
- **Fixed memory:** at most 50,000 candidates (files + directories), lower-cased once at build time.
- **One index per working directory** (at most 4 kept), built single-flight and pre-warmed at startup. A snapshot is served for 3 s; a later use refreshes it in the background while the old snapshot keeps answering, and the end of an agent turn marks it stale (files were probably created). A finished build sends `fileIndexReadyMsg` so hints shown from a flat listing are redrawn.
- **Keystrokes are an in-memory filter** (≈1.5 ms over 40,000 candidates) with no process spawn and no directory walk. Tab may wait up to 250 ms for the very first build.
- **Ranking:** base name starts with the fragment, then base name contains it, then the path below the typed directory contains it (2+ characters); within a tier direct children before deeper, shorter path, then alphabetical. A one-character fragment only lists direct children (the existing ≥2-character guard for anything recursive); an empty fragment lists the directory.
- **Outside the index:** absolute paths and paths leaving the working directory list one directory at a time (`ReadDir`), never walked.
- Removed: `walkMatches`, `filterGitIgnored` and `expandPath`'s second recursive pass.

## Consequences

- `@sess` finds `docs/internal/plans/acp-session-resume-plan.md` whatever the size of ignored directories next to it, and no per-keystroke git process remains.
- A repo with more than 50,000 candidates is truncated in `git ls-files` order (alphabetical by path); late-alphabet directories can be missing there. The cap is deliberately fixed.
- Not done (non-goals of the issue / follow-ups): fuzzy subsequence matching, a recency or frecency boost, a file watcher. A boost for files touched in the session can be added to the same ranking later; fuzzy matching should only ever run over this cached list.
