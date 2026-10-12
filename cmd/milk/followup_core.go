package main

// The rules for when finished background jobs trigger an automatic follow-up
// turn, shared by the TUI (maybeAutoFollowupBackgroundJobs) and the ACP server
// (requestFollowup). Each host computes "is a turn running right now" its own
// way and performs the follow-up its own way; the decision in between is this.

type followupDecision int

const (
	// followupSkip: nothing to do now; the request's retry flag is cleared.
	followupSkip followupDecision = iota
	// followupRetryLater: a turn is running; remember the request and retry
	// when it ends.
	followupRetryLater
	// followupNow: start the follow-up turn.
	followupNow
)

// decideFollowup applies the rules:
//   - nothing drainable (pending == 0): skip — a follow-up turn with nothing
//     to report just burns a turn. This is the shared home of the guard both
//     hosts used to keep inline (issue #188): a user-started job fires both
//     the per-job and the wave-done notification, so the first follow-up
//     drains the results and the second must find nothing to do;
//   - a wave of agent-spawned jobs (waitForWholeWave) follows up only once no
//     job is still active — a newer wave's own completion asks again;
//   - a user-started job follows up as soon as it ends;
//   - if a turn is running (busy), the request is deferred, not dropped.
func decideFollowup(waitForWholeWave bool, activeJobs, pending int, busy bool) followupDecision {
	if pending == 0 {
		return followupSkip
	}
	if waitForWholeWave && activeJobs > 0 {
		return followupSkip
	}
	if busy {
		return followupRetryLater
	}
	return followupNow
}

// applyFollowupDecision updates the two retry flags (agent-wave and
// user-started) for a decision about one kind of request. Starting a
// follow-up consumes both, since one turn delivers everything drained.
func applyFollowupDecision(waitForWholeWave bool, d followupDecision, wave, user *bool) {
	switch d {
	case followupNow:
		*wave, *user = false, false
	case followupRetryLater:
		if waitForWholeWave {
			*wave = true
		} else {
			*user = true
		}
	case followupSkip:
		if waitForWholeWave {
			*wave = false
		} else {
			*user = false
		}
	}
}
