package main

import (
	"context"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/config"
	"github.com/scoutme/milk/internal/oversight"
)

// TestAskPermission_RemoteStickyAnswerSetsMode pins the remote-oversight
// sticky path (issue #302): an "answer all the same way" reply sets the
// session mode through the policy's sticky hook, announces it, and answers
// this ask accordingly.
func TestAskPermission_RemoteStickyAnswerSetsMode(t *testing.T) {
	cases := []struct {
		dec      oversight.PermDecision
		want     bool
		wantMode sessionPermMode
	}{
		{oversight.PermAllowAlways, true, sessionPermAllowAll},
		{oversight.PermDenyAlways, false, sessionPermDenyAll},
	}
	for _, c := range cases {
		n := &fakeNotifier{
			permCh:  make(chan oversight.PermDecision, 1),
			inputCh: make(chan string, 1),
		}
		n.permCh <- c.dec
		st := &interactiveState{}
		var notes []string
		p := foregroundPermPolicy(permOverrides{})
		p.sticky = stickySetter(st.setSessionPermMode, func(s string) { notes = append(notes, s) })

		allow, src := askPermission(context.Background(), n,
			oversight.PermRequest{ToolName: "write_file"}, nil, p)
		if allow != c.want || src != permSrcRemote {
			t.Errorf("askPermission = (%v, %s), want (%v, direct… remote)", allow, src, c.want)
		}
		if got := st.permOverridesNow().sessionMode; got != c.wantMode {
			t.Errorf("session mode = %v, want %v", got, c.wantMode)
		}
		if len(notes) != 1 || !strings.Contains(notes[0], c.wantMode.label()) {
			t.Errorf("want one announcement naming the mode, got %v", notes)
		}
	}
}

// TestAskPermission_SafetyNeverSticky pins the doom-loop gate's exemption:
// the safety policy offers no sticky answer, an always-flavored reply is
// treated as its plain value, and the session mode is never touched.
func TestAskPermission_SafetyNeverSticky(t *testing.T) {
	n := &fakeNotifier{
		permCh:  make(chan oversight.PermDecision, 1),
		inputCh: make(chan string, 1),
	}
	n.permCh <- oversight.PermAllowAlways
	st := &interactiveState{}
	p := safetyAskPolicy(config.Config{}, permOverrides{})
	if p.sticky != nil {
		t.Fatal("safety policy must not offer sticky answers")
	}
	allow, _ := askPermission(context.Background(), n,
		oversight.PermRequest{ToolName: "doom_loop"}, nil, p)
	if !allow {
		t.Error("an always-flavored reply must still allow its own ask")
	}
	if st.permOverridesNow().sessionMode.decided() {
		t.Error("a safety answer must never set the session mode")
	}
}
