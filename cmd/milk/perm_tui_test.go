package main

import (
	"strings"
	"testing"
)

// TestHandlePermRequest_SessionModeAutoAnswer pins that a decided session
// mode answers tool asks as they arrive — never queued, never shown (issue
// #302) — while questions (ask_user) are never auto-answered.
func TestHandlePermRequest_SessionModeAutoAnswer(t *testing.T) {
	m := testModel()
	m.st = &interactiveState{}
	m.st.permOverrides.sessionMode = sessionPermAllowAll

	ch := make(chan string, 1)
	updated, _ := m.handlePermRequest(permRequestMsg{prompt: "Allow? [Y/n/a/d]", respCh: ch})
	mm := updated.(model)
	if got := <-ch; got != "y" {
		t.Errorf("answer = %q, want y", got)
	}
	if mm.pendingPerm != nil || len(mm.permQueue) != 0 {
		t.Errorf("prompt must not be queued under a session mode")
	}
	if !strings.Contains(mm.transcript.String(), "allow-all") {
		t.Errorf("expected an audit line naming the mode, got: %s", mm.transcript.String())
	}

	m.st.permOverrides.sessionMode = sessionPermDenyAll
	ch2 := make(chan string, 1)
	updated, _ = mm.handlePermRequest(permRequestMsg{prompt: "Allow?", respCh: ch2})
	if got := <-ch2; got != "n" {
		t.Errorf("deny-all answer = %q, want n", got)
	}
	_ = updated

	// A question is not a permission: it waits for a human. (Queued behind
	// another prompt so the test doesn't need a live textarea.)
	mm.pendingPerm = &permRequestMsg{prompt: "already pending", respCh: make(chan string, 1)}
	q := make(chan string, 1)
	updated, _ = mm.handlePermRequest(permRequestMsg{prompt: "question?", respCh: q, freeText: true})
	if len(q) != 0 {
		t.Fatalf("question auto-answered by the session mode")
	}
	if mm2 := updated.(model); len(mm2.permQueue) != 1 {
		t.Fatalf("question prompt should be queued (len=%d)", len(mm2.permQueue))
	}
}

// TestAnswerPermPrompt_BulkKeysSetMode pins the a/d keys (issue #302): they
// answer the active prompt and the whole queue AND set the session mode, and
// plain y/n answers change nothing about the mode.
func TestAnswerPermPrompt_BulkKeysSetMode(t *testing.T) {
	m := testModel()
	m.st = &interactiveState{}
	ch1, ch2 := make(chan string, 1), make(chan string, 1)
	m.pendingPerm = &permRequestMsg{prompt: "p1", respCh: ch1}
	m.permQueue = []permRequestMsg{{prompt: "p2", respCh: ch2}}

	m.answerPermPrompt("a")
	if got := <-ch1; got != "y" {
		t.Errorf("active answer = %q, want y", got)
	}
	if got := <-ch2; got != "y" {
		t.Errorf("queued answer = %q, want y", got)
	}
	if m.pendingPerm != nil || len(m.permQueue) != 0 {
		t.Errorf("queue not drained")
	}
	if mode := m.st.permOverridesNow().sessionMode; mode != sessionPermAllowAll {
		t.Errorf("mode = %v, want allow-all", mode)
	}
	if !strings.Contains(m.transcript.String(), "session permission mode: allow-all") {
		t.Errorf("setting the mode must be announced, got: %s", m.transcript.String())
	}

	// deny-all: same shape.
	ch3 := make(chan string, 1)
	m.pendingPerm = &permRequestMsg{prompt: "p3", respCh: ch3}
	m.answerPermPrompt("d")
	if got := <-ch3; got != "n" {
		t.Errorf("deny answer = %q, want n", got)
	}
	if mode := m.st.permOverridesNow().sessionMode; mode != sessionPermDenyAll {
		t.Errorf("mode = %v, want deny-all", mode)
	}

	// Plain y/n answers just the one prompt and leaves the mode alone.
	ch4 := make(chan string, 1)
	m.pendingPerm = &permRequestMsg{prompt: "p4", respCh: ch4}
	m.answerPermPrompt("y")
	if got := <-ch4; got != "y" {
		t.Errorf("answer = %q, want y", got)
	}
	if mode := m.st.permOverridesNow().sessionMode; mode != sessionPermDenyAll {
		t.Errorf("mode = %v, want deny-all (unchanged by a plain answer)", mode)
	}
}
