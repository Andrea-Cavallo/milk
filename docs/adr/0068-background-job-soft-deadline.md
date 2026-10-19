# 68. Background jobs get a soft deadline before the hard timeout

Date: 2026-10-19

Status: Accepted

## Context

A `spawn_background_agent` job runs under a hard execution timeout (`Manager.jobTimeout`, default 20 minutes, `background_agent_timeout_minutes`). At the deadline the context is cancelled and the job fails with `timed out after 20m0s`. Issue #271 documents a live case: a research job on `mimo-v2.6-flash` ran the full 20 minutes (25 requests, ~90 s TTFT) exploring three targets, never wrote up, and returned only the `noFinalSummaryTrailHeader` trail — tool names and 200-character result heads, none of the findings the model had formed.

Two properties make this a milk problem rather than only model behavior:

- The model is never told it has a time budget, so nothing prompts a wrap-up before the cut.
- The timeout was already raised once (10 → 20 minutes, `background-subagents-implementation-plan.md` item 12) for the same symptom. A bigger number only moves the cliff; loop detection, not the timeout, is the defense against stuck jobs, so the timeout's remaining role is to stop a job that is working but will not finish — and that job should still hand back what it has.

## Decision

1. **Soft deadline.** At 80% of the job's timeout, `Manager` sends a wrap-up message through the job's existing inbox (`Manager.Send`, the `send_background_agent` path, delivered as a user message at the next tool-loop iteration): the minutes remaining, an instruction to stop exploring and write the final answer from what it has, and the `<result status="partial" …/>` convention if incomplete. Sent once per job. Delivery is best-effort, as for any inbox message.
2. **Hard cancel unchanged.** The execution deadline stays the backstop; nothing is extended.
3. **Salvage on hard timeout.** A job that still times out records its last non-empty assistant text alongside the tool trail in `Job.Result`, so the findings it had formed are not lost.
4. **No new config.** The fraction is a constant (80%) and follows `background_agent_timeout_minutes`. Jobs with a timeout under 2 minutes skip the soft deadline.

## Consequences

- Most would-be timeouts turn into a `partial` result the spawning agent can use, instead of a failure.
- A model that ignores the message still times out exactly as before — no regression, just the salvage improvement.
- The wrap-up message costs one extra user message in the job's context, only for jobs that run past 80% of the timeout.
- Workflow stages and foreground turns are unaffected; this applies to `Manager` jobs only.

Implementation: `Manager.Spawn` / `finish` in [`internal/agent/local/background.go`](../../internal/agent/local/background.go); plan in [`docs/internal/plans/background-job-soft-deadline-plan.md`](../internal/plans/background-job-soft-deadline-plan.md).
