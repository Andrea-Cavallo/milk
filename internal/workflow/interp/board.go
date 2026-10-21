package interp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/scoutme/milk/internal/workflow"
	"github.com/scoutme/milk/internal/workflow/board"
)

// loadBoard resolves the run's shared board (Definition.Board, issue #197).
// The sidecar file (board.BoardPath of the checkpoint path) is the source of
// truth — written through on every mutation, because parallel groups
// checkpoint only leaf-atomically and mid-fan-out claims must survive a
// crash. When the sidecar is gone but the checkpoint still holds the
// board_seed stage's leaf, the items are rebuilt from that value (findings
// and live claim statuses in a deleted sidecar are unrecoverable by
// construction; the item skeleton is not).
func loadBoard(checkpointPath string, trace []TraceEntry, stages []workflow.Stage) (*board.Board, error) {
	path := ""
	if checkpointPath != "" {
		path = board.BoardPath(checkpointPath)
	}
	b, found, err := board.LoadOrNew(path)
	if err != nil {
		return nil, err
	}
	if found || checkpointPath == "" {
		return b, nil
	}
	if items, ok := seedItemsFromTrace(trace, stages); ok {
		b.Seed(items)
	}
	return b, nil
}

// seedItemsFromTrace finds the board_seed stage's checkpointed leaf value
// (recorded as []board.Item, round-tripped through JSON like every trace
// value) and rebuilds items from it.
func seedItemsFromTrace(trace []TraceEntry, stages []workflow.Stage) ([]board.Item, bool) {
	if len(trace) == 0 {
		return nil, false
	}
	ids := map[string]bool{}
	var collect func([]workflow.Stage)
	collect = func(sts []workflow.Stage) {
		for _, s := range sts {
			if s.Kind == workflow.StageKindBoardSeed {
				ids[s.ID] = true
			}
			collect(s.Body)
		}
	}
	collect(stages)
	for _, e := range trace {
		if !ids[e.StageID] || e.Value == nil {
			continue
		}
		raw, err := json.Marshal(e.Value)
		if err != nil {
			continue
		}
		var items []board.Item
		if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
			continue
		}
		return items, true
	}
	return nil, false
}

var (
	boardLabelListMarker = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+`)
	boardLabelBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
)

// cleanBoardLabel turns a plan section's first content line into a board
// label: the plan is markdown, the board is a plain-text table, so list
// markers, bold spans and inline-code ticks are dropped (a designer that
// bolds each item's title would otherwise leave "**…**" in every label).
func cleanBoardLabel(line string) string {
	line = strings.TrimSpace(line)
	line = boardLabelListMarker.ReplaceAllString(line, "")
	line = boardLabelBold.ReplaceAllString(line, "$1")
	line = strings.ReplaceAll(line, "`", "")
	return strings.TrimSpace(line)
}

// execBoardSeed populates the shared board from a plan document's declared
// sections (deterministic — no agent turn). Seeding is idempotent (indexes
// already on the board keep their live status), so a replayed leaf or a
// re-run pass never resets work in progress. The items are recorded as the
// leaf's value so a deleted sidecar can be rebuilt from the checkpoint
// (loadBoard).
func execBoardSeed(ec *execContext, s workflow.Stage) error {
	if _, ok, err := tryReplay(ec, s); ok {
		return err
	}
	if ec.board == nil {
		return fmt.Errorf("workflow: stage %q (board_seed): definition has no board (set \"board: true\")", s.ID)
	}
	doc, _ := ec.vars[s.From].(string)
	decls := ParseDeclarations(doc)
	sections := decls.SectionsFor(s.Over)
	ec.pushPath(s.ID)
	ec.reportProgress("")
	defer ec.popPath()
	if len(sections) == 0 && ec.cfg.Send != nil {
		ec.cfg.Send(workflow.WorkflowChunkMsg{Text: fmt.Sprintf(
			"\n[warning: board_seed %q found no %q sections in %q — board starts empty]\n", s.ID, s.Over, s.From)})
	}
	items := make([]board.Item, 0, len(sections))
	for _, sec := range sections {
		// Section.Label is the COLLECTION name ("Item"), not a per-item
		// title — and the body carries the section's own "## Item N"
		// heading as its first line. The human label is the first content
		// line after the heading ("" renders as just "Item N").
		label := ""
		for _, line := range strings.Split(sec.Body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "##") {
				continue
			}
			label = cleanBoardLabel(line)
			break
		}
		items = append(items, board.Item{Index: sec.Index, Label: label, DependsOn: sec.DependsOn})
	}
	ec.board.Seed(items)
	ec.recordLeaf(TraceEntry{StageID: s.ID, SaveAs: s.SaveAs, Value: items})
	return nil
}
