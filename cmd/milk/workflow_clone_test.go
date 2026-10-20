package main

import (
	"context"
	"sync"
	"testing"

	"github.com/scoutme/milk/internal/agent/local"
	"github.com/scoutme/milk/internal/oversight"
)

// Issue #297: CloneTurnRunner gives every parallel_group item an adapter of
// its own — independent session ID, nonce, attachment-injection and
// use-memory flags — while the original stays untouched. Two clones then run
// concurrently (as items do) writing their own state: under -race that is
// clean only if the per-call state really is per-clone.
func TestWorkflowTurnRunner_CloneIndependentState(t *testing.T) {
	inner := &fakeTurnRunner{newSessionID: "s-item"}
	wtr := &workflowTurnRunner{
		inner:     inner,
		role:      RoleWorkflow,
		roleName:  "worker",
		notifier:  oversight.Noop{},
		sessionID: "", // a fresh adapter, as buildWorkflowRunners makes
		attachments: []PendingAttachment{
			{Path: "/tmp/p.png", Name: "p.png", MIMEType: "image/png", Data: []byte{1}},
		},
	}

	clone1, ok := wtr.CloneTurnRunner().(*workflowTurnRunner)
	if !ok {
		t.Fatal("CloneTurnRunner must return *workflowTurnRunner")
	}
	clone2, _ := wtr.CloneTurnRunner().(*workflowTurnRunner)
	if clone1 == wtr || clone2 == wtr || clone1 == clone2 {
		t.Fatal("each clone must be its own adapter instance")
	}
	if clone1.sess == wtr.sess || clone1.sess == clone2.sess {
		t.Error("each adapter needs its own scratch session")
	}
	if clone1.sessionID != "" || clone1.attachmentsInjected || clone1.nonce != "" || clone1.useMemoryNextCall {
		t.Errorf("per-call state must start fresh on a clone: sid=%q injected=%v nonce=%q mem=%v",
			clone1.sessionID, clone1.attachmentsInjected, clone1.nonce, clone1.useMemoryNextCall)
	}

	// Concurrent item runs — each writing its own sessionID/attachment flag
	// and carrying its own ctx parts. The shared original must not move.
	var wg sync.WaitGroup
	for i, c := range []*workflowTurnRunner{clone1, clone2} {
		wg.Add(1)
		go func(i int, c *workflowTurnRunner) {
			defer wg.Done()
			c.SetUseMemoryForNextCall(i == 0) // per-clone write, as interp does
			if _, err := c.Run(context.Background(), "work item", nil); err != nil {
				t.Errorf("clone %d Run: %v", i, err)
			}
		}(i, c)
	}
	wg.Wait()

	if wtr.attachmentsInjected || wtr.sessionID != "" || wtr.nonce != "" {
		t.Errorf("original adapter mutated by clone runs: injected=%v sid=%q nonce=%q",
			wtr.attachmentsInjected, wtr.sessionID, wtr.nonce)
	}
	for i, c := range []*workflowTurnRunner{clone1, clone2} {
		if !c.attachmentsInjected {
			t.Errorf("clone %d must have consumed its own first-turn injection", i)
		}
		if c.sessionID != "s-item" {
			t.Errorf("clone %d sessionID = %q, want %q captured independently", i, c.sessionID, "s-item")
		}
	}
	if clone1.useMemoryNextCall == clone2.useMemoryNextCall {
		t.Errorf("use-memory flags set differently per clone must stay distinct (got both %v)", clone1.useMemoryNextCall)
	}

	// Both first turns carried image parts on THEIR OWN ctx (no instance
	// write — the shared-agent race the old SetPendingImageParts path had).
	if len(inner.ctxs) != 2 {
		t.Fatalf("inner saw %d calls, want 2", len(inner.ctxs))
	}
	for i, ctx := range inner.ctxs {
		parts, ok := local.ImagePartsFromContext(ctx)
		if !ok || len(parts) != 1 {
			t.Errorf("call %d image parts = %v ok=%v, want exactly one ctx part", i, parts, ok)
		}
	}
}
