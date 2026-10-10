# Documentation map

**Switch models, not context.** milk routes each prompt between a cheap primary
agent and a deep escalation agent — keeping the whole conversation in sync
across both — and layers tools, MCP, multi-agent workflows, persistent memory,
and an eval harness on top.

This page is the map of the documentation. Everything published lives in one of
four sections below; each section has its own overview page saying what's in it
and in what order to read it. If you're new, just follow the sections top to
bottom.

## Start here

- [Getting started](getting-started.md) — install milk, wire up your first
  primary + escalation pair, and complete your first routed turn. The fastest
  path to a working setup; no specific backend required.

## Guides — task-oriented, day to day

Read in order the first time, jump straight to a page afterwards.

- [Providers & configuration](../guides/providers.md) — every agent backend
  (local inference servers, Bedrock, Claude CLI, aider, smolagents, Bearer
  APIs), auth transports, context/memory tuning.
- [Routing, sessions & workflows](../guides/workflows.md) — how a prompt gets
  routed, session states, sticky escalation, the native `/workflow` engine.
- [Tooling, MCP & tool-agents](../guides/tooling.md) — built-in tools,
  agent-as-tool, MCP servers (including OAuth), attachments, machine-readable
  output.
- [Operations](../guides/operations.md) — running milk day to day: memory,
  observability, loop detection, task tracking, remote oversight.

## Reference

- [Evaluation (`milk eval`)](../reference/eval.md) — run scenario prompts
  against real agent backends, score them with an LLM judge, compare reports.
- [ACP integration](../reference/acp-integration.md) — embedding milk in an
  editor or any client via `milk serve --acp` (what's implemented today).
- [Specification & CLI reference](../reference/spec.md) — the architecture of
  the whole system and the full CLI surface.

## Architecture & decisions

- [Decision records (ADR)](../adr/README.md) — why things are the way they are,
  one decision per record.

## Where do I find…?

| I want to… | Go to |
|---|---|
| Install milk and run a first turn | [Getting started](getting-started.md) |
| Add or configure an agent backend | [Providers & configuration](../guides/providers.md) |
| Understand routing / fix escalation behavior | [Routing, sessions & workflows](../guides/workflows.md) |
| Connect MCP servers or use tools | [Tooling, MCP & tool-agents](../guides/tooling.md) |
| Set up memory, observability, remote oversight | [Operations](../guides/operations.md) |
| Consume milk's machine-readable output (`--output-format stream-json`) | [Tooling & MCP — machine-readable output](../guides/tooling.md#machine-readable-output) |
| Embed milk in an editor/client over ACP | [ACP integration](../reference/acp-integration.md) |
| Benchmark agents with `milk eval` | [Evaluation](../reference/eval.md) |
| Look up a CLI flag or config field | [Specification & CLI reference](../reference/spec.md) |
| Understand a design decision | [ADRs](../adr/README.md) |

## For contributors

Design notes, implementation plans, and analyses live in the source tree under
[`docs/internal/`](https://github.com/scoutme/milk/tree/main/docs/internal)
(deliberately not published on this site). Branch conventions and commit style
are in
[`docs/internal/branching-strategy.md`](https://github.com/scoutme/milk/blob/main/docs/internal/branching-strategy.md).
