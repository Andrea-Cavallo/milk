package local

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/memory"
	"github.com/scoutme/milk/internal/workflow/board"
)

// The registration gate: board tools exist only for a call whose ctx
// carries a shared board (Definition.Board, issue #197).
func TestBoardTools_RegistrationGated(t *testing.T) {
	if got, added := appendBoardToolsForCtx([]map[string]any{{"x": 1}}, context.Background()); added || len(got) != 1 {
		t.Fatalf("no board in ctx: added=%v len=%d — tools must be untouched", added, len(got))
	}
	ctx := board.With(context.Background(), board.New(""))
	got, added := appendBoardToolsForCtx(nil, ctx)
	if !added {
		t.Fatal("board in ctx must add the tools")
	}
	if len(got) != len(boardToolNames) {
		t.Fatalf("added %d tools, want %d", len(got), len(boardToolNames))
	}
	byName := map[string]bool{}
	for _, s := range got {
		byName[toolName(s)] = true
	}
	for _, n := range boardToolNames {
		if !byName[n] {
			t.Errorf("tool %q missing from the schema set", n)
		}
	}
	// Contract (B5-style): everything registered here is dispatchable.
	for _, n := range boardToolNames {
		res, _ := dispatchTool(ctx, n, "{}", nil, nil, memory.ConsumerAll, "", nil)
		if isUnknownToolResult(res) {
			t.Errorf("board tool %q is registered but dispatchTool does not know it", n)
		}
	}
}

func boardDispatch(t *testing.T, ctx context.Context, name, args string) (string, string) {
	t.Helper()
	res, _ := dispatchTool(ctx, name, args, nil, nil, memory.ConsumerAll, "", nil)
	var r toolResult
	if err := json.Unmarshal([]byte(res), &r); err != nil {
		t.Fatalf("dispatch %s: bad result %q: %v", name, res, err)
	}
	return r.Output, r.Error
}

// End-to-end handler flow on one board: claim → update → get → post, the
// contention refusal, and the no-board guard on every tool.
func TestBoardTools_DispatchFlow(t *testing.T) {
	b := board.New("")
	w1 := board.WithIdentity(board.With(context.Background(), b), "worker·Item-1")
	w2 := board.WithIdentity(board.With(context.Background(), b), "worker·Item-2")

	b.Seed([]board.Item{{Index: 1, Label: "parser"}})

	out, errE := boardDispatch(t, w1, "board_claim", `{"item":1}`)
	if errE != "" || !strings.Contains(out, "claimed item 1") {
		t.Fatalf("claim: out=%q err=%q", out, errE)
	}
	// Contending claimer is refused and told who holds it.
	out, errE = boardDispatch(t, w2, "board_claim", `{"item":1}`)
	if errE != "" || !strings.Contains(out, "NOT claimed") || !strings.Contains(out, "worker·Item-1") {
		t.Fatalf("contention: out=%q err=%q", out, errE)
	}
	out, errE = boardDispatch(t, w1, "board_update", `{"item":1,"status":"in_progress","notes":"started"}`)
	if errE != "" || !strings.Contains(out, "now in_progress") {
		t.Fatalf("update: out=%q err=%q", out, errE)
	}
	out, errE = boardDispatch(t, w2, "board_get", `{}`)
	if errE != "" || !strings.Contains(out, "[~] Item 1 — parser (by worker·Item-1)") {
		t.Fatalf("get: out=%q err=%q", out, errE)
	}
	out, errE = boardDispatch(t, w1, "board_post", `{"text":"signature changed","item":1}`)
	if errE != "" || !strings.Contains(out, "posted finding as worker·Item-1") {
		t.Fatalf("post: out=%q err=%q", out, errE)
	}
	// Attribution landed on the board itself.
	var found bool
	for _, f := range b.Findings() {
		if f.From == "worker·Item-1" && f.Item == 1 && f.Text == "signature changed" {
			found = true
		}
	}
	if !found {
		t.Errorf("finding not attributed on the board: %+v", b.Findings())
	}
}

func TestBoardTools_ErrorsAndGuards(t *testing.T) {
	b := board.New("")
	b.Seed([]board.Item{{Index: 1, Label: "parser"}})
	ctx := board.With(context.Background(), b)

	// Without a board in ctx every tool refuses plainly.
	plain := context.Background()
	for _, n := range boardToolNames {
		_, errE := boardDispatch(t, plain, n, `{"item":1,"text":"x"}`)
		if !strings.Contains(errE, "only available") {
			t.Errorf("%s without board: err=%q", n, errE)
		}
	}
	// Argument validation.
	if _, errE := boardDispatch(t, ctx, "board_claim", `{}`); !strings.Contains(errE, "integer item") {
		t.Errorf("claim without item: err=%q", errE)
	}
	if _, errE := boardDispatch(t, ctx, "board_update", `{"item":1,"status":"claimed"}`); !strings.Contains(errE, "CAS-only") {
		t.Errorf("update to claimed must point at board_claim: err=%q", errE)
	}
	if _, errE := boardDispatch(t, ctx, "board_update", `{"item":42,"status":"done"}`); !strings.Contains(errE, "no item 42") {
		t.Errorf("missing item: err=%q", errE)
	}
	if _, errE := boardDispatch(t, ctx, "board_post", `{"item":1}`); !strings.Contains(errE, "requires text") {
		t.Errorf("post without text: err=%q", errE)
	}
	// Unknown tool names behave like every other unknown.
	res, _ := dispatchTool(ctx, "board_spray", `{"item":1}`, nil, nil, memory.ConsumerAll, "", nil)
	if !isUnknownToolResult(res) {
		t.Errorf("unknown board tool must fall through: %s", res)
	}
}
