package local

import "context"

// Turn wrap-up / activity plumbing for the shared idle watchdog (issue #305).
//
// Both live on the per-call ctx — the agents are shared with background work
// and other turns, so no instance field may carry per-turn state (issue #297's
// discipline, same as local.WithImageParts).

type wrapUpKey struct{}
type activityKey struct{}

// WithTurnWrapUp attaches the turn's wrap-up signal to ctx: closing the
// channel asks the tool loop to finish with a forced text-only closing
// summary at its next iteration boundary (the same mechanism as the
// max-iteration wrap-up) instead of continuing to call tools. A loop that
// never reaches another boundary simply never sees it — the caller's hard
// stop covers that case. Consumed once; nil (every call site that has no
// watchdog) is a no-op.
func WithTurnWrapUp(ctx context.Context, ch <-chan struct{}) context.Context {
	return context.WithValue(ctx, wrapUpKey{}, ch)
}

// turnWrapUp returns the wrap-up signal attached to ctx, or nil.
func turnWrapUp(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(wrapUpKey{}).(<-chan struct{})
	return ch
}

// WithActivityNotify attaches a liveness callback to ctx, called on real
// internal progress events the host's writers never see: tool-loop iteration
// boundaries, tool-call execution, and stream retries (an image-drop replay
// buffers its output, so a whole generation can stream without a single byte
// reaching out). nil is a no-op.
func WithActivityNotify(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, activityKey{}, fn)
}

// activityNotify returns the liveness callback attached to ctx (never nil:
// call sites just invoke it).
func activityNotify(ctx context.Context) func() {
	if fn, _ := ctx.Value(activityKey{}).(func()); fn != nil {
		return fn
	}
	return func() {}
}

// deadlineWrapUpReminder is injected in place of the user's turn when the
// opt-in turn deadline (limits.turn_deadline_secs) asks the loop to wrap up,
// with tools disabled for that call — the deadline sibling of
// maxIterSummaryReminder. A graceful forced summary rather than a bare
// context cancel: the turn still closes with a model-authored answer.
const deadlineWrapUpReminder = `<system-reminder>
TURN DEADLINE REACHED for this turn. Tools are disabled for this response —
you cannot call any more tools. Respond now with text only:
1. What you accomplished this turn.
2. What remains to be done.
3. Any blocker or decision you need from the user.
This overrides all other instructions for this response.
</system-reminder>`
