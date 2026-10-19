package main

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/scoutme/milk/internal/livebuf"
)

// Thinking as a first-class view (ADR-0067, issue #208).
//
// Reasoning text no longer streams into the transcript buffer as dim-inline
// text with hand-managed escape bookkeeping. Each reasoning run becomes a
// thinkBlock: its text lives in its own buffer, out of band from the main
// transcript, anchored at the byte offset in the transcript it renders after.
// The renderer splices blocks in as fold containers — a one-line header
// ("▸ thinking · N lines · Ctrl+T") with the body shown only when expanded —
// so thinking is a region the renderer can style, hide or fold instead of a
// whole-buffer variant (the old transcript/transcriptNoThink pair).
//
// Display rules:
//   - /think off: every block renders one placeholder line
//     ("[thinking… Ctrl+T to show]"), bodies never shown.
//   - /think on: blocks render as fold containers, collapsed by default once
//     complete (issue #208: "collapsed by default even when the setting is
//     on" — long reasoning must not flood the transcript). The block that is
//     still streaming stays open so reasoning can be watched live; it folds
//     when it closes unless the user pinned it open.
//   - Clicking a fold header (or /think expand|collapse) toggles the body;
//     per-block state is remembered (userSet pins it against auto-fold).
//
// Consumption/display separation is preserved: loop detection and the session
// turn record consume reasoning from their own accumulators regardless of
// what the renderer shows.

// thinkBlock is one reasoning block of the visible transcript.
type thinkBlock struct {
	// buf holds the block's reasoning text — kept out of the main transcript
	// buffer so the render stack (colorize cache, wrapping, selection) keeps
	// working on one plain string and no ANSI escape bookkeeping is needed.
	buf strings.Builder
	// anchor is the byte offset in model.transcript this block renders after.
	// The block text itself never lands in the transcript, so anchors only
	// shift on retractStreamed surgery (handled there).
	anchor int
	// closed is true once no more chunks will arrive (regular content
	// followed, or the turn ended). While open the block is the transcript's
	// last content.
	closed bool
	// expanded is true while the body is rendered.
	expanded bool
	// userSet pins the expand state the user chose (fold-header click,
	// /think expand|collapse) so auto-fold-on-close no longer overrides it.
	userSet bool
}

// thinkLines reports the block's rendered line count (for the fold header).
func (b *thinkBlock) thinkLines() int {
	s := b.buf.String()
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// thinkHiddenPlaceholder is the one-line stand-in rendered in place of a
// thinking block while reasoning visibility is off (/think off).
const thinkHiddenPlaceholder = "[thinking… Ctrl+T to show]"

// thinkFoldedPrefix/thinkExpandedPrefix are the fold-header glyphs. The
// hit-test (thinkBlockAtLine) keys off these prefixes — see the note there on
// why that is safe enough.
const (
	thinkFoldedPrefix   = "▸ thinking · "
	thinkExpandedPrefix = "▾ thinking · "
)

// lineLabel renders "N lines"/"1 line" for the fold header.
func lineLabel(n int) string {
	if n == 1 {
		return "1 line"
	}
	return strconv.Itoa(n) + " lines"
}

// thinkHeader renders the one-line fold header for a block. showThinking
// selects the visible form; streaming (open) blocks say so.
func thinkHeader(b *thinkBlock, streaming bool) string {
	n := lineLabel(b.thinkLines())
	if b.expanded {
		if streaming {
			return dim(thinkExpandedPrefix + n + " · streaming")
		}
		return dim(thinkExpandedPrefix + n)
	}
	return dim(thinkFoldedPrefix + n + " · Ctrl+T")
}

// renderThinkBlock renders one block's container (header + optional body).
// The returned text always ends with a newline and never starts mid-line —
// transcriptDisplay inserts a separating newline when the surrounding text
// needs one.
func (m *model) renderThinkBlock(b *thinkBlock) string {
	if !m.showThinking {
		return dim(thinkHiddenPlaceholder) + "\n"
	}
	var sb strings.Builder
	sb.WriteString(thinkHeader(b, !b.closed))
	sb.WriteString("\n")
	if b.expanded {
		if body := b.buf.String(); body != "" {
			sb.WriteString(dim(body))
			if !strings.HasSuffix(body, "\n") {
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

// transcriptDisplay renders the visible transcript: the regular-text buffer
// with every thinking block spliced in at its anchor as a fold container.
// Everything downstream (colorize + wrap + selection) runs on the returned
// string exactly like it ran on the old activeTranscript() variant.
func (m *model) transcriptDisplay() string {
	text := m.transcript.String()
	if len(m.thinkBlocks) == 0 {
		return text
	}
	var sb strings.Builder
	pos := 0
	for i := range m.thinkBlocks {
		b := &m.thinkBlocks[i]
		at := b.anchor
		if at > len(text) {
			at = len(text)
		}
		if at < pos {
			at = pos
		}
		prefix := text[pos:at]
		sb.WriteString(prefix)
		if sb.Len() > 0 && !strings.HasSuffix(prefix, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString(m.renderThinkBlock(b))
		pos = at
	}
	sb.WriteString(text[pos:])
	return sb.String()
}

// isThinkHeaderLine reports whether a plain (ANSI-stripped) transcript line is
// a fold header. A body line could in principle imitate the prefix and skew
// thinkBlockAtLine's count — reasoning text is model output, not adversarial
// input, and the worst case is a mis-targeted fold toggle, so the simple
// prefix match is accepted here.
func isThinkHeaderLine(line string) bool {
	return strings.HasPrefix(line, thinkFoldedPrefix) || strings.HasPrefix(line, thinkExpandedPrefix)
}

// thinkBlockAtLine maps a clicked transcript line (index into
// transcriptPlainLines — the same coordinate space selection uses) to the
// think-block index whose fold header sits there, or -1 when the click was not
// on a header. Header lines appear in block order in the display, so the
// ordinal of the clicked header is the block index.
func (m *model) thinkBlockAtLine(line int) int {
	if !m.showThinking || line < 0 {
		return -1
	}
	lines := m.transcriptPlainLines()
	if line >= len(lines) || !isThinkHeaderLine(lines[line]) {
		return -1
	}
	idx := 0
	for i := 0; i < line && i < len(lines); i++ {
		if isThinkHeaderLine(lines[i]) {
			idx++
		}
	}
	if idx >= len(m.thinkBlocks) {
		return -1
	}
	return idx
}

// toggleThinkBlock flips one block's expand state and pins it (userSet), so a
// mid-stream click keeps the body open past the block's close.
func (m *model) toggleThinkBlock(idx int) {
	if idx < 0 || idx >= len(m.thinkBlocks) {
		return
	}
	b := &m.thinkBlocks[idx]
	b.userSet = true
	b.expanded = !b.expanded
	m.invalidateThinkDisplay()
}

// setAllThinkBlocks expands or folds every block, pinning each (userSet).
// Used by /think expand|collapse.
func (m *model) setAllThinkBlocks(expanded bool) {
	for i := range m.thinkBlocks {
		m.thinkBlocks[i].userSet = true
		m.thinkBlocks[i].expanded = expanded
	}
	m.invalidateThinkDisplay()
}

// applyThinkVisibility re-derives per-block state after the global toggle
// (/think on|off, Ctrl+T): the streaming block opens for live watching when
// reasoning becomes visible, pinned blocks keep their state.
func (m *model) applyThinkVisibility() {
	for i := range m.thinkBlocks {
		b := &m.thinkBlocks[i]
		if b.userSet {
			continue
		}
		if b.closed {
			b.expanded = false // completed blocks fold by default (issue #208)
		} else {
			b.expanded = m.showThinking
		}
	}
	m.invalidateThinkDisplay()
}

// closeOpenThinkBlock ends the streaming block (regular content or turn end
// followed): it folds unless the user pinned it open. No-op when nothing is
// open.
func (m *model) closeOpenThinkBlock() {
	for i := range m.thinkBlocks {
		b := &m.thinkBlocks[i]
		if b.closed {
			continue
		}
		b.closed = true
		if !b.userSet {
			b.expanded = false
		}
		m.invalidateThinkDisplay()
	}
}

// dropThinkBlocksFrom removes every block from index n on — the reasoning-
// promoted path (reasoningPromotedMsg), where the streamed reasoning became
// the turn's actual answer and keeping the blocks would show it twice.
func (m *model) dropThinkBlocksFrom(n int) {
	if n < 0 {
		n = 0
	}
	if n >= len(m.thinkBlocks) {
		return
	}
	m.thinkBlocks = m.thinkBlocks[:n]
	m.invalidateThinkDisplay()
}

// adjustThinkAnchors shifts block anchors after retractStreamed replaced
// [idx, idx+oldLen) with newLen bytes of text. Anchors are "render after this
// many bytes", so an anchor past the replaced region shifts by the delta; an
// anchor inside it (blocks sit on content boundaries, so this is defensive)
// clamps to the replacement's end.
func (m *model) adjustThinkAnchors(idx, oldLen, newLen int) {
	delta := newLen - oldLen
	for i := range m.thinkBlocks {
		a := &m.thinkBlocks[i].anchor
		switch {
		case *a >= idx+oldLen:
			*a += delta
		case *a > idx:
			*a = idx + newLen
		}
	}
}

// invalidateThinkDisplay forces the next wrappedTranscript render to rebuild:
// any fold-state change rewrites the displayed text mid-transcript, so the
// byte-offset-keyed colorize cache can't extend itself incrementally.
func (m *model) invalidateThinkDisplay() {
	m.colorizeForce = true
	m.colorizeTransLen = 0
}

// thinkViewMaxBytes caps the dedicated thinking view's buffer (issue #208's
// "toggle that swaps the main area to a thinking-only stream").
const thinkViewMaxBytes = 512 * 1024

// startThinkView swaps the main area to a thinking-only stream (/think view):
// every block's fold header plus its full body, in order, over the static
// preview attach view (ADR-0047) — so reasoning is inspectable on its own,
// independent of the transcript's scroll position.
func (m model) startThinkView() (model, tea.Cmd) {
	if len(m.thinkBlocks) == 0 {
		m.notify("no thinking blocks in this session yet", "")
		return m, nil
	}
	var sb strings.Builder
	for i := range m.thinkBlocks {
		b := &m.thinkBlocks[i]
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(thinkHeader(b, !b.closed))
		sb.WriteString("\n")
		if body := b.buf.String(); body != "" {
			sb.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				sb.WriteString("\n")
			}
		}
	}
	buf := livebuf.New(thinkViewMaxBytes)
	buf.Append([]byte(sb.String()))
	cmd := m.startAttach(attachPreview, "", "thinking", buf)
	return m, cmd
}
