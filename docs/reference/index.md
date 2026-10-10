# Reference

Reference material — lookup, not tutorials. For hands-on setup and day-to-day
usage, start with the [Guides](../guides/index.md) instead.

- [Evaluation (`milk eval`)](eval.md) — the eval harness: scenario format,
  adapters (`claude-code`, `milk-tui`) and their options, LLM judging, and
  reports.
- [ACP integration](acp-integration.md) — `milk serve --acp` as an Agent
  Client Protocol v2 agent: exactly what's implemented today, the JSON-RPC
  exchange, and known gaps.
- [Specification & CLI reference](spec.md) — the full architecture (routing &
  sessions, providers, tools & MCP, orchestration, memory & safety,
  observability/eval) and the complete CLI surface.

Design decisions behind the architecture are recorded as
[ADRs](../adr/README.md).
