# Guides

Task-oriented guides for using milk day to day. Read them in order the first
time — each one assumes the concepts from the previous — then jump straight to
whichever you need.

1. [Providers & configuration](providers.md) — set up agent backends: local
   inference servers, Bedrock, Claude CLI, aider, smolagents, Bearer-token
   APIs. Auth transports, context windows, memory tuning.
2. [Routing, sessions & workflows](workflows.md) — how prompts are routed
   between your primary and escalation agents, session state management,
   sticky escalation, and the native multi-agent `/workflow` engine.
3. [Tooling, MCP & tool-agents](tooling.md) — give agents hands: built-in
   tools, other agents exposed as tools, MCP servers (including OAuth),
   attachments, and machine-readable output.
4. [Operations](operations.md) — running milk in practice: memory and its
   panel, observability, loop detection, task tracking, config reload,
   remote oversight (Telegram), graceful degradation.

New to milk? Start with [Getting started](../start/getting-started.md) before
these. Looking for `milk eval`, ACP, or the full spec? Those are in
[Reference](../reference/index.md).
