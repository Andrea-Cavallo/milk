// Package board is the shared, file-backed coordination board that workflow
// definitions can opt into (Definition.Board — issue #197): a kanban-style
// single source of truth every role of a running workflow reads and writes
// during the run — items with claim/status/notes plus short findings any
// role can post for the others.
//
// It is a deliberately generic engine capability: nothing in this package
// knows which workflow (or workflow name) is using it. Concurrency: the
// board is shared between the interpreter's parallel_group item goroutines,
// so every mutation happens under one mutex and claims are compare-and-set
// (first claimer wins) — in-process only, which is exactly the fan-out's
// execution model. Every mutation is written through to the sidecar file
// atomically (tmp + rename) when a path is set, because parallel groups
// checkpoint only leaf-atomically: a mid-fan-out crash must not lose live
// claims. Resume loads the sidecar; a missing sidecar falls back to the
// board_seed stage's checkpointed value (see interp.loadBoard).
package board

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Item statuses. Claiming is its own transition (see Board.Claim) — a
// claimed item becomes in_progress through Update.
const (
	StatusTodo       = "todo"
	StatusClaimed    = "claimed"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDone       = "done"
)

// Statuses Update may set. "claimed" is deliberately excluded: claiming is
// a CAS against contention (Claim), not a status write.
var updatableStatuses = map[string]bool{
	StatusTodo: true, StatusInProgress: true, StatusBlocked: true, StatusDone: true,
}

const (
	// MaxFindings caps the stored finding list (oldest dropped) so a long
	// run cannot grow the sidecar or the rendered board without bound.
	MaxFindings = 200
	// maxFindingRunes trims one finding's text at post time.
	maxFindingRunes = 500
	// maxNotesRunes trims one item's notes at write time.
	maxNotesRunes = 1000
	// renderFindings is how many of the newest findings the rendered board
	// shows; combined with item-line rendering it keeps {{.board}} well
	// under the engine's per-variable budget (12K) with room to spare.
	renderFindings = 20
	// renderNotesRunes trims a note line in the rendered view (the stored
	// value keeps the full trimmed-at-post text).
	renderNotesRunes = 160
)

// Item is one board row — the decomposition of the work with its live state.
type Item struct {
	Index     int       `json:"index"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	ClaimedBy string    `json:"claimed_by,omitempty"`
	DependsOn []int     `json:"depends_on,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Finding is a short announcement any role can post for the others.
type Finding struct {
	From string    `json:"from"`
	Item int       `json:"item,omitempty"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Board is the shared store. The zero value is unusable — construct with
// New or LoadOrNew.
type Board struct {
	mu       sync.Mutex
	path     string // "" = in-memory only (no checkpoint to hang it on)
	items    []Item
	findings []Finding
}

// New returns an empty board persisted (write-through) to path; path ""
// builds an in-memory-only board.
func New(path string) *Board {
	return &Board{path: path}
}

// BoardPath derives the sidecar path for a workflow's checkpoint path —
// "<...>.workflow[.<id>].interp.json" → "<...>.workflow[.<id>].board.json"
// — so the two artefacts share a lifetime, directory and multiplicity.
func BoardPath(checkpointPath string) string {
	return strings.TrimSuffix(checkpointPath, "interp.json") + "board.json"
}

// LoadOrNew reads an existing sidecar, if any. found reports whether one
// existed (interp uses that to decide whether the board_seed trace value
// should rebuild a deleted sidecar). A corrupt file is an error, never a
// silent fresh board: resuming with an empty board would double-work every
// item.
func LoadOrNew(path string) (b *Board, found bool, err error) {
	if path == "" {
		return New(""), false, nil
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return New(path), false, nil
		}
		return nil, false, fmt.Errorf("board: read %s: %w", path, rerr)
	}
	var doc struct {
		Items    []Item    `json:"items"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("board: parse %s: %w", path, err)
	}
	return &Board{path: path, items: doc.Items, findings: doc.Findings}, true, nil
}

// saveLocked persists b atomically. Caller must hold b.mu.
func (b *Board) saveLocked() {
	if b.path == "" {
		return
	}
	data, err := json.MarshalIndent(struct {
		Items    []Item    `json:"items"`
		Findings []Finding `json:"findings"`
	}{b.items, b.findings}, "", "  ")
	if err != nil {
		return // encoding these shapes cannot fail in practice
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, b.path)
}

// Seed adds items the given list brings and the board doesn't have yet —
// idempotent by index, so re-running a seed stage (or a fan-out that
// replays its leaves) never resets live statuses or clobbers notes.
func (b *Board) Seed(items []Item) {
	b.mu.Lock()
	defer b.mu.Unlock()
	have := make(map[int]bool, len(b.items))
	for _, it := range b.items {
		have[it.Index] = true
	}
	added := false
	for _, it := range items {
		if have[it.Index] {
			continue
		}
		if it.Status == "" {
			it.Status = StatusTodo
		}
		it.UpdatedAt = time.Now()
		b.items = append(b.items, it)
		added = true
	}
	if added {
		sort.Slice(b.items, func(i, j int) bool { return b.items[i].Index < b.items[j].Index })
		b.saveLocked()
	}
}

// Items returns a copy of the items in index order.
func (b *Board) Items() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Item, len(b.items))
	copy(out, b.items)
	return out
}

// Findings returns a copy of the findings, oldest first.
func (b *Board) Findings() []Finding {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Finding, len(b.findings))
	copy(out, b.findings)
	return out
}

func findItem(items []Item, index int) *Item {
	for i := range items {
		if items[i].Index == index {
			return &items[i]
		}
	}
	return nil
}

// Claim atomically claims item index for who. It succeeds for a todo or
// blocked item, is idempotent when who already holds the claim, and fails
// (ok=false, with the holding item) when another claimer or a done item
// stands in the way — first claimer wins, structurally preventing the
// duplicate-work failure mode swarm only catches at integration time.
func (b *Board) Claim(index int, who string) (Item, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	it := findItem(b.items, index)
	if it == nil {
		return Item{}, false
	}
	switch {
	case it.Status == StatusClaimed && it.ClaimedBy == who:
		return *it, true // idempotent re-claim (resume, retry pass)
	case it.Status == StatusTodo || it.Status == StatusBlocked:
		it.Status = StatusClaimed
		it.ClaimedBy = who
		it.UpdatedAt = time.Now()
		b.saveLocked()
		return *it, true
	default:
		return *it, false // claimed/in_progress by someone else, or done
	}
}

// Update writes status and/or notes for item index (empty = keep).
// "claimed" is rejected — claims go through Claim's CAS — and unknown
// statuses are an error, so a typo can't park an item in a state no stage
// understands. Setting status back to "todo" releases any claim.
func (b *Board) Update(index int, status, notes string) (Item, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	it := findItem(b.items, index)
	if it == nil {
		return Item{}, fmt.Errorf("board: no item %d", index)
	}
	if status != "" {
		if status == StatusClaimed {
			return Item{}, fmt.Errorf("board: use board_claim to claim item %d (status %q is CAS-only)", index, status)
		}
		if !updatableStatuses[status] {
			return Item{}, fmt.Errorf("board: unknown status %q for item %d (want todo|in_progress|blocked|done)", status, index)
		}
		it.Status = status
		if status == StatusTodo {
			it.ClaimedBy = "" // release
		}
	}
	if notes != "" {
		it.Notes = trimRunes(notes, maxNotesRunes)
	}
	it.UpdatedAt = time.Now()
	b.saveLocked()
	return *it, nil
}

// Post appends a finding from a role (optionally scoped to an item),
// trimming and capping so a long run degrades instead of growing forever.
func (b *Board) Post(from string, item int, text string) Finding {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := Finding{
		From: from,
		Item: item,
		Text: trimRunes(text, maxFindingRunes),
		At:   time.Now(),
	}
	b.findings = append(b.findings, f)
	if len(b.findings) > MaxFindings {
		b.findings = append([]Finding(nil), b.findings[len(b.findings)-MaxFindings:]...)
	}
	b.saveLocked()
	return f
}

// Render is the deterministic text form injected as {{.board}}: a compact
// status table plus the newest findings. Stable input → stable output (the
// engine's per-variable budget applies on top of the caps here).
func (b *Board) Render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "shared board — %d item(s)", len(b.items))
	counts := map[string]int{}
	for _, it := range b.items {
		counts[it.Status]++
	}
	if len(b.items) > 0 {
		var parts []string
		for _, st := range []string{StatusDone, StatusInProgress, StatusClaimed, StatusBlocked, StatusTodo} {
			if counts[st] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
			}
		}
		fmt.Fprintf(&sb, " (%s)", strings.Join(parts, ", "))
	}
	sb.WriteByte('\n')
	for _, it := range b.items {
		mark := map[string]string{
			StatusTodo: "[ ]", StatusClaimed: "[c]", StatusInProgress: "[~]",
			StatusBlocked: "[!]", StatusDone: "[x]",
		}[it.Status]
		if mark == "" {
			mark = "[?]"
		}
		fmt.Fprintf(&sb, "  %s Item %d", mark, it.Index)
		if it.Label != "" {
			fmt.Fprintf(&sb, " — %s", it.Label)
		}
		if it.ClaimedBy != "" {
			fmt.Fprintf(&sb, " (by %s)", it.ClaimedBy)
		}
		if len(it.DependsOn) > 0 {
			var deps []string
			for _, d := range it.DependsOn {
				deps = append(deps, fmt.Sprintf("%d", d))
			}
			fmt.Fprintf(&sb, " (depends on %s)", strings.Join(deps, ","))
		}
		sb.WriteByte('\n')
		if it.Notes != "" {
			n := trimRunes(it.Notes, renderNotesRunes)
			n = strings.ReplaceAll(n, "\n", " ")
			fmt.Fprintf(&sb, "      notes: %s\n", n)
		}
	}
	if len(b.findings) > 0 {
		shown := b.findings
		if len(shown) > renderFindings {
			shown = shown[len(shown)-renderFindings:]
		}
		fmt.Fprintf(&sb, "findings (%d newest of %d):\n", len(shown), len(b.findings))
		for _, f := range shown {
			scope := ""
			if f.Item > 0 {
				scope = fmt.Sprintf(" → item %d", f.Item)
			}
			fmt.Fprintf(&sb, "  [%s%s] %s\n", f.From, scope, strings.ReplaceAll(f.Text, "\n", " "))
		}
	}
	return sb.String()
}

// trimRunes trims to at most n runes without splitting a rune (an
// ellipsis marker replaces the cut).
func trimRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
