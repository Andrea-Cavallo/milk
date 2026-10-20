package local

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/scoutme/milk/internal/workflow/board"
)

// boardToolNames are the tools registered ONLY for a turn whose ctx carries
// a shared workflow board (Definition.Board — issue #197): schemas are
// assembled per Run, so registration is per-call state and nothing is
// written to any shared agent instance (issue #297's discipline).
var boardToolNames = []string{"board_get", "board_claim", "board_update", "board_post"}

// boardToolSchemas returns the four board tool definitions. They are
// appended after filterTools — same dynamic-backing-store treatment as the
// task/otel tools: a run that declared a board structurally needs these
// (the workflow's prompts orchestrate around them), and noteToolUsed marks
// them observed so the ADR-0058 pruning ladder keeps them under pressure.
func boardToolSchemas() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "board_get",
				"description": "Read the workflow's shared coordination board: every item with its status/claim/notes/dependencies plus the newest teammate findings. Read this whenever you need the current picture — the board snapshot in your prompt may be stale.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "board_claim",
				"description": "Atomically claim a board item before working on it so concurrent teammates don't pick up the same one. First claimer wins: an item claimed by someone else (or done) comes back with its holder — do not work on it, report and move on. Idempotent for your own claim.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"item": map[string]any{"type": "integer", "description": "The item's index on the board"},
					},
					"required": []string{"item"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "board_update",
				"description": "Update a board item: status (in_progress when you start, blocked with the reason in notes when stuck, done with a short summary in notes when finished; todo releases your claim) and/or notes. The claimed status itself is CAS-only via board_claim.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"item":   map[string]any{"type": "integer", "description": "The item's index on the board"},
						"status": map[string]any{"type": "string", "description": "todo | in_progress | blocked | done (empty = keep)"},
						"notes":  map[string]any{"type": "string", "description": "Short progress/summary note (empty = keep)"},
					},
					"required": []string{"item"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "board_post",
				"description": "Post a short finding/announcement to the shared board for your teammates (e.g. \"the parser API changed in item 2 — anything importing it needs updating\"), optionally scoped to an item. Post discoveries the moment they matter — that is how teammates unblock each other.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "description": "The finding, one short paragraph"},
						"item": map[string]any{"type": "integer", "description": "Related item index (0/omitted = general)"},
					},
					"required": []string{"text"},
				},
			},
		},
	}
}

// appendBoardToolsForCtx is the single registration gate: board tools join
// the list only when ctx carries a shared board (Definition.Board, issue
// #197). Returns whether they were added (the caller pre-marks them
// observed so the ADR-0058 pruning ladder keeps them for the run).
func appendBoardToolsForCtx(tools []map[string]any, ctx context.Context) ([]map[string]any, bool) {
	if board.FromContext(ctx) == nil {
		return tools, false
	}
	return append(tools, boardToolSchemas()...), true
}

// boardToolGuidance is appended to the system prompt whenever board tools
// are registered (see memoryToolGuidance for the pattern).
const boardToolGuidance = `**Shared workflow board**: this run has a coordination board — the single source of truth for what teammates are doing. Conventions: before starting an item, claim it with board_claim (if it comes back claimed by a teammate or already done, do not work on it — say so and move on); board_update status=in_progress when you start, status=blocked with the reason in notes when you are stuck instead of retrying blindly, status=done with a short summary in notes when finished; post cross-item discoveries to the board immediately with board_post — a teammate may be blocked on exactly that; and re-read with board_get whenever you need the live picture, because the board snapshot rendered into your prompt reflects the board only at the start of this turn.`

// dispatchBoard* handlers: all four read the board and acting identity off
// the call's ctx (board.With / board.WithIdentity — interp sets them per
// stage), never off any shared instance.

func dispatchBoardGet(ctx context.Context) string {
	b := board.FromContext(ctx)
	if b == nil {
		return toolResult{Error: "board tools are only available in a workflow run with a shared board"}.String()
	}
	return toolResult{Output: b.Render()}.String()
}

func dispatchBoardClaim(ctx context.Context, args map[string]any) string {
	b := board.FromContext(ctx)
	if b == nil {
		return toolResult{Error: "board tools are only available in a workflow run with a shared board"}.String()
	}
	item, ok := boardItemArg(args)
	if !ok {
		return toolResult{Error: "board_claim requires an integer item index"}.String()
	}
	who := board.IdentityFrom(ctx)
	if who == "" {
		who = "unknown"
	}
	it, claimed := b.Claim(item, who)
	if !claimed {
		return toolResult{Output: fmt.Sprintf("NOT claimed — item %d is %s by %s: %s — do not work on it", item, it.Status, it.ClaimedBy, it.Label)}.String()
	}
	return toolResult{Output: fmt.Sprintf("claimed item %d (%s) as %s", item, it.Label, who)}.String()
}

func dispatchBoardUpdate(ctx context.Context, args map[string]any) string {
	b := board.FromContext(ctx)
	if b == nil {
		return toolResult{Error: "board tools are only available in a workflow run with a shared board"}.String()
	}
	item, ok := boardItemArg(args)
	if !ok {
		return toolResult{Error: "board_update requires an integer item index"}.String()
	}
	status, _ := args["status"].(string)
	notes, _ := args["notes"].(string)
	it, err := b.Update(item, status, notes)
	if err != nil {
		return toolResult{Error: err.Error()}.String()
	}
	return toolResult{Output: fmt.Sprintf("item %d is now %s (by %s): %s", it.Index, it.Status, it.ClaimedBy, it.Label)}.String()
}

func dispatchBoardPost(ctx context.Context, args map[string]any) string {
	b := board.FromContext(ctx)
	if b == nil {
		return toolResult{Error: "board tools are only available in a workflow run with a shared board"}.String()
	}
	text, _ := args["text"].(string)
	if text == "" {
		return toolResult{Error: "board_post requires text"}.String()
	}
	item := 0
	if v, ok := args["item"].(float64); ok {
		item = int(v)
	}
	who := board.IdentityFrom(ctx)
	if who == "" {
		who = "unknown"
	}
	f := b.Post(who, item, text)
	return toolResult{Output: fmt.Sprintf("posted finding as %s: %s", f.From, f.Text)}.String()
}

// boardItemArg reads the required integer item index from parsed args.
func boardItemArg(args map[string]any) (int, bool) {
	v, ok := args["item"].(float64)
	if !ok || v != float64(int(v)) {
		return 0, false
	}
	return int(v), true
}

// boardArgsJSON is a tiny helper for tests: marshals args for dispatchTool.
func boardArgsJSON(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
