package main

import (
	"context"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/router"
	"github.com/scoutme/milk/internal/session"
)

func routingState(t *testing.T) *interactiveState {
	t.Helper()
	return &interactiveState{sess: &session.Session{CWD: t.TempDir()}, cwd: t.TempDir(), cfg: config.Config{}}
}

func TestRouteTurn_ConsumesSingleTurnFlagsButKeepsPins(t *testing.T) {
	st := routingState(t)
	rtr := router.New(st.cfg, nil)

	st.forceEscalate = true
	rt, err := routeTurn(context.Background(), st, rtr, "x", true, true)
	if err != nil || rt.Target != router.TargetEscalation {
		t.Fatalf("forceEscalate: target=%v err=%v", rt.Target, err)
	}
	if st.forceEscalate {
		t.Error("single-turn forceEscalate was not consumed")
	}

	st.stickyEscalate = true
	for i := 0; i < 2; i++ {
		if rt, _ = routeTurn(context.Background(), st, rtr, "x", true, true); rt.Target != router.TargetEscalation {
			t.Fatalf("pinned turn %d routed to %v", i, rt.Target)
		}
	}
	if !st.stickyEscalate {
		t.Error("pin was cleared by routing")
	}
}

func TestRouteTurn_FallsBackWhenTargetUnavailable(t *testing.T) {
	st := routingState(t)
	rtr := router.New(st.cfg, nil)

	st.forceEscalate = true
	rt, _ := routeTurn(context.Background(), st, rtr, "x", true, false)
	if rt.Target != router.TargetLocal || rt.Fallback != "primary" {
		t.Errorf("escalation unavailable: target=%v fallback=%q", rt.Target, rt.Fallback)
	}

	st.forcePrimary = true
	rt, _ = routeTurn(context.Background(), st, rtr, "x", false, true)
	if rt.Target != router.TargetEscalation || rt.Fallback != "escalation" {
		t.Errorf("primary unavailable: target=%v fallback=%q", rt.Target, rt.Fallback)
	}

	st.forceEscalate = true
	if rt, _ = routeTurn(context.Background(), st, rtr, "x", true, true); rt.Fallback != "" {
		t.Errorf("no fallback expected, got %q", rt.Fallback)
	}
}

func TestAutoSticky_KeepsLaterTurnsOnEscalationUntilPrimaryOverride(t *testing.T) {
	st := routingState(t)
	rtr := router.New(st.cfg, nil)

	// The router sends a turn to escalation and it succeeds: later turns stay there.
	noteTurnSucceeded(st, router.TargetEscalation)
	if !st.autoStickyEscalate {
		t.Fatal("auto-sticky not set after a successful escalation turn")
	}
	if rt, _ := routeTurn(context.Background(), st, rtr, "next", true, true); rt.Target != router.TargetEscalation {
		t.Errorf("sticky session routed to %v, want escalation", rt.Target)
	}

	// A single-turn /primary override breaks it.
	st.forcePrimary = true
	routeTurn(context.Background(), st, rtr, "back", true, true) //nolint:errcheck
	if st.autoStickyEscalate {
		t.Error("a forcePrimary turn should clear auto-sticky")
	}
}

func TestNoteTurnSucceeded_Conditions(t *testing.T) {
	cases := []struct {
		name   string
		target router.Target
		mutate func(*interactiveState)
		want   bool
	}{
		{"router escalation", router.TargetEscalation, func(*interactiveState) {}, true},
		{"local turn", router.TargetLocal, func(*interactiveState) {}, false},
		{"explicitly pinned already", router.TargetEscalation, func(st *interactiveState) { st.stickyEscalate = true }, false},
		{"disabled in config", router.TargetEscalation, func(st *interactiveState) {
			off := false
			st.cfg.StickyEscalation = &off
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := routingState(t)
			c.mutate(st)
			noteTurnSucceeded(st, c.target)
			if st.autoStickyEscalate != c.want {
				t.Errorf("autoStickyEscalate = %v, want %v", st.autoStickyEscalate, c.want)
			}
		})
	}
}

func TestTurnSource(t *testing.T) {
	st := routingState(t)
	if got := turnSource(st); got != "auto" {
		t.Errorf("default = %q", got)
	}
	st.autoStickyEscalate = true
	if got := turnSource(st); got != "auto_sticky" {
		t.Errorf("auto sticky = %q", got)
	}
	st.stickyPrimary = true
	if got := turnSource(st); got != "user" {
		t.Errorf("pinned = %q", got)
	}
}
