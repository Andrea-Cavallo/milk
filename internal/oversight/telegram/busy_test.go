package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scoutme/milk/internal/oversight"
)

// busyAPIServer fakes the Bot API for the busy indicator: it records
// sendChatAction timestamps and sendMessage payloads, and can rate-limit
// the first chat action with a 429 (retry_after) response.
type busyAPIServer struct {
	mu             sync.Mutex
	actions        []time.Time
	sent           []string
	chatCalls      int
	rateLimitFirst bool
	srv            *httptest.Server
}

func newBusyServer(rateLimitFirst bool) *busyAPIServer {
	s := &busyAPIServer{rateLimitFirst: rateLimitFirst}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
			s.mu.Lock()
			s.chatCalls++
			n := s.chatCalls
			s.actions = append(s.actions, time.Now())
			limited := s.rateLimitFirst && n == 1
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if limited {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": false, "error_code": 429, "description": "Too Many Requests",
					"parameters": map[string]any{"retry_after": 1},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var payload struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			s.mu.Lock()
			s.sent = append(s.sent, payload.Text)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	return s
}

func (s *busyAPIServer) actionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.actions)
}

func (s *busyAPIServer) actionTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.actions...)
}

func (s *busyAPIServer) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func (s *busyAPIServer) close() { s.srv.Close() }

func newBusyNotifier(s *busyAPIServer, interval time.Duration) *Notifier {
	return New(Config{
		ChatID:        1,
		APIBase:       s.srv.URL,
		PermTimeout:   400 * time.Millisecond,
		TimeoutAction: "deny",
		BusyIndicator: true,
		BusyInterval:  interval,
	})
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// graceStable waits out any in-flight send, then reports whether the action
// count stopped growing.
func countStable(s *busyAPIServer, grace, window time.Duration) (int, bool) {
	time.Sleep(grace)
	before := s.actionCount()
	time.Sleep(window)
	return before, s.actionCount() == before
}

func TestBusyIndicatorTurnLifecycle(t *testing.T) {
	s := newBusyServer(false)
	defer s.close()
	n := newBusyNotifier(s, 60*time.Millisecond)

	n.NotifyTurnStart(context.Background(), "mimo", "local", "do the thing")
	waitFor(t, 1500*time.Millisecond, "typing actions to repeat", func() bool {
		return s.actionCount() >= 2
	})

	n.NotifyTurnDone(context.Background(), "mimo", nil)
	if _, stable := countStable(s, 120*time.Millisecond, 300*time.Millisecond); !stable {
		t.Fatalf("typing actions kept firing after NotifyTurnDone (count %d)", s.actionCount())
	}

	// A later turn must re-engage.
	baseline := s.actionCount()
	n.NotifyTurnStart(context.Background(), "mimo", "local", "again")
	waitFor(t, 1500*time.Millisecond, "typing to resume on a new turn", func() bool {
		return s.actionCount() > baseline
	})
	n.NotifyTurnDone(context.Background(), "mimo", nil)
}

func TestBusyIndicatorDisabled(t *testing.T) {
	s := newBusyServer(false)
	defer s.close()
	n := New(Config{
		ChatID:        1,
		APIBase:       s.srv.URL,
		BusyIndicator: false, // config: remote_oversight.busy_indicator=false
		BusyInterval:  40 * time.Millisecond,
	})
	n.NotifyTurnStart(context.Background(), "mimo", "local", "go")
	time.Sleep(200 * time.Millisecond)
	n.NotifyTurnDone(context.Background(), "mimo", nil)
	if got := s.actionCount(); got != 0 {
		t.Fatalf("disabled busy indicator sent %d chat actions, want 0", got)
	}
}

func TestAnnounceLeavesNoBusyIndicator(t *testing.T) {
	s := newBusyServer(false)
	defer s.close()
	n := newBusyNotifier(s, 50*time.Millisecond)

	// Announce is what startup/setup notices use: same 🔄 message, but no
	// turn follows it — nothing may ever start typing.
	n.Announce("milk", "startup", "oversight active")
	waitFor(t, 1500*time.Millisecond, "the announcement message", func() bool {
		return len(s.messages()) == 1 && strings.HasPrefix(s.messages()[0], "🔄")
	})
	time.Sleep(250 * time.Millisecond)
	if got := s.actionCount(); got != 0 {
		t.Fatalf("Announce engaged the busy indicator: %d chat actions", got)
	}
}

func TestBusyIndicatorOverlappingTurns(t *testing.T) {
	s := newBusyServer(false)
	defer s.close()
	n := newBusyNotifier(s, 60*time.Millisecond)

	n.NotifyTurnStart(context.Background(), "a", "local", "turn one")
	n.NotifyTurnStart(context.Background(), "b", "local", "turn two")
	waitFor(t, 1500*time.Millisecond, "typing actions", func() bool {
		return s.actionCount() >= 2
	})

	// First turn done: a second one is still in flight — typing continues.
	n.NotifyTurnDone(context.Background(), "a", nil)
	if _, stable := countStable(s, 0, 250*time.Millisecond); stable {
		t.Fatalf("busy loop stopped with one turn still running (count %d)", s.actionCount())
	}

	// Last turn done: it stops.
	n.NotifyTurnDone(context.Background(), "b", nil)
	if _, stable := countStable(s, 120*time.Millisecond, 300*time.Millisecond); !stable {
		t.Fatalf("typing actions kept firing after the last NotifyTurnDone")
	}

	// Unbalanced stop calls must not go negative and later turns still work.
	n.NotifyTurnDone(context.Background(), "ghost", nil)
	baseline := s.actionCount()
	n.NotifyTurnStart(context.Background(), "c", "local", "turn three")
	waitFor(t, 1500*time.Millisecond, "typing after an unbalanced stop", func() bool {
		return s.actionCount() > baseline
	})
	n.NotifyTurnDone(context.Background(), "c", nil)
}

// TestBusyIndicatorPausesDuringAsks pins the honesty rule: while milk waits
// for the human's remote reply the agent isn't working, so no typing…
// (both ask kinds block ~PermTimeout with no reply scripted here).
func TestBusyIndicatorPausesDuringAsks(t *testing.T) {
	cases := []struct {
		name string
		ask  func(n *Notifier)
	}{
		{"AskPermission", func(n *Notifier) {
			_, _ = n.AskPermission(context.Background(), oversight.PermRequest{ToolName: "bash", Input: "rm -rf /tmp/x"})
		}},
		{"AskInput", func(n *Notifier) {
			_, _ = n.AskInput(context.Background(), oversight.InputRequest{Question: "which one?"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newBusyServer(false)
			defer s.close()
			n := newBusyNotifier(s, 50*time.Millisecond)

			n.NotifyTurnStart(context.Background(), "mimo", "local", "work")
			waitFor(t, 1500*time.Millisecond, "typing before the ask", func() bool {
				return s.actionCount() >= 1
			})
			time.Sleep(60 * time.Millisecond) // settle away from the t0 tick

			askStart := time.Now()
			tc.ask(n)
			askEnd := time.Now()

			// No sends may land well inside the ask window (one straggler
			// already in flight at the pause boundary is tolerated).
			inWindow := 0
			lo := askStart.Add(70 * time.Millisecond)
			hi := askEnd.Add(-40 * time.Millisecond)
			for _, at := range s.actionTimes() {
				if at.After(lo) && at.Before(hi) {
					inWindow++
				}
			}
			if inWindow > 1 {
				t.Fatalf("busy indicator kept typing during the ask: %d actions in (%v..%v)", inWindow, lo.Sub(askStart), askEnd.Sub(askStart))
			}

			// After the answer times out and the ask returns, typing resumes.
			postAsk := s.actionCount()
			waitFor(t, 1500*time.Millisecond, "typing to resume after the ask", func() bool {
				return s.actionCount() > postAsk
			})
			n.NotifyTurnDone(context.Background(), "mimo", nil)
		})
	}
}

func TestBusyIndicator429HonorsRetryAfter(t *testing.T) {
	s := newBusyServer(true)
	defer s.close()
	n := newBusyNotifier(s, 60*time.Millisecond)

	n.NotifyTurnStart(context.Background(), "mimo", "local", "go")
	waitFor(t, 3*time.Second, "the post-429 action", func() bool {
		return s.actionCount() >= 2
	})
	times := s.actionTimes()
	if len(times) < 2 {
		t.Fatalf("expected at least 2 actions, got %d", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 900*time.Millisecond {
		t.Fatalf("429 retry_after ignored: second action only %v after the first (want ≥ ~1s)", gap)
	}
	n.NotifyTurnDone(context.Background(), "mimo", nil)
}
