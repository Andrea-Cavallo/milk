package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// turnIdleWatch is the shared idle watchdog for one dispatched turn (issue
// #305). It replaces the old fixed one-shot turn timer with the workflow
// watchdog's semantics: the clock resets on real activity (streamed output,
// tool start/end, retries), the advisory warning fires once per idle stretch
// and re-arms when work resumes — stalling is the signal, being slow is not.
// Used by the TUI's runTurn and ACP's acpSession.runTurn with identical
// semantics (the workflow's tea.Tick variant in repl.go keeps its own
// msg-based implementation of the same pattern).
//
// The opt-in turn deadline (limits.turn_deadline_secs, default off) layers on
// top: at the deadline the turn is asked to wrap up with a forced closing
// summary, and hard-stopped only if it will not end (see Start).
type turnIdleWatch struct {
	agentName string
	// idle is the inactivity threshold for the advisory warning; 0 disables
	// the warning (limits.turn_timeout_secs: -1).
	idle time.Duration
	// deadline is the opt-in wall-clock turn deadline; 0 means off.
	deadline time.Duration
	// steerable says the turn runs milk's own tool loop (local.Agent), which
	// can be told to wrap up with a forced summary at its next iteration
	// boundary. External process runners (claude-cli, subprocess) cannot —
	// their deadline is enforced by the hard stop alone.
	steerable bool
	// grace is how long after the deadline the hard stop fires. Steerable
	// turns only hard-stop if nothing happened since the wrap-up request
	// (an actively-writing summary is never killed); unsteerable turns stop
	// unconditionally — it is the only way their deadline bounds anything.
	grace    time.Duration
	interval time.Duration // check granularity (tests shrink it)

	onWarn     func() // once per idle stretch
	onWrapUp   func() // once, at the deadline (steerable: the wrap-up signal)
	onHardStop func() // once, at deadline+grace

	last     atomic.Int64 // unix nanos of last activity
	warned   atomic.Bool  // one warning per idle stretch
	wrapAt   atomic.Int64 // unix nanos when the wrap-up fired (0 = not yet)
	stopped  atomic.Bool  // hard stop fired
	closeOne sync.Once
	done     chan struct{}
}

// turnIdleCheckInterval is how often a running turn's watchdog checks elapsed
// time since the last activity event.
const turnIdleCheckInterval = 15 * time.Second

// turnDeadlineGrace bounds how long a wrapped-up turn may keep running past
// its deadline before the hard stop. Generous: one more slow model round must
// fit inside it.
const turnDeadlineGrace = 2 * time.Minute

func newTurnIdleWatch(agentName string, idle, deadline time.Duration, steerable bool, onWarn, onWrapUp, onHardStop func()) *turnIdleWatch {
	return &turnIdleWatch{
		agentName:  agentName,
		idle:       idle,
		deadline:   deadline,
		steerable:  steerable,
		grace:      turnDeadlineGrace,
		interval:   turnIdleCheckInterval,
		onWarn:     onWarn,
		onWrapUp:   onWrapUp,
		onHardStop: onHardStop,
		done:       make(chan struct{}),
	}
}

// Touch records activity: the idle clock restarts and a previously warned
// stretch re-arms. Safe from any goroutine (stream writers, tool callbacks).
func (w *turnIdleWatch) Touch() {
	w.last.Store(time.Now().UnixNano())
	w.warned.Store(false)
}

// Close stops the watchdog's tick loop (the turn ending also stops it via
// ctx; this makes shutdown deterministic for tests and quick teardown).
func (w *turnIdleWatch) Close() {
	w.closeOne.Do(func() { close(w.done) })
}

// Start runs the watchdog until ctx ends. One goroutine per turn.
func (w *turnIdleWatch) Start(ctx context.Context) {
	defer w.Close()
	start := time.Now()
	if w.last.Load() == 0 {
		w.Touch()
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.check(ctx, start)
		}
	}
}

func (w *turnIdleWatch) check(ctx context.Context, start time.Time) {
	now := time.Now()

	// Advisory idle warning: once per idle stretch, re-armed by Touch.
	if w.idle > 0 && !w.warned.Load() {
		last := time.Unix(0, w.last.Load())
		if now.Sub(last) >= w.idle {
			if w.warned.CompareAndSwap(false, true) && w.onWarn != nil {
				w.onWarn()
			}
		}
	}

	if w.deadline <= 0 {
		return
	}
	// Graceful wrap-up request at the deadline (once).
	if w.wrapAt.Load() == 0 && now.Sub(start) >= w.deadline {
		if w.wrapAt.CompareAndSwap(0, now.UnixNano()) && w.onWrapUp != nil {
			w.onWrapUp()
		}
		return
	}
	// Hard stop after deadline+grace.
	wrapAt := w.wrapAt.Load()
	if wrapAt == 0 || now.Sub(start) < w.deadline+w.grace || w.stopped.Load() {
		return
	}
	if w.steerable {
		// A steerable turn is wrapping up: kill it only if the wrap-up
		// request produced nothing at all (an actively-writing closing
		// summary always finishes).
		if w.last.Load() > wrapAt {
			return
		}
	}
	if w.stopped.CompareAndSwap(false, true) && w.onHardStop != nil {
		w.onHardStop()
	}
	_ = ctx // reserved: stop semantics are the caller's (it owns the cancel)
}

// touchWriter forwards writes and ticks the watchdog on each one — the
// single choke point for everything the agent streams to the host (text
// deltas, tool rows, notices).
type touchWriter struct {
	w     io.Writer
	touch func()
}

func (a *touchWriter) Write(p []byte) (int, error) {
	a.touch()
	return a.w.Write(p)
}

// turnWatchWarnText / turnWatchWrapUpText / turnWatchStopText are the shared
// wordings for both hosts' surfaces (TUI transcript line, ACP notice chunk).
func turnWatchWarnText(agentName string, idle time.Duration) string {
	return fmt.Sprintf("[%s — no activity for %s; turn still running, Ctrl+C (or /interrupt) to cancel]", agentName, roundDuration(idle))
}

func turnWatchWrapUpText(agentName string, deadline time.Duration) string {
	return fmt.Sprintf("[%s — turn deadline (%s) reached; asking for a closing summary]", agentName, roundDuration(deadline))
}

func turnWatchStopText(agentName string) string {
	return fmt.Sprintf("[%s — turn did not wrap up after its deadline; stopping it]", agentName)
}

// roundDuration renders a duration for the notice texts (drop noise below s).
func roundDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return (d.Round(time.Second)).String()
}
