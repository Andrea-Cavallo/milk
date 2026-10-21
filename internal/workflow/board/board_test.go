package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBoardPath_DerivedFromCheckpoint(t *testing.T) {
	cases := map[string]string{
		"/x/sess123.workflow.interp.json":   "/x/sess123.workflow.board.json",
		"/x/sess123.workflow.2.interp.json": "/x/sess123.workflow.2.board.json",
	}
	for in, want := range cases {
		if got := BoardPath(in); got != want {
			t.Errorf("BoardPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeed_IdempotentKeepsLiveState(t *testing.T) {
	b := New("")
	b.Seed([]Item{{Index: 1, Label: "one"}, {Index: 2, Label: "two", DependsOn: []int{1}}})
	if _, ok := b.Claim(1, "worker·Item-1"); !ok {
		t.Fatal("claim item 1")
	}
	b.Update(1, StatusInProgress, "moving")

	// Re-seeding (a replayed board_seed leaf) must not reset live state.
	b.Seed([]Item{{Index: 1, Label: "one REVISED"}, {Index: 2, Label: "two", DependsOn: []int{1}}, {Index: 3, Label: "three"}})
	items := b.Items()
	if len(items) != 3 || items[0].Status != StatusInProgress || items[0].ClaimedBy != "worker·Item-1" || items[0].Notes != "moving" {
		t.Fatalf("re-seed clobbered live state: %+v", items[0])
	}
	if items[0].Label != "one" {
		t.Errorf("re-seed must not rename an existing item, got %q", items[0].Label)
	}
	if items[2].Status != StatusTodo {
		t.Errorf("new item must start todo, got %q", items[2].Status)
	}
}

func TestClaim_CASRules(t *testing.T) {
	b := New("")
	b.Seed([]Item{{Index: 1, Label: "one"}, {Index: 2, Label: "two"}})

	it, ok := b.Claim(1, "worker·Item-1")
	if !ok || it.Status != StatusClaimed || it.ClaimedBy != "worker·Item-1" {
		t.Fatalf("first claim = %+v ok=%v", it, ok)
	}
	// Idempotent for the same claimer (retry pass / resume).
	if _, ok := b.Claim(1, "worker·Item-1"); !ok {
		t.Error("re-claim by the same holder must succeed")
	}
	// Second claimer is refused and told who holds it.
	it, ok = b.Claim(1, "worker·Item-2")
	if ok {
		t.Fatal("second claimer must not win")
	}
	if it.ClaimedBy != "worker·Item-1" || it.Status != StatusClaimed {
		t.Errorf("refusal must carry the holder, got %+v", it)
	}
	// done blocks claiming.
	b.Update(2, StatusDone, "")
	if _, ok := b.Claim(2, "worker·Item-1"); ok {
		t.Error("a done item must not be claimable")
	}
	// Unknown item: refused.
	if _, ok := b.Claim(9, "x"); ok {
		t.Error("missing item must not be claimable")
	}
	// blocked is claimable (reassignment flow).
	b.Update(2, StatusBlocked, "stuck")
	if _, ok := b.Claim(2, "coordinator"); !ok {
		t.Error("a blocked item must be claimable for reassignment")
	}
}

func TestClaim_ConcurrentExactlyOneWinner(t *testing.T) {
	b := New("")
	b.Seed([]Item{{Index: 1, Label: "one"}})
	var wg sync.WaitGroup
	wins := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			who := fmt.Sprintf("worker-%d", i)
			if _, ok := b.Claim(1, who); ok {
				wins <- who
			}
		}(i)
	}
	wg.Wait()
	close(wins)
	var winners []string
	for w := range wins {
		winners = append(winners, w)
	}
	if len(winners) != 1 {
		t.Fatalf("exactly one claimer must win, got %v", winners)
	}
	if got := b.Items()[0].ClaimedBy; got != winners[0] {
		t.Errorf("board holder = %q, want %q", got, winners[0])
	}
}

func TestUpdate_TransitionsAndValidation(t *testing.T) {
	b := New("")
	b.Seed([]Item{{Index: 1, Label: "one"}})
	if _, err := b.Update(1, StatusClaimed, ""); err == nil {
		t.Error("status=claimed via Update must be rejected (CAS-only via Claim)")
	}
	if _, err := b.Update(1, "in-progress", ""); err == nil {
		t.Error("unknown status must be rejected")
	}
	if _, err := b.Update(7, StatusDone, ""); err == nil {
		t.Error("missing item must error")
	}
	b.Claim(1, "w")
	if _, err := b.Update(1, StatusInProgress, "started"); err != nil {
		t.Fatalf("in_progress: %v", err)
	}
	// todo releases the claim.
	it, err := b.Update(1, StatusTodo, "")
	if err != nil || it.ClaimedBy != "" {
		t.Errorf("releasing via todo: it=%+v err=%v", it, err)
	}
	// Notes are kept when empty is passed, trimmed when long.
	b.Update(1, "", "kept note")
	it, _ = b.Update(1, StatusDone, "")
	if it.Notes != "kept note" {
		t.Errorf("empty notes must keep the previous value, got %q", it.Notes)
	}
	long := strings.Repeat("é", maxNotesRunes+50)
	it, _ = b.Update(1, StatusTodo, long)
	if len([]rune(it.Notes)) > maxNotesRunes+1 {
		t.Errorf("notes must be trimmed to %d runes, got %d", maxNotesRunes, len([]rune(it.Notes)))
	}
}

func TestPost_TrimsAndCaps(t *testing.T) {
	b := New("")
	long := strings.Repeat("ü", maxFindingRunes+100)
	f := b.Post("worker·Item-1", 2, long)
	if f.Text == "" || len([]rune(f.Text)) > maxFindingRunes+1 { // +1 for the ellipsis
		t.Errorf("finding text len = %d runes (want <= %d+1)", len([]rune(f.Text)), maxFindingRunes)
	}
	for i := 0; i < MaxFindings+40; i++ {
		b.Post("w", 0, fmt.Sprintf("f%d", i))
	}
	fs := b.Findings()
	if len(fs) != MaxFindings {
		t.Fatalf("findings = %d, want capped at %d", len(fs), MaxFindings)
	}
	if !strings.HasSuffix(fs[len(fs)-1].Text, fmt.Sprintf("f%d", MaxFindings+39)) {
		t.Errorf("newest findings must survive the cap, last = %q", fs[len(fs)-1].Text)
	}
}

func TestRender_DeterministicAndBounded(t *testing.T) {
	b := New("")
	b.Seed([]Item{
		{Index: 1, Label: "parser"},
		{Index: 2, Label: "cli", DependsOn: []int{1}},
		{Index: 3, Label: "docs"},
	})
	b.Claim(1, "worker·Item-1")
	b.Update(1, StatusInProgress, "multi\nline note")
	b.Update(3, StatusDone, "")
	for i := 1; i <= 30; i++ {
		b.Post(fmt.Sprintf("w%d", i), 0, fmt.Sprintf("finding %d", i))
	}
	r1, r2 := b.Render(), b.Render()
	if r1 != r2 {
		t.Error("Render must be deterministic (byte-identical across calls)")
	}
	for _, want := range []string{
		"3 item(s)", "1 done", "1 in_progress", "1 todo",
		"[~] Item 1 — parser (by worker·Item-1)",
		"notes: multi line note", // newline flattened
		"(depends on 1)",
		"findings (20 newest of 30)",
		"[w30] finding 30",
	} {
		if !strings.Contains(r1, want) {
			t.Errorf("render missing %q:\n%s", want, r1)
		}
	}
	if strings.Contains(r1, "[w5] finding 5") {
		t.Error("old findings beyond the newest 20 must not render")
	}
}

func TestLoadOrNew_RoundTripAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.workflow.board.json")

	b, found, err := LoadOrNew(path)
	if err != nil || found {
		t.Fatalf("fresh load: found=%v err=%v", found, err)
	}
	b.Seed([]Item{{Index: 1, Label: "one"}})
	b.Claim(1, "w")
	b.Post("w", 1, "hi")

	b2, found, err := LoadOrNew(path)
	if err != nil || !found {
		t.Fatalf("reload: found=%v err=%v", found, err)
	}
	it := b2.Items()[0]
	if it.Status != StatusClaimed || it.ClaimedBy != "w" {
		t.Errorf("persisted claim lost: %+v", it)
	}
	if len(b2.Findings()) != 1 {
		t.Errorf("persisted finding lost: %+v", b2.Findings())
	}

	// In-memory boards never touch disk.
	mem := New("")
	mem.Seed([]Item{{Index: 1, Label: "x"}})

	// Corrupt sidecar is an error — never a silent fresh board.
	if err := os.WriteFile(path, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrNew(path); err == nil {
		t.Error("a corrupt sidecar must surface an error, not start empty")
	}
}

// A retry of an item the same worker already moved to in_progress re-claims it
// (idempotent), a done item must be reopened first, and reopening releases the
// claim so the same worker — or another — can take it again.
func TestClaim_RetryAfterProgressAndReopen(t *testing.T) {
	b := New("")
	b.Seed([]Item{{Index: 1}})
	b.Claim(1, "worker·Item-1")
	b.Update(1, StatusInProgress, "")
	if _, ok := b.Claim(1, "worker·Item-1"); !ok {
		t.Error("re-claim of the owner's own in_progress item must succeed")
	}
	if _, ok := b.Claim(1, "worker·Item-2"); ok {
		t.Error("another claimer must not take an in_progress item")
	}

	b.Update(1, StatusDone, "")
	if _, ok := b.Claim(1, "worker·Item-1"); ok {
		t.Fatal("a done item stays unclaimable until reopened, even for its owner")
	}
	it, err := b.Update(1, StatusTodo, "")
	if err != nil || it.ClaimedBy != "" {
		t.Fatalf("reopen: %+v err=%v (claim must be released)", it, err)
	}
	if _, ok := b.Claim(1, "worker·Item-1"); !ok {
		t.Error("a reopened item must be claimable again")
	}
}

func TestPost_AbsorbsRepeatedFinding(t *testing.T) {
	b := New("")
	b.Post("worker·Item-3", 4, "signature changed")
	b.Post("worker·Item-3", 4, "signature changed") // model repeated the call
	b.Post("worker·Item-3", 5, "signature changed") // different scope: kept
	b.Post("worker·Item-9", 4, "signature changed") // different sender: kept
	if got := len(b.Findings()); got != 3 {
		t.Errorf("findings = %d, want 3 (only the exact repeat absorbed)", got)
	}
}
