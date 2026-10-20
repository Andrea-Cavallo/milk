package local

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Unit pins for bashKilledMessage (issue #275): the configured limit must
// never be presented as the time a run actually lasted.
func TestBashKilledMessage_DistinguishesElapsedFromLimit(t *testing.T) {
	limit := 5 * time.Minute

	// Cancelled parent (turn cancel): external kill → elapsed reported.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	own := time.Now().Add(5 * time.Minute)
	msg := bashKilledMessage(ctx, time.Now().Add(-2*time.Second), own, limit)
	if want := "bash: command killed by context after 2s (limit 5m0s)"; msg != want {
		t.Errorf("cancelled: got %q, want %q", msg, want)
	}

	// Own configured timeout consumed → plain wording, elapsed IS the limit.
	tctx, tcancel := context.WithTimeout(context.Background(), -time.Millisecond)
	defer tcancel()
	<-tctx.Done()
	msg = bashKilledMessage(tctx, time.Now(), time.Now().Add(-time.Millisecond), limit)
	if want := "bash: command timed out after 5m0s"; msg != want {
		t.Errorf("own timeout: got %q, want %q", msg, want)
	}
}

var killedRe = regexp.MustCompile(`^bash: command killed by context after [0-9.]+(ms|s|m) \(limit 2m0s\)$`)

// End-to-end: cancelling the caller's context mid-run reports how long the
// command really ran, not the configured default (2m0s).
func TestRunBash_ExternalCancelReportsElapsed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		res, _ := runBash(ctx, map[string]any{"command": "sleep 30"}, "")
		done <- res
	}()
	time.Sleep(120 * time.Millisecond)
	cancel()
	res := <-done
	if !strings.Contains(res, `"exit_code":-1`) {
		t.Fatalf("killed bash must fail with exit -1, got %q", res)
	}
	// The message names a short elapsed time and the limit separately —
	// never the limit as the duration.
	if !killedRe.MatchString(errorText(t, res)) {
		t.Errorf("message must be 'killed by context after <elapsed> (limit 2m0s)', got %q", res)
	}
	if strings.Contains(errorText(t, res), "after 2m0s") {
		t.Errorf("elapsed must not equal the configured limit: %q", res)
	}
}

// End-to-end: a parent deadline (the background-job hard-deadline shape —
// ctx done before our own bash timeout) reports elapsed, not the limit.
func TestRunBash_ExternalDeadlineReportsElapsed(t *testing.T) {
	pctx, pcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer pcancel()
	res, _ := runBash(pctx, map[string]any{"command": "sleep 30"}, "")
	if !killedRe.MatchString(errorText(t, res)) {
		t.Errorf("parent-deadline kill must report elapsed (limit 2m0s), got %q", res)
	}
	if strings.Contains(errorText(t, res), "after 2m0s") {
		t.Errorf("elapsed must not equal the configured limit: %q", res)
	}
}

// The plain wording survives for a run that consumed its own timeout.
func TestRunBash_OwnTimeoutKeepsPlainWording(t *testing.T) {
	res, _ := runBash(context.Background(), map[string]any{
		"command": "sleep 5", "timeout_seconds": 0.3,
	}, "")
	if !strings.Contains(errorText(t, res), "bash: command timed out after 300ms") {
		t.Errorf("own-timeout wording changed: %q", res)
	}
	if !strings.Contains(res, `"exit_code":-1`) {
		t.Errorf("timeout must fail with exit -1, got %q", res)
	}
}

// errorText pulls the "error" field out of a toolResult JSON string.
func errorText(t *testing.T, res string) string {
	t.Helper()
	m := regexp.MustCompile(`"error":"((?:[^"\\]|\\.)*)"`).FindStringSubmatch(res)
	if m == nil {
		t.Fatalf("no error field in result %q", res)
	}
	s := strings.ReplaceAll(m[1], `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}
