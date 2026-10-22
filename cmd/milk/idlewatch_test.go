package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestWatch builds a watchdog with test-shrunk timings and channel-backed
// callbacks so tests can await events instead of sleeping past them.
func newTestWatch(idle, deadline time.Duration, steerable bool) (w *turnIdleWatch, warned, wrapped, stopped chan string) {
	warned, wrapped, stopped = make(chan string, 8), make(chan string, 8), make(chan string, 8)
	w = newTurnIdleWatch("test-agent", idle, deadline, steerable,
		func() { warned <- "warn" },
		func() { wrapped <- "wrap" },
		func() { stopped <- "stop" })
	w.interval = 2 * time.Millisecond
	w.grace = 15 * time.Millisecond
	return w, warned, wrapped, stopped
}

func await(t *testing.T, ch chan string, what string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

func assertSilent(t *testing.T, ch chan string, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %q", what, v)
	case <-time.After(30 * time.Millisecond):
	}
}

// TestTurnIdleWatch_WarnsOncePerStretch: the advisory warning fires after the
// idle threshold, fires at most once per stretch, and re-arms when activity
// resumes (issue #305's core semantic — stalling is the signal).
func TestTurnIdleWatch_WarnsOncePerStretch(t *testing.T) {
	w, warned, _, _ := newTestWatch(15*time.Millisecond, 0, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	await(t, warned, "first idle warning")
	assertSilent(t, warned, "second warning in the same idle stretch")

	w.Touch() // activity resumes → the stretch ends and re-arms
	await(t, warned, "warning after re-arm")
}

// TestTurnIdleWatch_DeadlineWrapUpThenHardStop: the opt-in deadline requests a
// wrap-up exactly once; a steerable turn that produces nothing afterwards is
// hard-stopped after the grace period.
func TestTurnIdleWatch_DeadlineWrapUpThenHardStop(t *testing.T) {
	w, warned, wrapped, stopped := newTestWatch(0, 10*time.Millisecond, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	await(t, wrapped, "wrap-up at the deadline")
	assertSilent(t, wrapped, "second wrap-up request")
	await(t, stopped, "hard stop after grace with no activity since wrap-up")
	assertSilent(t, stopped, "second hard stop")
	assertSilent(t, warned, "idle warning with idle disabled")
}

// TestTurnIdleWatch_ActiveWrapUpIsNeverKilled: a steerable turn that is
// writing its closing summary (activity after the wrap-up request) is never
// hard-stopped — the graceful summary always finishes.
func TestTurnIdleWatch_ActiveWrapUpIsNeverKilled(t *testing.T) {
	w, _, wrapped, stopped := newTestWatch(0, 10*time.Millisecond, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	await(t, wrapped, "wrap-up at the deadline")
	w.Touch() // the closing summary is streaming
	assertSilent(t, stopped, "hard stop while the wrap-up is actively writing")
}

// TestTurnIdleWatch_UnsteerableStopsEvenWhenActive: a CLI/subprocess turn
// cannot be told to wrap up, so the deadline is enforced unconditionally —
// the only way it bounds anything.
func TestTurnIdleWatch_UnsteerableStopsEvenWhenActive(t *testing.T) {
	w, _, wrapped, stopped := newTestWatch(0, 10*time.Millisecond, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	await(t, wrapped, "wrap-up notice at the deadline")
	w.Touch() // still streaming — ignored for unsteerable turns
	await(t, stopped, "hard stop at deadline+grace")
}

// TestTurnIdleWatch_NoDeadlineNeverStops: with the deadline off (the default)
// the watchdog only ever warns — it never ends a turn (regression guard for
// the old "turn timeout" confusion: this thing is advisory).
func TestTurnIdleWatch_NoDeadlineNeverStops(t *testing.T) {
	w, warned, wrapped, stopped := newTestWatch(10*time.Millisecond, 0, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	await(t, warned, "idle warning")
	assertSilent(t, wrapped, "wrap-up with no deadline")
	assertSilent(t, stopped, "hard stop with no deadline")
}

// TestTouchWriter_TicksOnWrite: everything the agent streams to the host is
// liveness (streamed tokens, tool rows, notices).
func TestTouchWriter_TicksOnWrite(t *testing.T) {
	var n atomic.Int32
	var sb strings.Builder
	tw := &touchWriter{w: &sb, touch: func() { n.Add(1) }}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("touch count = %d, want 1 per write", got)
	}
	if sb.String() != "hello" {
		t.Errorf("passthrough = %q, want %q", sb.String(), "hello")
	}
}
