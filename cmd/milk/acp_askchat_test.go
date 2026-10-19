package main

// Tests for ask_user's typed-chat answer floor over ACP (issue #266): the
// question is announced on the chat floor and the next session/prompt text
// is consumed as its answer — in runTurn (no turn in flight) and intercepted
// before turnMu (mid-turn: the answer prompt must never block on the lock of
// the turn that asked). Escape hatches: a cancel word withdraws the question
// unanswered, a /command withdraws it and runs.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/transport/acp"
)

// armQuestion runs as.askUser in the background and waits for the question
// to appear on the chat floor (the typed-chat slot is armed before the
// announcement, so sync on the announcement itself). Returns the answer
// channel.
func armQuestion(t *testing.T, as *acpSession, conn *fakeACPConn, question string, options []string) chan [2]any {
	t.Helper()
	out := make(chan [2]any, 1)
	go func() {
		answer, ok := as.askUser(context.Background(), question, options)
		out <- [2]any{answer, ok}
	}()
	waitFor(t, "question announced", func() bool {
		return strings.Contains(strings.Join(acpChunks(conn), "\n"), question)
	})
	return out
}

// waitFor polls cond until it holds or the 2s deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestAskChat_AnsweredByTyping: with no turn in flight the prompt falls
// through to runTurn, where the armed question consumes it as the answer —
// the turn is empty (echo + ack) and never reaches the model.
func TestAskChat_AnsweredByTyping(t *testing.T) {
	server, conn := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)

	ans := armQuestion(t, as, conn, "which color?", []string{"blue", "green"})
	if chunks := acpChunks(conn); len(chunks) == 0 || !strings.Contains(chunks[0], "which color?") {
		t.Fatalf("question not announced on the chat floor: %v", chunks)
	}

	acpPromptText(t, server, id, "blue")

	res := <-ans
	if res[0] != "blue" || res[1] != true {
		t.Fatalf("askUser = (%v, %v), want (blue, true)", res[0], res[1])
	}
	chunks := acpChunks(conn)
	if !strings.Contains(chunks[len(chunks)-1], "answer recorded") {
		t.Errorf("no ack chunk, got %v", chunks)
	}
	as.mu.Lock()
	pending := as.pendingAsk
	as.mu.Unlock()
	if pending != nil {
		t.Error("slot still armed after the answer was taken")
	}
}

// TestAskChat_CancelWordWithdraws: a cancel word withdraws the question
// unanswered (the tool returns guidance — never an error) and consumes the
// turn.
func TestAskChat_CancelWordWithdraws(t *testing.T) {
	server, conn := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)

	ans := armQuestion(t, as, conn, "which color?", nil)
	acpPromptText(t, server, id, "cancel")

	res := <-ans
	if res[1] != false {
		t.Fatalf("withdrawn question must resolve as (\"\", false): got (%v, %v)", res[0], res[1])
	}
}

// TestAskChat_SlashWithdrawsAndRuns: a recognized /command withdraws the
// question and runs through normal slash dispatch instead of being
// swallowed as answer (wizard parity — unknown "/text" is plain text and
// would answer the question, exactly like the wizards' escape hatches).
func TestAskChat_SlashWithdrawsAndRuns(t *testing.T) {
	server, conn := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)

	ans := armQuestion(t, as, conn, "which color?", nil)
	acpPromptText(t, server, id, "/help")

	res := <-ans
	if res[1] != false {
		t.Fatalf("question must withdraw unanswered on a /command: got (%v, %v)", res[0], res[1])
	}
	if chunks := acpChunks(conn); !strings.Contains(strings.Join(chunks, "\n"), "help") {
		t.Errorf("the /command must run (help listing expected), got %v", chunks)
	}
}

// TestAskChat_MidTurnAnswerBeforeLock: a question armed by a running turn
// takes its answer before turnMu — the answer prompt completes even while
// the asking turn holds the lock (regression: it used to have no surface at
// all; a naive runTurn-only hook would deadlock here).
func TestAskChat_MidTurnAnswerBeforeLock(t *testing.T) {
	server, conn := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)

	// Simulate the running turn: it holds turnMu and has turnCtx set, and
	// holds the lock until this test releases it.
	as.turnMu.Lock()
	defer as.turnMu.Unlock()
	as.mu.Lock()
	as.turnCtx = context.Background()
	as.mu.Unlock()
	defer func() {
		as.mu.Lock()
		as.turnCtx = nil
		as.mu.Unlock()
	}()

	ans := armQuestion(t, as, conn, "which color?", nil)

	done := make(chan any, 1)
	go func() {
		done <- acpRequest(t, server, "session/prompt", acp.PromptRequest{
			SessionID: id,
			Prompt:    []acp.ContentBlock{acp.TextBlock("blue")},
		})
	}()
	select {
	case res := <-ans:
		if res[0] != "blue" || res[1] != true {
			t.Fatalf("askUser = (%v, %v), want (blue, true)", res[0], res[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the answer prompt blocked on the running turn's lock (turnMu)")
	}
	<-done
}

// TestAskChat_FormClientKeepsDialog: form-capable clients keep the
// elicitation dialog and its contract — the typed floor only serves clients
// without form dialogs.
func TestAskChat_FormClientKeepsDialog(t *testing.T) {
	server, conn, id, reqs := acpFormSession(t, []acp.CreateElicitationResponse{
		{Action: acp.ElicitationAccept, Content: map[string]any{"answer": "blue"}},
	})
	as := server.session(id)

	answer, ok := as.askUser(context.Background(), "which color?", nil)
	if !ok || answer != "blue" {
		t.Fatalf("askUser = (%q, %v), want (blue, true)", answer, ok)
	}
	if len(*reqs) == 0 {
		t.Fatal("form-capable client must get the elicitation dialog")
	}
	as.mu.Lock()
	pending := as.pendingAsk
	as.mu.Unlock()
	if pending != nil {
		t.Error("the typed floor must not arm on a form-capable client")
	}
	if chunks := acpChunks(conn); strings.Contains(strings.Join(chunks, "\n"), "type your answer here") {
		t.Error("the typed-floor hint must not be announced on a form-capable client")
	}
}
