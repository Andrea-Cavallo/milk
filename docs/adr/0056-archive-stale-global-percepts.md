# 56. Stale Global Percepts Are Archived by Session Time

Date: 2026-10-13

Status: Accepted

## Context

ADR-0053 decays promoted (non-core) global percepts gently and floors them at the prune threshold, so decay alone never removes them; only a size cap (`percept_store_max`, unset by default) or a manual `/forget` does. A store used for months therefore keeps every fact it ever promoted, ranked low but present and still eligible for injection. A size cap evicts by weight alone, which has no notion of "not used lately".

Consolidation also does not run "at session end": it runs after every turn handled by the primary agent, when milk exits and after a single-prompt run. Any rule counted in consolidations or sessions would depend on how chatty the sessions were.

## Decision

- **Staleness is idle running time, summed over sessions.** Each non-core global percept carries `idle_seconds`. At every consolidation, the time milk has been running since the previous pass (or since the store was opened) is added to every percept that was not used in that interval, so one 20-day session counts like ten 2-day ones and the number of passes does not matter. `last_used_at` records the last use.
- **"Used" means** re-stated (reinforced), returned by `get_memory`, or injected into a prompt (`Store.MarkUsed`, called from `perceptsForAgent` for what is actually injected). A use resets `idle_seconds` immediately.
- **Eligibility:** non-core, weight decayed to `prune_threshold`, and `idle_seconds >= global_archive_after_days` (default 14 days of running time; negative disables). Both conditions: weight is the importance signal, idle time guards against archiving something still in use.
- **Logical deletion.** A stale percept (with the edges touching it) is appended to `~/.milk/memory/global-archive.jsonl` with `archived_at`, a reason and its idle days, and only then removed from the store; a failed archive write leaves the percept in place. Engram membership and edges are cleaned up on removal.
- **Reversible and inspectable.** `/memory stale` shows idle time and time left per percept, `/memory archive` lists the archive, `/memory restore <id>` moves one back with a fresh clock (and re-adds edges whose other end still exists; refuses when an equivalent percept was re-learned meanwhile).
- **On by default**, but a percept stored before this change starts with `idle_seconds = 0`, so nothing is archived until it has gone unused for the full period after the upgrade.
- Core percepts (e.g. `/learn` facts) are never archived. Session percepts are unaffected (they are pruned by weight at consolidation as before).

## Consequences

- The global store sheds facts nobody uses, without losing them: the archive file is append-only and restorable.
- Concurrent milk processes each add their own running time to the shared file, so overlapping sessions count twice — acceptable for "session duration summed".
- A percept that is relevant but never matches a prompt's relevance gate is never injected and will age out; `get_memory` and re-stating it keep it alive.
