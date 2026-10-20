package main

import (
	"context"
	"strings"
	"sync"
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

// busySession holds a live turn (turnMu + turnCtx + cancel) for interrupt
// tests and releases everything at cleanup. canceled fires at most once.
func busyInterruptFixture(t *testing.T) (*acpServer, acp.SessionID, *acpSession, chan struct{}, func()) {
	t.Helper()
	server, _ := acpTestServer(t, "MUST NOT REACH THE MODEL")
	id := acpNewSession(t, server)
	as := server.session(id)
	as.turnMu.Lock()
	canceled := make(chan struct{})
	var once sync.Once
	as.mu.Lock()
	as.turnCtx = context.Background()
	as.cancel = func() { once.Do(func() { close(canceled) }) }
	as.mu.Unlock()
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			as.mu.Lock()
			as.turnCtx = nil
			as.cancel = nil
			as.mu.Unlock()
			as.turnMu.Unlock()
		})
	}
	t.Cleanup(release)
	return server, id, as, canceled, release
}

// The prompt form accepts trailing words: "/interrupt stop it" is still the
// command (shared first-token rule) — cancels without queueing on turnMu.
func TestACPInterrupt_BusyPromptWithArgsCancels(t *testing.T) {
	server, id, _, canceled, _ := busyInterruptFixture(t)

	done := make(chan any, 1)
	go func() {
		done <- acpRequest(t, server, "session/prompt", acp.PromptRequest{
			SessionID: id,
			Prompt:    []acp.ContentBlock{acp.TextBlock("/interrupt stop it now")},
		})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("leading-args /interrupt blocked on the running turn's lock (turnMu)")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("leading-args /interrupt did not cancel the running turn")
	}
}

// Mid-input is plain prompt text on ACP too: it must NOT cancel, and
// without the intercept it queues behind turnMu like any prompt (released
// at cleanup, where it runs as an ordinary turn).
func TestACPInterrupt_BusyPromptMidInputDoesNotCancel(t *testing.T) {
	server, id, _, canceled, release := busyInterruptFixture(t)

	done := make(chan any, 1)
	go func() {
		done <- acpRequest(t, server, "session/prompt", acp.PromptRequest{
			SessionID: id,
			Prompt:    []acp.ContentBlock{acp.TextBlock("please /interrupt this turn")},
		})
	}()
	select {
	case <-canceled:
		t.Fatal("mid-input text cancelled the running turn")
	case <-done:
		t.Fatal("mid-input prompt was intercepted instead of queueing behind the turn")
	case <-time.After(300 * time.Millisecond):
	}
	// Release the turn: the queued prompt then runs as a normal turn —
	// waited for here so no request outlives the test.
	release()
	<-done
}

// Remote (Telegram) input follows the same rule: only a message starting
// with /interrupt cancels; mid-input queues as an ordinary message.
func TestACPRemoteInterrupt_FirstTokenOnly(t *testing.T) {
	for _, c := range []struct {
		text       string
		wantCancel bool
	}{
		{"  /interrupt stop it  ", true},
		{"please /interrupt now", false},
		{"/interrupted", false},
	} {
		t.Run(c.text, func(t *testing.T) {
			server, _, as, canceled, _ := busyInterruptFixture(t)
			server.handleRemoteInput(c.text)
			deadline := time.After(500 * time.Millisecond)
			if !c.wantCancel {
				select {
				case <-canceled:
					t.Fatal("mid-input/lookalike text cancelled the running turn")
				case <-deadline:
					as.mu.Lock()
					pending := len(as.pendingRemoteInputs)
					as.mu.Unlock()
					if pending != 1 {
						t.Fatalf("expected the text queued as an ordinary message, pending = %d", pending)
					}
				}
				return
			}
			select {
			case <-canceled:
			case <-deadline:
				t.Fatal("leading /interrupt did not cancel the running turn")
			}
		})
	}
}
