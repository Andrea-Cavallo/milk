package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/transport/acp"
)

// Client prompt /interrupt while a turn is running: the prompt must NOT
// queue behind turnMu (it exists to cancel that turn) — it cancels,
// echoes to the client, and answers immediately, while the test literally
// holds the running turn's lock.
func TestACPInterrupt_BusyPromptCancelsWithoutQueueing(t *testing.T) {
	server, _ := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)

	// Simulate the running turn: holds turnMu, turnCtx set, cancellable.
	as.turnMu.Lock()
	defer as.turnMu.Unlock()
	canceled := make(chan struct{})
	as.mu.Lock()
	as.turnCtx = context.Background()
	as.cancel = func() { close(canceled) }
	as.mu.Unlock()
	defer func() {
		as.mu.Lock()
		as.turnCtx = nil
		as.cancel = nil
		as.mu.Unlock()
	}()

	done := make(chan any, 1)
	go func() {
		done <- acpRequest(t, server, "session/prompt", acp.PromptRequest{
			SessionID: id,
			Prompt:    []acp.ContentBlock{acp.TextBlock("/interrupt")},
		})
	}()
	select {
	case res := <-done:
		if res == nil {
			t.Fatal("no PromptResponse for /interrupt")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("/interrupt blocked on the running turn's lock (turnMu)")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("/interrupt did not cancel the running turn")
	}

	// Client sees the command echo (user_message MessageUpsert) and the
	// interrupt notice (agent_message_chunk ContentChunk).
	var sawEcho, sawNotice bool
	for _, n := range server.conn.(*fakeACPConn).sent() {
		upd, ok := n.Params.(acp.UpdateSessionNotification)
		if !ok {
			continue
		}
		switch u := upd.Update.(type) {
		case acp.MessageUpsert:
			if u.SessionUpdate == "user_message" {
				var content string
				for _, b := range u.Content {
					content += b.Text
				}
				if strings.Contains(content, "/interrupt") {
					sawEcho = true
				}
			}
		case acp.ContentChunk:
			if u.SessionUpdate == "agent_message_chunk" && strings.Contains(u.Content.Text, "interrupting") {
				sawNotice = true
			}
		}
	}
	if !sawEcho {
		t.Error("/interrupt never echoed to the client as a user message")
	}
	if !sawNotice {
		t.Error("client never saw the interrupting notice")
	}
}

// Idle /interrupt over ACP runs the notice (the busy case is intercepted
// earlier, in prompt()).
func TestACPInterrupt_IdleRunsNotice(t *testing.T) {
	server, _ := acpTestServer(t, "prompt")
	id := acpNewSession(t, server)
	as := server.session(id)

	handled, out, dispatch := as.runSlashCommand(nil, "/interrupt")
	if !handled {
		t.Fatal("/interrupt not recognized as a slash command")
	}
	if !strings.Contains(out, "nothing to interrupt") {
		t.Fatalf("idle notice missing: %q", out)
	}
	if dispatch != "" {
		t.Fatalf("idle /interrupt dispatched a prompt: %q", dispatch)
	}
}

// Remote (Telegram) /interrupt while a turn runs: acts immediately —
// bypasses the held turnMu, does not queue, and does not start a turn.
func TestACPRemoteInterrupt_CancelsActiveTurn(t *testing.T) {
	server, _ := acpTestServer(t, "nope")
	id := acpNewSession(t, server)
	as := server.session(id)

	as.turnMu.Lock() // held for the whole test: the bypass must not need it
	defer as.turnMu.Unlock()
	canceled := make(chan struct{})
	as.mu.Lock()
	as.turnCtx = context.Background()
	as.cancel = func() { close(canceled) }
	as.mu.Unlock()
	defer func() {
		as.mu.Lock()
		as.turnCtx = nil
		as.cancel = nil
		as.mu.Unlock()
	}()

	server.handleRemoteInput("  /interrupt  ")

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("remote /interrupt did not cancel the running turn")
	}
	as.mu.Lock()
	pending := len(as.pendingRemoteInputs)
	as.mu.Unlock()
	if pending != 0 {
		t.Fatalf("remote /interrupt queued instead of acting: %d pending", pending)
	}
}

// Remote /interrupt with an idle session: the remote surface gets the
// "nothing to turn" announcement instead of silence.
func TestACPRemoteInterrupt_NoTurnAnnounces(t *testing.T) {
	server, _ := acpTestServer(t, "idle ok")
	fake := &fakeNotifier{}
	setTestNotifier(server, fake)

	acpNewSession(t, server)
	server.handleRemoteInput("/interrupt")

	waitForOversightCall(t, fake, "start:milk:interrupt:no turn running")
}
