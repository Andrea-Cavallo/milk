package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// handleInterruptCmd with a running turn: identical to the Ctrl+C branch
// of handleBusyKey — cancel the turn and mark it interrupted (the
// completion handler then prints [interrupted]).
func TestInterrupt_CancelsRunningTurn(t *testing.T) {
	m := layoutTestModel(t, 100, 30)
	canceled := false
	m.busy = true
	m.cancelTurn = func() { canceled = true }

	out, _ := m.handleSlashInput(cmdInterrupt, "")
	got := out.(model)
	if !canceled {
		t.Fatal("/interrupt did not cancel the running turn")
	}
	if !got.interrupted {
		t.Fatal("/interrupt did not mark the turn interrupted (Ctrl+C parity)")
	}
	if got.cancelTurn != nil {
		t.Fatal("cancelTurn left set after interrupt")
	}
}

// With no turn running it reports that — it must never fall through to the
// model as prompt text.
func TestInterrupt_IdleNotice(t *testing.T) {
	m := layoutTestModel(t, 100, 30)
	out, _ := m.handleSlashInput(cmdInterrupt, "")
	got := out.(model)
	if !strings.Contains(got.transcript.String(), "nothing to interrupt") {
		t.Fatalf("idle /interrupt printed no notice: %q", got.transcript.String())
	}
	if len(got.sessionHistory) != 0 {
		t.Fatalf("idle /interrupt leaked into history: %v", got.sessionHistory)
	}
}

// Busy Enter with /interrupt in the buffer: busySafeCommands routes it to
// the handler instead of the "unavailable while agent is responding" hint.
func TestInterrupt_BusyEnterExecutes(t *testing.T) {
	m := layoutTestModel(t, 100, 30)
	canceled := false
	m.busy = true
	m.cancelTurn = func() { canceled = true }
	m.ta.SetValue("/interrupt")

	out, _ := m.handleBusyKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(model)
	if !canceled {
		t.Fatal("busy Enter on /interrupt did not cancel the turn")
	}
	if hint := got.busyHint; strings.Contains(hint, "unavailable") {
		t.Fatalf("busy Enter treated /interrupt as unavailable: %q", hint)
	}
}

// Remote (Telegram) /interrupt while busy: acts now — the message must NOT
// go to pendingRemoteInputs (it exists precisely to reach the turn that
// would swallow it).
func TestInterrupt_RemoteBusyCancelsNotQueues(t *testing.T) {
	m := layoutTestModel(t, 100, 30)
	canceled := false
	m.busy = true
	m.cancelTurn = func() { canceled = true }

	out, _ := m.Update(remoteInputMsg{text: "  /interrupt  "})
	got := out.(model)
	if !canceled {
		t.Fatal("remote /interrupt did not cancel the running turn")
	}
	if len(got.st.pendingRemoteInputs) != 0 {
		t.Fatalf("remote /interrupt was queued instead of acting: %v", got.st.pendingRemoteInputs)
	}
	if !strings.Contains(got.transcript.String(), "[telegram]") {
		t.Fatalf("remote /interrupt not echoed: %q", got.transcript.String())
	}
}

// Remote /interrupt with no turn running: notice only — never dispatched
// to the model as a prompt.
func TestInterrupt_RemoteIdleNoDispatch(t *testing.T) {
	m := layoutTestModel(t, 100, 30)

	out, _ := m.Update(remoteInputMsg{text: "/interrupt"})
	got := out.(model)
	if !strings.Contains(got.transcript.String(), "nothing to interrupt") {
		t.Fatalf("remote idle /interrupt printed no notice: %q", got.transcript.String())
	}
	if len(got.sessionHistory) != 0 || len(got.globalHistory) != 0 {
		t.Fatalf("remote /interrupt dispatched to the model: hist=%v", got.sessionHistory)
	}
	if got.busy {
		t.Fatal("idle remote /interrupt started a turn")
	}
}
