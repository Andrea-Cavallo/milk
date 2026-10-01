package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/oversight"
	"github.com/scoutme/milk/internal/session"
)

// Toast/notification subsystem tests (issue #162, ADR-0048).
//
// The background_ui_test.go migration tests cover the emitters (job done,
// spawn, panel toggles); these cover the subsystem itself: queue semantics,
// TTL/promotion, dismissal, the generation-guarded expiry tick (the #168
// lesson applied proactively), rendering, and the /notifications history.

// toastMentions reports whether the model has — visible or waiting in the
// queue — a notification whose text contains substr. History is deliberately
// not consulted: an event that was recorded but never shown must not count
// as "the user saw a toast".
func toastMentions(m model, substr string) bool {
	for _, ev := range m.toastVisible {
		if strings.Contains(ev.text, substr) {
			return true
		}
	}
	for _, ev := range m.toastQueue {
		if strings.Contains(ev.text, substr) {
			return true
		}
	}
	return false
}

// newToastTestModel returns a fully wired model for Update/View-level tests.
func newToastTestModel(t *testing.T) model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sess, err := session.New("/repo", "")
	if err != nil {
		t.Fatal(err)
	}
	st := &interactiveState{sess: sess, cwd: "/repo", notifier: oversight.Noop{}}
	return newModel(context.Background(), st, nil, dispatchAgents{}, nil)
}

func TestNotify_RecordsHistoryTimestampAndVisible(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	m.notify("reasoning visibility: on", "/think off")

	if len(m.toastHistory) != 1 {
		t.Fatalf("history length = %d, want 1", len(m.toastHistory))
	}
	ev := m.toastHistory[0]
	if ev.at.IsZero() {
		t.Error("history entry must carry a timestamp")
	}
	if ev.text != "reasoning visibility: on" || ev.hint != "/think off" {
		t.Errorf("history entry = %+v, want text/hint recorded", ev)
	}
	if len(m.toastVisible) != 1 {
		t.Fatalf("visible length = %d, want 1", len(m.toastVisible))
	}
	if m.toastVisible[0].expiresAt.IsZero() {
		t.Error("visible toast must have an expiry set at promotion")
	}
	if len(m.toastQueue) != 0 {
		t.Errorf("queue length = %d, want 0 (promoted immediately)", len(m.toastQueue))
	}
}

func TestNotify_CapsVisibleAndQueuesTheRest(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	for i := range 5 {
		m.notify(strings.Repeat("event ", 1)+string(rune('a'+i)), "")
	}
	if len(m.toastVisible) != maxVisibleToasts {
		t.Errorf("visible = %d, want %d", len(m.toastVisible), maxVisibleToasts)
	}
	if len(m.toastQueue) != 5-maxVisibleToasts {
		t.Errorf("queue = %d, want %d", len(m.toastQueue), 5-maxVisibleToasts)
	}
	if len(m.toastHistory) != 5 {
		t.Errorf("history = %d, want 5 (all events recorded)", len(m.toastHistory))
	}
}

// TestExpireToasts_PromotesQueuedWithFreshTTL pins the core queue contract:
// a queued event must never expire unseen — promotion stamps a *fresh* TTL,
// so its lifetime starts when it becomes visible, not when it was enqueued.
func TestExpireToasts_PromotesQueuedWithFreshTTL(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	for i := range 4 {
		m.notify(string(rune('a'+i)), "")
	}
	if len(m.toastVisible) != 3 || len(m.toastQueue) != 1 {
		t.Fatalf("visible=%d queue=%d, want 3/1", len(m.toastVisible), len(m.toastQueue))
	}

	now := time.Now()
	for i := range m.toastVisible {
		m.toastVisible[i].expiresAt = now.Add(-time.Second) // all expired
	}
	if !m.expireToasts(now) {
		t.Error("expireToasts must report a change when toasts expired")
	}
	if len(m.toastVisible) != 1 {
		t.Fatalf("visible after expiry = %d, want 1 (promoted from queue)", len(m.toastVisible))
	}
	promoted := m.toastVisible[0]
	if want := now.Add(toastTTL); promoted.expiresAt.Before(now) || promoted.expiresAt.After(want.Add(time.Second)) {
		t.Errorf("promoted expiry = %v, want a fresh TTL around %v", promoted.expiresAt, want)
	}
}

// TestStaleToastTick_Ignored is the #168 lesson applied to the toast tick: a
// tick scheduled before a dismissal/re-arm carries an older generation and
// must not clear the armed flag that now belongs to the newer in-flight tick
// (which would open the door to duplicate tick chains).
func TestStaleToastTick_Ignored(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	m.notify("first", "")
	m.armToastTick()  // gen=1, armed
	m.dismissToasts() // gen=2, armed=false, visible cleared
	m.notify("second", "")
	m.armToastTick() // gen=3, armed=true (live tick in flight)
	if !m.toastTickArmed {
		t.Fatal("expected a tick to be armed after armToastTick")
	}
	liveGen := m.toastGen

	// The stale (first) tick fires: must be ignored entirely — same gen
	// value the dragResetMsg handler failed to check in #168.
	updated, _ := m.updateInner(toastTickMsg{gen: 1})
	got := updated.(model)
	if !got.toastTickArmed {
		t.Error("stale tick cleared the armed flag of the live tick")
	}
	if got.toastGen != liveGen {
		t.Errorf("stale tick changed toastGen: %d -> %d", liveGen, got.toastGen)
	}
	if len(got.toastVisible) != 1 {
		t.Errorf("stale tick mutated the visible set: %d toasts", len(got.toastVisible))
	}

	// The live tick fires: clears the flag and expires the (now past-deadline) toast.
	got.toastVisible[0].expiresAt = time.Now().Add(-time.Second)
	updated, _ = got.updateInner(toastTickMsg{gen: liveGen})
	got = updated.(model)
	if got.toastTickArmed {
		t.Error("live tick must clear the armed flag")
	}
	if len(got.toastVisible) != 0 {
		t.Errorf("live tick must expire past-deadline toasts, got %d", len(got.toastVisible))
	}
}

// TestDismissToasts_KeepsHistory: interactive dismissal (Ctrl+G) clears what
// is on screen but must not erase the record — /notifications still shows it.
func TestDismissToasts_KeepsHistory(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	m.notify("one", "")
	m.notify("two", "")
	genBefore := m.toastGen

	if !m.dismissToasts() {
		t.Fatal("dismissToasts must report success when toasts are open")
	}
	if len(m.toastVisible) != 0 || len(m.toastQueue) != 0 {
		t.Errorf("dismiss must clear visible+queue, got %d/%d", len(m.toastVisible), len(m.toastQueue))
	}
	if len(m.toastHistory) != 2 {
		t.Errorf("history = %d, want 2 (dismissal keeps history)", len(m.toastHistory))
	}
	if m.toastGen == genBefore {
		t.Error("dismiss must bump toastGen to invalidate in-flight ticks")
	}
	if m.dismissToasts() {
		t.Error("dismiss with nothing open must report false")
	}
}

// TestUpdate_CtrlGDismissesToasts exercises the global keybinding path.
func TestUpdate_CtrlGDismissesToasts(t *testing.T) {
	m := newToastTestModel(t)
	m.notify("a toast to dismiss", "")
	if len(m.toastVisible) != 1 {
		t.Fatalf("setup: visible = %d, want 1", len(m.toastVisible))
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	got := updated.(model)
	if len(got.toastVisible) != 0 {
		t.Errorf("Ctrl+G must dismiss open toasts, got %d", len(got.toastVisible))
	}
	if len(got.toastHistory) != 1 {
		t.Errorf("Ctrl+G must keep history, got %d entries", len(got.toastHistory))
	}
}

// TestView_ToastOverlayFloating pins the rendering contract: the toast (with
// its timestamp, message, slash-command hint, and dismiss affordance) shows
// in the rendered view, is NOT part of the transcript, and — being a pure
// render-time overlay — adds zero layout rows (same line count either way).
func TestView_ToastOverlayFloating(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	baseline := m.View()
	baselineLines := strings.Count(baseline, "\n")

	m.notify("background agents panel: on", "/panel background")
	view := m.View()

	if !strings.Contains(view, "background agents panel: on") {
		t.Errorf("view must contain the toast text, got:\n%s", view)
	}
	if !strings.Contains(view, "/panel background") {
		t.Errorf("view must show the slash-command hint next to the toast, got:\n%s", view)
	}
	if !strings.Contains(view, "ctrl+g dismiss") {
		t.Errorf("view must show the dismiss affordance, got:\n%s", view)
	}
	if !regexp.MustCompile(`\d{2}:\d{2}`).MatchString(view) {
		t.Errorf("view must show the current time on the toast, got:\n%s", view)
	}
	if strings.Contains(m.transcript.String(), "background agents panel: on") {
		t.Errorf("toast text must never enter the transcript, got %q", m.transcript.String())
	}
	if got := strings.Count(view, "\n"); got != baselineLines {
		t.Errorf("toast overlay changed the view height: %d -> %d lines (must consume zero rows)", baselineLines, got)
	}
}

// TestHandleThink_TogglesToastNotTranscript: the #162 exemplar emitter —
// state changes are toasts, the bare query stays in the transcript.
func TestHandleThink_TogglesToastNotTranscript(t *testing.T) {
	m := layoutTestModel(t, 120, 40)

	m2 := m.handleThinkCmd("on")
	if !toastMentions(m2, "reasoning visibility: on") {
		t.Errorf("expected a visibility toast, got %#v", m2.toastVisible)
	}
	if strings.Contains(m2.transcript.String(), "reasoning visibility") {
		t.Errorf("state change must not be appended to the transcript, got %q", m2.transcript.String())
	}

	// Bare query: explicit answer → transcript, no toast.
	m3 := m2.handleThinkCmd("")
	if !strings.Contains(m3.transcript.String(), "reasoning visibility") {
		t.Errorf("bare /think must answer in the transcript, got %q", m3.transcript.String())
	}
	if len(m3.toastVisible) != len(m2.toastVisible) {
		t.Errorf("bare /think must not toast, visible %d -> %d", len(m2.toastVisible), len(m3.toastVisible))
	}
}

// TestAutoOpenPanel_ToastsOnlyOnTransition: ADR-0044 auto-opens fire on every
// workflow chunk — only the closed→open transition may notify, and a panel
// the user already opened manually (panelManualOverride) stays silent.
func TestAutoOpenPanel_ToastsOnlyOnTransition(t *testing.T) {
	m := layoutTestModel(t, 120, 40)

	m.autoOpenPanel(regionBackground)
	if !toastMentions(m, "background agents panel opened") {
		t.Errorf("expected an auto-open toast, got %#v", m.toastVisible)
	}
	firstCount := len(m.toastHistory)

	// Repeated calls while open (every workflow chunk) stay silent.
	m.autoOpenPanel(regionBackground)
	if len(m.toastHistory) != firstCount {
		t.Errorf("auto-open while already open must not toast, history %d -> %d", firstCount, len(m.toastHistory))
	}

	// A panel the user manages manually is never auto-opened at all.
	m2 := layoutTestModel(t, 120, 40)
	m2.panelBackground = true
	m2.autoOpenPanel(regionBackground)
	if len(m2.toastHistory) != 0 {
		t.Errorf("auto-open must respect manual panel state, got %#v", m2.toastHistory)
	}
}

// TestNotificationsCmd: history view (timestamps + hints), clear, and the
// empty case — the /notifications command required by issue #162.
func TestNotificationsCmd(t *testing.T) {
	m := layoutTestModel(t, 120, 40)

	// Empty history.
	updated, _ := m.handleSlashInput("/notifications", "")
	got := updated.(model)
	if !strings.Contains(got.transcript.String(), "no notifications recorded") {
		t.Errorf("empty history response, got %q", got.transcript.String())
	}

	// Record two events, then view.
	m = got
	m.toastHistory = nil
	m.notify("reasoning visibility: on", "/think off")
	m.notify("config reloaded", "/config")
	updated, _ = m.handleSlashInput("/notifications", "")
	got = updated.(model)
	out := got.transcript.String()
	for _, want := range []string{"notification history", "reasoning visibility: on", "/think off", "config reloaded", "/config"} {
		if !strings.Contains(out, want) {
			t.Errorf("history output missing %q, got %q", want, out)
		}
	}
	if !regexp.MustCompile(`\d{2}:\d{2}:\d{2}`).MatchString(out) {
		t.Errorf("history output must carry timestamps, got %q", out)
	}

	// Clear.
	updated, _ = got.handleSlashInput("/notifications", "clear")
	got = updated.(model)
	if len(got.toastHistory) != 0 {
		t.Errorf("clear must empty history, got %d entries", len(got.toastHistory))
	}
	if !strings.Contains(got.transcript.String(), "notification history cleared") {
		t.Errorf("clear must confirm in the transcript, got %q", got.transcript.String())
	}
}

// TestNotificationsRegistration pins the pieces that made #164 (the /clear
// alias) necessary the first time: extraction, completion variants, and help
// text must all know about the command — #162 must not reintroduce the gap.
func TestNotificationsRegistration(t *testing.T) {
	cmd, rest, found := extractSlashCommand("/notifications clear")
	if !found || cmd != "/notifications" || rest != "clear" {
		t.Errorf("extractSlashCommand = (%q, %q, %v), want (/notifications, clear, true)", cmd, rest, found)
	}
	if vs, ok := cmdVariants["/notifications"]; !ok || len(vs) < 2 {
		t.Errorf("cmdVariants[/notifications] = %v, want >= 2 variants (bare + clear)", vs)
	}
	if !strings.Contains(interactiveHelp, "/notifications") {
		t.Error("interactiveHelp must document /notifications")
	}
	if !strings.Contains(interactiveHelp, "Ctrl+G") {
		t.Error("interactiveHelp must document the Ctrl+G dismiss binding")
	}
}

// TestToggleThinking_CtrlTPath: the Ctrl+T handler goes through
// toggleThinking — same toast contract as /think on|off.
func TestToggleThinking_CtrlTPath(t *testing.T) {
	m := layoutTestModel(t, 120, 40)
	m2 := m.toggleThinking()
	if !toastMentions(m2, "reasoning visibility: on") {
		t.Errorf("expected a toast from toggleThinking, got %#v", m2.toastVisible)
	}
	m3 := m2.toggleThinking()
	if !toastMentions(m3, "reasoning visibility: off") {
		t.Errorf("expected the off toast, got %#v", m3.toastVisible)
	}
	if strings.Contains(m3.transcript.String(), "reasoning visibility") {
		t.Errorf("toggle must not write to the transcript, got %q", m3.transcript.String())
	}
}

// TestNewModelToastsOnBackgroundJobDone guards the full Update path including
// the wrapper that arms the expiry tick — a regression here means toasts
// would render but never disappear.
func TestNewModelToastsOnBackgroundJobDone(t *testing.T) {
	m := newToastTestModel(t)
	updated, cmd := m.Update(backgroundJobDoneMsg{job: &local.Job{Label: "x", Result: "y"}})
	got := updated.(model)
	if !toastMentions(got, `background agent "x" completed`) {
		t.Fatalf("expected a completion toast, got %#v", got.toastVisible)
	}
	if !got.toastTickArmed {
		t.Error("Update wrapper must arm the expiry tick while toasts are visible")
	}
	if cmd == nil {
		t.Error("expected a tick Cmd to be batched into the Update result")
	}
	// Error stays transcript material (ADR-0048: warnings/errors persist).
	if strings.Contains(got.transcript.String(), `background agent "x" completed`) {
		t.Error("completion must not be appended to the transcript")
	}
}
