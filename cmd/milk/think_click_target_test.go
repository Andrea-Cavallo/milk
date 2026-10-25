package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Regression: clicking a fold header must toggle the block the header belongs
// to — nothing else. The pre-fix failure modes this pins:
//   - the click's screen-row→content-row mapping hardcoded "2 chrome rows"
//     above the viewport, so whenever the header bar wrapped (narrow terminal)
//     every click mapped rows low and toggled a later block (or none);
//   - the header→block ordinal counted only line-*start* headers, so a header
//     glued onto a neighbouring line by the markdown reflow (colorizeFull)
//     skewed every later block index.
//
// Each block gets a unique body line count (block i has i+1 lines), so the
// fold header's "N lines" label identifies the block exactly.
var thinkHdrLabelRe = regexp.MustCompile(`· ([0-9]+) lines?`)

func thinkBlockByLines(m *model, n int) int {
	for i := range m.thinkBlocks {
		if m.thinkBlocks[i].thinkLines() == n {
			return i
		}
	}
	return -1
}

func buildThinkClickModel(t *testing.T, theme string, width, height int, mode string, nBlocks int) model {
	t.Helper()
	oldTTY := isTTY
	isTTY = true
	t.Cleanup(func() { isTTY = oldTTY })
	oldTheme := th.name
	setTheme(theme)
	t.Cleanup(func() { setTheme(oldTheme) })

	m := newTestModelForPanels(t)
	nm, _ := m.handleResize(tea.WindowSizeMsg{Width: width, Height: height})
	m = nm.(model)
	m.colorizeMode = ParseColorizeMode(mode)

	m.appendTranscript("**user**: do some work please\n\n")
	for i := 0; i < nBlocks; i++ {
		// Block i gets i+1 body lines -> a unique "N lines" header label.
		for j := 0; j <= i; j++ {
			m.appendThinkingStreamed(fmt.Sprintf("thought %d line %d with some filler text to force wrapping at narrow widths\n", i, j))
		}
		m.appendTranscript(fmt.Sprintf("## answer %d\n\n", i))
		m.appendTranscript(fmt.Sprintf("here is what I did %d, with `code` in it and enough text to wrap around the width once or twice.\n\n", i))
	}
	m.closeOpenThinkBlock()
	m.setViewportContent()
	m.vp.GotoBottom()
	m.setViewportContent()
	return m
}

func TestThinkHeaderClickTogglesOwnBlock(t *testing.T) {
	for _, theme := range []string{"off", "supermilk"} {
		for _, width := range []int{160, 100, 72, 48} {
			for _, mode := range []string{"off", "fenced", "balanced", "full"} {
				for _, scroll := range []string{"bottom", "mid"} {
					t.Run(fmt.Sprintf("%s/w%d/%s/%s", theme, width, mode, scroll), func(t *testing.T) {
						m := buildThinkClickModel(t, theme, width, 40, mode, 12)
						if scroll == "mid" && m.vp.YOffset() > 2 {
							m.vp.SetYOffset(m.vp.YOffset() - 8)
							m.setViewportContent()
						}
						rows := strings.Split(m.viewString(), "\n")
						type cand struct{ row, wantLines int }
						var cs []cand
						for i, r := range rows {
							if thinkHeaderCount(r) == 0 {
								continue
							}
							mm := thinkHdrLabelRe.FindStringSubmatch(ansi.Strip(r))
							if mm == nil {
								t.Fatalf("surface row %d carries a header but no line label: %q", i, ansi.Strip(r))
							}
							n, _ := strconv.Atoi(mm[1])
							cs = append(cs, cand{row: i, wantLines: n})
						}
						if len(cs) == 0 {
							t.Fatalf("no think headers on screen (%d rows)", len(rows))
						}
						for _, c := range cs {
							want := thinkBlockByLines(&m, c.wantLines)
							if want < 0 {
								t.Fatalf("header %q on screen row %d has no block with %d lines", rows[c.row], c.row, c.wantLines)
							}
							for i := range m.thinkBlocks {
								m.thinkBlocks[i].expanded = false
								m.thinkBlocks[i].userSet = false
							}
							m.thinkFoldOpen = false
							m.invalidateThinkDisplay()
							m.setViewportContent()

							m.handleMouse(leftEv(mousePress, 2, c.row))
							m.handleMouse(leftEv(mouseRelease, 2, c.row))

							toggled := []int{}
							for i := range m.thinkBlocks {
								if m.thinkBlocks[i].expanded {
									toggled = append(toggled, i)
								}
							}
							if len(toggled) != 1 || toggled[0] != want {
								t.Errorf("click screen row %d (header %d lines -> block %d) toggled %v", c.row, c.wantLines, want, toggled)
							}
						}
					})
				}
			}
		}
	}
}

// A quoted header in ordinary transcript text (no thinking surface behind it)
// must not count toward the block ordinal: it used to shift every click below
// it by one block per quote ("acts on the wrong block, N blocks after").
func TestQuotedHeadersDoNotSkewClickMapping(t *testing.T) {
	oldTTY := isTTY
	isTTY = true
	defer func() { isTTY = oldTTY }()
	m := newTestModelForPanels(t)
	nm, _ := m.handleResize(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = nm.(model)

	m.appendTranscript("the fold header format is:\n\n▸ thinking · 9 lines\n▾ thinking · 9 lines · streaming\n\n")
	for i := 0; i < 4; i++ {
		for j := 0; j <= i; j++ {
			m.appendThinkingStreamed(fmt.Sprintf("real thought %d line %d\n", i, j))
		}
		m.appendTranscript(fmt.Sprintf("answer %d\n\n", i))
	}
	m.closeOpenThinkBlock()
	m.setViewportContent()
	m.vp.GotoBottom()
	m.setViewportContent()

	rows := strings.Split(m.viewString(), "\n")
	clicks := 0
	for i, r := range rows {
		if thinkHeaderCount(r) == 0 {
			continue
		}
		mm := thinkHdrLabelRe.FindStringSubmatch(ansi.Strip(r))
		if mm == nil {
			continue
		}
		n, _ := strconv.Atoi(mm[1])
		want := thinkBlockByLines(&m, n)
		if want < 0 {
			continue // quoted header label with no matching block — not clickable
		}
		for j := range m.thinkBlocks {
			m.thinkBlocks[j].expanded = false
			m.thinkBlocks[j].userSet = false
		}
		m.thinkFoldOpen = false
		m.invalidateThinkDisplay()
		m.setViewportContent()

		m.handleMouse(leftEv(mousePress, 2, i))
		m.handleMouse(leftEv(mouseRelease, 2, i))
		for j := range m.thinkBlocks {
			if m.thinkBlocks[j].expanded {
				if j != want {
					t.Errorf("click row %d (block %d) toggled %d", i, want, j)
				}
				clicks++
			}
		}
	}
	if clicks == 0 {
		t.Fatal("no header clicks landed on a block")
	}
}
