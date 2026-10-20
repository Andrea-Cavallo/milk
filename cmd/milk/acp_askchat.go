package main

// ask_user's typed-chat answer floor over ACP — the parity gap from issue
// #266 (follow-up to #249): a client without form dialogs (no
// clientCapabilities.elicitation.form) previously had no direct surface for
// an agent's question, so it raced remote oversight alone and a foreground
// question was unanswerable on plain ACP chat. The floor mirrors the setup
// wizard's typed chat (acp_setup.go): the question is announced as a chat
// message and the next session/prompt text is consumed as its answer —
// never routed to a model.
//
// The mechanics differ from the wizards in one way: a foreground question is
// asked *mid-turn* (the tool loop is inside runTurn holding turnMu), so the
// answer prompt must be intercepted before turnMu — as.prompt's pre-lock
// hook below — or it deadlocks against the very turn that asked. With no
// turn in flight the prompt falls through to runTurn, where the
// pending-init and pending-telegram wizards get first claim (they consume
// under turnMu) and the question takes what remains.
//
// While a question is armed, typed input belongs to it: any text is the
// answer, a cancel/quit/abort word withdraws it ("no answer" — the tool
// returns guidance, never an error), and a recognized /command withdraws it
// too and then runs normally (extractSlashCommand knows only real commands,
// so unknown "/text" is plain text — wizard parity). Synthetic follow-up
// prompts (backgroundFollowupPrompt) are never answers. One question is
// armed per session at a time.

import (
	"context"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/scoutme/milk/internal/transport/acp"
)

// askSlot is one armed question waiting for a typed answer. answers is
// buffered (1) so the taker never blocks on a waiter that raced a cancel;
// done is closed when the question is withdrawn unanswered. Owned by
// acpSession.pendingAsk (guarded by as.mu).
type askSlot struct {
	answers chan string
	done    chan struct{}
}

// typedAskAnswer returns the ACP direct surface for a question on a client
// without form dialogs: arm the slot (one at a time — a second concurrent
// question gives up this surface and races remote oversight alone), announce
// the question on the chat floor, and wait for the typed answer, the
// withdrawal, or external cancellation (a session/cancel ends the turn's
// ctx). note is the timed-answer line for background asks, "" for foreground
// ones (which wait for a human — per #249, silence is never an answer).
func (as *acpSession) typedAskAnswer(question string, options []string, note string) func(context.Context) (string, bool) {
	return func(c context.Context) (string, bool) {
		slot := &askSlot{answers: make(chan string, 1), done: make(chan struct{})}
		as.mu.Lock()
		if as.pendingAsk != nil {
			as.mu.Unlock()
			return "", false // one typed question at a time — this surface gives up
		}
		as.pendingAsk = slot
		as.mu.Unlock()
		defer func() {
			as.mu.Lock()
			if as.pendingAsk == slot {
				as.pendingAsk = nil
			}
			as.mu.Unlock()
		}()

		// The question floor: userAskPrompt's shared rendering (the same
		// text the TUI shows) minus its trailing "your answer: " input
		// marker, plus the typed-chat instructions ACP needs spelled out
		// because chat has no esc key.
		msg := strings.TrimSuffix(ansi.Strip(userAskPrompt(question, options, note)), "your answer: ")
		msg += "type your answer here as a new message ('cancel' skips the question; a /command aborts it and runs instead)"
		as.notify(acp.AgentMessageChunk(as.liveID("ask"), msg))

		select {
		case ans := <-slot.answers:
			return ans, true
		case <-slot.done:
			return "", false
		case <-c.Done():
			return "", false
		}
	}
}

// takeAskAnswer resolves an armed question from typed input and reports
// whether the prompt was consumed (and, when consumed, whether it answered
// the question or withdrew it). An answer is delivered to the waiter; a
// cancel word withdraws the question unanswered (consumed — the turn is
// empty); a /command withdraws it and is *not* consumed, so the command runs
// through the normal slash dispatch instead.
func (as *acpSession) takeAskAnswer(text string) (consumed, answered bool) {
	text = strings.TrimSpace(text)
	as.mu.Lock()
	slot := as.pendingAsk
	if slot == nil || text == "" {
		as.mu.Unlock()
		return false, false
	}
	as.pendingAsk = nil
	as.mu.Unlock()
	if _, _, found := extractSlashCommand(text); found {
		close(slot.done) // withdraw: the command runs instead
		return false, false
	}
	if initWizardCancelWord(text) {
		close(slot.done) // withdrawn unanswered
		return true, false
	}
	slot.answers <- text
	return true, true
}

// questionSurface is the ACP direct surface for ask_user: the elicitation
// form dialog on form-capable clients (elicitAnswer — unchanged contract: a
// dismissed or empty form is "no answer"), else the typed-chat floor
// (typedAskAnswer). note is the timed-answer line for background asks.
func (as *acpSession) questionSurface(question string, options []string, note string) func(context.Context) (string, bool) {
	if as.formElicit {
		return as.elicitAnswer(question, options)
	}
	return as.typedAskAnswer(question, options, note)
}

// turnBusy reports whether a turn is currently running (turnCtx set under
// as.mu by runTurn) — the trigger for pre-turnMu answer interception.
func (as *acpSession) turnBusy() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.turnCtx != nil
}
