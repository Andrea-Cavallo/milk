# ADR-0074: Multimodal input over ACP — prompt content blocks ride the TUI's attachment path

*Date: 2026-10-21*

## Status

Accepted

## Context

ACP's `session/prompt` carries the `ContentBlock` union (`text` | `image` |
`audio` | `resource_link`), so any client with a paste gesture can attach an
image to a chat message. milk's ACP surface only read `text` blocks: a pasted
image was silently dropped — even when the configured agent had `"vision": true`
and the TUI handled the exact same paste fine (clipboard paste → `PendingAttachment`
→ multipart `image_url` part / `@path` for the claude CLI). Two surfaces, one
capability gap.

The TUI's multimodal pipeline (`cmd/milk/attachments.go`, `repl.go`'s
`dispatchAgent`) already encodes every hard-won rule: the `vision` agent flag
gates the multipart payload (sending `image_url` to a non-vision endpoint is a
hard API error on many backends), a CLI (claude) escalation runner gets
`@temp-file` `@path` references instead, non-vision agents with tool-agents get
a system-reminder instead of the payload, and session history stores compact
placeholders — never base64. Re-implementing any of that per surface would
guarantee drift.

## Decision

**ACP multimodal input is parsed in one place (`cmd/milk/acp_media.go`) and
delivered through the TUI's existing attachment shape** — `parseACPPrompt`
turns a prompt's blocks into the same `PendingAttachment` values `/attach` and
clipboard paste produce, so everything downstream (vision gate, data-URI
parts, `@path` staging, history compaction) is the TUI's code path, not a
parallel one.

- **Block handling** (`ContentBlock` in `internal/transport/acp/acp.go` is the
  full union): `text` concatenates verbatim (unchanged behavior); `image`
  decodes — bare base64 per the spec, a full `data:` URI tolerated on the way
  in — and rides the turn as `local.WithImageParts` per-call ctx parts (issue
  #297's discipline: agents are shared with background work, never an instance
  field), or as `@path` temp files when a CLI runner handles the turn (the
  files live until the turn ends); `audio` and open-set kinds degrade to a
  placeholder line plus a client-visible notice — never an error, never a
  dropped prompt; `resource_link` is referenced in the prompt, with inline
  `resource.text` inlined.
- **The vision gate is not re-decided per surface.** `local.Run` applies
  `agent.vision` exactly as for a TUI paste (multipart payload, tool-agent
  forwarding, or the drop notice).
- **The accept-echo carries the client's own blocks.** The `user_message`
  upsert under the response's `messageId` is the prompt's `ContentBlock`s
  round-tripped unchanged (v1 clients: per-block `user_message_chunk`s), so a
  client renders its pasted image back. `ContentBlock` keeps unmodelled wire
  fields (`annotations`, `_meta`, open-set payloads) in an `extra` map so the
  echo is lossless.
- **Session history stays compact.** The recorded user turn is the assembled
  placeholder text (`[attached image: image-1.png (image/png)]`), so replay
  stays small and no payload is persisted — the same rule as TUI attachments.

Rejected: forwarding images as inline data-URI text for every runner (a
non-vision endpoint rejects it hard, and models confabulate base64 they can't
see — the exact failure the TUI's multipart path exists to avoid), and a
separate ACP-only attachment model (two pipelines = drift; see above).

## Amendments (post-review hardening, same day)

A full review of every multimodal path (TUI, ACP, local agent, transports)
surfaced gaps that this ADR's implementation shares with the older TUI code;
the fixes below apply to both surfaces and refine the decisions above:

1. **The image-drop retry now requires positive evidence.** The
   `image_tokens == 0` trigger fired on backends that simply never report
   image accounting (most OpenAI-compatible servers) — every image request ran
   three full generations, two discarded, all charged. `usage` with no
   `prompt_tokens_details` at all is now "unknown", never a drop; only a
   backend that reports image accounting and saw zero image tokens triggers
   the replay budget. Discarded attempts are no longer charged to token
   accounting, a later attempt's transport error falls back to the previous
   attempt's buffered response, replays are byte-identical (one seed per
   request), and an exhausted budget surfaces a user-visible notice instead of
   letting the "I can't see the image" non-answer stand unexplained.
2. **A rejecting endpoint degrades instead of failing the turn.** A mis-set
   `vision: true` used to surface the backend's hard error; the turn is now
   retried once with the image parts stripped (`stripImageParts`, in place so
   later tool-loop iterations stay image-free) plus a notice.
3. **CLI image staging moved into the runners** (`stageCLIImageRefs`,
   `cliRunner.Execute`/`RunToolCall`). The hosts (TUI dispatch, ACP runTurn)
   only wire ctx parts; the runner that handles the turn stages its own
   `@temp-file` references and cleans them up. This fixes three bugs at once:
   temp files deleted before the turn ran (dangling `@path`, self-escalation
   handed dead refs), `@path` noise in local-model prompts, and images being
   dropped entirely for a CLI/subprocess *primary*.
4. **Payload-type and size guards**: content sniffing beats extensions (a text
   file named `foo.png` is text; unknown binary is `application/octet-stream`,
   never inlined as text), known MIME survives the clipboard round trip, SVG
   inlines as text, non-raster payloads in ACP `image` blocks never become
   `image_url`, and attachments are capped at 32MB.
5. **Silent-drop paths closed**: the Bedrock-Converse and Responses-API
   transports now carry image parts (they dropped them without a trace);
   background-job clones keep `supportsVision`; user-spawned background jobs
   carry staged attachments; self-escalation and `RecordNeed` record the
   compact session content, never payloads; an over-limit ACP JSON-RPC line
   drops with a warning instead of ending the connection.
6. **ACP sessions no longer freeze their config.** The TUI hot-reloads on a
   config write; an ACP session kept the runners it was created with, so
   enabling `"vision": true` never reached an open client session. Prompt
   entry now fingerprints both config files and rebuilds the runners through
   the `buildRunners` seam on change (with a notice), mirroring the TUI.

## Amendment (image budget and context interplay, 2026-10-22)

Found live: a pasted 7.4 MB screenshot read as a 2.5M-token context and a
cached follow-up iteration was mistaken for a dropped image.

7. **Images are shrunk once, at attach time** (`internal/imageprep`, stdlib
   only): long edge ≤ 2000 px and ≤ 500 KB encoded, by area-averaging
   downscale, EXIF orientation applied before the EXIF-dropping re-encode,
   PNG kept for flat/transparent images and JPEG for photos. In-limit images
   pass through byte-identical; undecodable/WebP/BMP and > 50 MP images are
   sent as-is. The 500 KB cap (not the backend's 5 MiB) is deliberate: base64
   of a larger image would exceed the 900 KB default request guard and make it
   drop history on every request of the turn. MCP tool images are shrunk at
   the one place they join the conversation (`dispatchBatch`).
8. **Token estimates never count base64**: the pre-response estimate that
   feeds the ADR-0058 reduction ladder counts an image as a flat ~4K tokens,
   and the status bar's live request-size estimate excludes inlined image
   data. Wire-size gates (the payload guard) still count real bytes.
9. **Image-drop detection runs only on the image's first send** — later
   tool-loop iterations are served from the backend's prompt cache and report
   no `image_tokens`, which is not drop evidence.
10. **`grep` no longer depends on ripgrep for correctness**: rg → `git grep`
    (honors `.gitignore`) → a built-in Go search (no external binary, RE2
    syntax, skips hidden/vendored/binary files), with a 2000-byte line cap on
    every backend. A runtime ripgrep download (as MiMo-Code does) was rejected:
    an unprompted fetch-and-execute of a binary needs pinned checksums per
    platform and is at odds with milk's approval-first stance.
