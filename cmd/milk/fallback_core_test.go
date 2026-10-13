package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/events"
	"github.com/scoutme/milk/internal/oversight"
	"github.com/scoutme/milk/internal/router"
)

// askHost answers permission requests with a fixed outcome and records them.
type askHost struct {
	events.Host
	allow bool
	reqs  []events.PermissionRequest
}

func (h *askHost) RequestPermission(_ context.Context, r events.PermissionRequest) (events.PermissionOutcome, error) {
	h.reqs = append(h.reqs, r)
	return events.PermissionOutcome{Allow: h.allow}, nil
}

// primaryDownRoute routes a turn with the primary unavailable and Claude
// installed, the case that used to fall back silently.
func primaryDownRoute(t *testing.T, st *interactiveState) routedTurn {
	t.Helper()
	st.forcePrimary = true // the router must pick the primary for this to be a fallback
	rt, err := routeTurn(context.Background(), st, router.New(st.cfg, nil), "x", false, true)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestRouteTurn_ImplicitClaudeFallbackNeedsConsent(t *testing.T) {
	st := routingState(t)
	rt := primaryDownRoute(t, st)
	if rt.Fallback != "escalation" || !rt.NeedsFallbackConsent {
		t.Errorf("implicit Claude fallback must need consent: %+v", rt)
	}

	st.cfg = config.Config{EscalationAgent: "claude"}
	if rt = primaryDownRoute(t, st); rt.NeedsFallbackConsent {
		t.Error("an explicitly configured escalation agent is a defined fallback, not a consent case")
	}

	st.cfg = config.Config{Agents: []config.AgentConfig{{Name: "big", URL: "http://x", Model: "m"}}, EscalationAgent: "big"}
	if rt = primaryDownRoute(t, st); rt.NeedsFallbackConsent {
		t.Error("a user-named escalation agent needs no consent")
	}
}

func TestRouteTurn_ExplicitEscalateNeedsNoConsent(t *testing.T) {
	st := routingState(t)
	st.forceEscalate = true
	rt, _ := routeTurn(context.Background(), st, router.New(st.cfg, nil), "x", false, true)
	if rt.Target != router.TargetEscalation || rt.NeedsFallbackConsent {
		t.Errorf("/escalate is the user's own choice: %+v", rt)
	}
}

func TestRouteTurn_NoAgentWhenBothDown(t *testing.T) {
	st := routingState(t)
	rt, _ := routeTurn(context.Background(), st, router.New(st.cfg, nil), "x", false, false)
	if !rt.NoAgent {
		t.Fatalf("both down must be NoAgent: %+v", rt)
	}
	err := gateFallback(context.Background(), st, rt, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "neither the primary agent") || !strings.Contains(err.Error(), "Claude Code was not found") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestGateFallback_AskAndRemember(t *testing.T) {
	st := routingState(t)
	host := &askHost{allow: true}

	rt := primaryDownRoute(t, st)
	if err := gateFallback(context.Background(), st, rt, host, oversight.Noop{}); err != nil {
		t.Fatalf("approved fallback must run: %v", err)
	}
	if len(host.reqs) != 1 || !host.reqs[0].DefaultNo || !strings.Contains(host.reqs[0].Prompt, "no escalation agent is configured") ||
		!strings.Contains(host.reqs[0].Prompt, "Claude Code is available") {
		t.Errorf("unexpected question: %+v", host.reqs)
	}

	// Same outage: no second question.
	rt = primaryDownRoute(t, st)
	if rt.NeedsFallbackConsent {
		t.Error("an approved outage must not ask again")
	}

	// Primary answers again, then goes down once more: ask again.
	if _, err := routeTurn(context.Background(), st, router.New(st.cfg, nil), "x", true, true); err != nil {
		t.Fatal(err)
	}
	if rt = primaryDownRoute(t, st); !rt.NeedsFallbackConsent {
		t.Error("a new outage must ask again")
	}
}

func TestGateFallback_DeclineDoesNotRunOrRemember(t *testing.T) {
	st := routingState(t)
	host := &askHost{allow: false}
	err := gateFallback(context.Background(), st, primaryDownRoute(t, st), host, oversight.Noop{})
	if !errors.Is(err, errFallbackDeclined) {
		t.Fatalf("want errFallbackDeclined, got %v", err)
	}
	if st.claudeFallbackApproved {
		t.Error("a decline must not be remembered as approval")
	}
}

func TestGateFallback_NoSurfaceMeansNo(t *testing.T) {
	st := routingState(t)
	if err := gateFallback(context.Background(), st, primaryDownRoute(t, st), nil, nil); !errors.Is(err, errFallbackDeclined) {
		t.Errorf("with nobody to ask the answer is no, got %v", err)
	}
}

func TestSinglePromptFallbackGate(t *testing.T) {
	cfg := config.Config{}
	var errOut bytes.Buffer

	if err := singlePromptFallbackGate(cfg, router.TargetLocal, router.TargetLocal, true, strings.NewReader(""), &errOut); err != nil {
		t.Errorf("no fallback happened: %v", err)
	}
	if err := singlePromptFallbackGate(cfg, router.TargetEscalation, router.TargetEscalation, false, strings.NewReader(""), &errOut); err != nil {
		t.Errorf("an explicit escalation is not a fallback: %v", err)
	}
	if err := singlePromptFallbackGate(config.Config{EscalationAgent: "claude"}, router.TargetLocal, router.TargetEscalation, false, strings.NewReader(""), &errOut); err != nil {
		t.Errorf("a configured escalation agent needs no consent: %v", err)
	}

	err := singlePromptFallbackGate(cfg, router.TargetLocal, router.TargetEscalation, false, strings.NewReader(""), &errOut)
	if err == nil || !strings.Contains(err.Error(), "--escalate") {
		t.Errorf("non-interactive must refuse and say how to proceed: %v", err)
	}

	errOut.Reset()
	if err := singlePromptFallbackGate(cfg, router.TargetLocal, router.TargetEscalation, true, strings.NewReader("y\n"), &errOut); err != nil {
		t.Errorf("approved: %v", err)
	}
	if !strings.Contains(errOut.String(), "[y/N]") {
		t.Errorf("question not shown: %q", errOut.String())
	}
	for _, in := range []string{"\n", "n\n", ""} {
		if err := singlePromptFallbackGate(cfg, router.TargetLocal, router.TargetEscalation, true, strings.NewReader(in), &errOut); !errors.Is(err, errFallbackDeclined) {
			t.Errorf("answer %q must decline, got %v", in, err)
		}
	}
}

// An approved fallback covers the outage, not the session: it must not make
// the escalation agent sticky, or Claude would keep getting turns after the
// primary is back.
func TestNoteTurnSucceeded_ImplicitFallbackIsNotSticky(t *testing.T) {
	st := routingState(t)
	rt := primaryDownRoute(t, st)
	noteTurnSucceeded(st, rt)
	if st.autoStickyEscalate {
		t.Error("implicit Claude fallback became auto-sticky")
	}

	st.cfg = config.Config{EscalationAgent: "claude"}
	rt = primaryDownRoute(t, st)
	noteTurnSucceeded(st, rt)
	if !st.autoStickyEscalate {
		t.Error("a configured escalation agent keeps the existing sticky behavior")
	}
}
