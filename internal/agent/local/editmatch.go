package local

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// editmatch.go — the fuzzy fallback chain for edit_file (Phase 2 of
// docs/internal/analysis/local-agent-toolset-review.md, G1).
//
// When the exact old_string is not found, a chain of progressively looser
// matchers tries to locate the block anyway (trailing-whitespace drift,
// indentation differences, collapsed whitespace, leaked \n escape literals).
// A match is only ever APPLIED when it is unique (unless replace_all); when
// nothing matches, the closest block is returned as a hint and never applied
// silently. Inspired by opencode's edit.ts replacer chain and MiMo-Code's
// closest-match hint, implemented from scratch.

// editSpan is a match of old_string within newline-normalized file content.
type editSpan struct{ start, end int }

// normalizeNewlines returns s with \r\n collapsed to \n plus whether the
// string predominantly used CRLF (so the edit round-trips unchanged).
func normalizeNewlines(s string) (string, bool) {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n") - crlf
	return strings.ReplaceAll(s, "\r\n", "\n"), crlf > 0 && crlf >= lf
}

// denormalizeNewlines restores CRLF line endings when the file used them.
func denormalizeNewlines(s string, crlf bool) string {
	if !crlf {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// collapseWS collapses runs of spaces/tabs into a single space.
func collapseWS(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	_ = space
	return strings.TrimSuffix(b.String(), " ")
}

// unescapeCStyle interprets the common backslash escapes models leak into
// string arguments ("\n" typed as four characters, "\t", "\"", "\\"). Any
// other backslash sequence is kept verbatim so nothing is silently swallowed.
func unescapeCStyle(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				i++
				continue
			case '"':
				b.WriteByte('"')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// literalSpans returns every non-overlapping occurrence of needle in content.
func literalSpans(content, needle string) []editSpan {
	if needle == "" {
		return nil
	}
	var spans []editSpan
	for i := 0; ; {
		idx := strings.Index(content[i:], needle)
		if idx < 0 {
			break
		}
		spans = append(spans, editSpan{i + idx, i + idx + len(needle)})
		i += idx + len(needle)
	}
	return spans
}

// splitLines drops a single trailing empty element (a trailing newline is not
// a line of the block).
func splitLines(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineBlockSpans matches oldStr against content line-by-line, comparing lines
// through key(). Returns the byte span of each matched line range.
func lineBlockSpans(content, oldStr string, key func(string) string) []editSpan {
	oldLines := splitLines(oldStr)
	if len(oldLines) == 0 {
		return nil
	}
	oldKeys := make([]string, len(oldLines))
	anyKey := false
	for i, l := range oldLines {
		oldKeys[i] = key(l)
		if oldKeys[i] != "" {
			anyKey = true
		}
	}
	if !anyKey {
		return nil // a pure-whitespace block would match everywhere
	}

	lines := strings.Split(content, "\n")
	offs := make([]int, len(lines))
	o := 0
	for i, l := range lines {
		offs[i] = o
		o += len(l) + 1
	}

	var spans []editSpan
	for i := 0; i+len(oldKeys) <= len(lines); i++ {
		match := true
		for j := range oldKeys {
			if key(lines[i+j]) != oldKeys[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		last := i + len(oldKeys) - 1
		spans = append(spans, editSpan{offs[i], offs[last] + len(lines[last])})
	}
	return spans
}

// editMatcher is one stage of the fallback chain.
type editMatcher struct {
	name string
	find func(content, oldStr string) []editSpan
}

// editMatchers runs strict → loose. The first stage that finds anything wins,
// so a stricter interpretation is always preferred over a fuzzier one.
func editMatchers(oldStr string) []editMatcher {
	unescaped := unescapeCStyle(oldStr)
	trimmed := strings.TrimSpace(oldStr)

	ms := []editMatcher{
		{"exact", func(content, s string) []editSpan { return literalSpans(content, s) }},
	}
	if unescaped != oldStr {
		ms = append(ms, editMatcher{"escape-normalized", func(content, _ string) []editSpan {
			return literalSpans(content, unescaped)
		}})
	}
	if trimmed != oldStr && trimmed != "" {
		// Boundary-only fuzz: the old_string's leading/trailing blank lines
		// don't match. Deliberately not active for pure indentation drift —
		// trimming those would match mid-line fragments.
		if i := strings.Index(oldStr, trimmed); i >= 0 &&
			(strings.Contains(oldStr[:i], "\n") || strings.Contains(oldStr[i+len(trimmed):], "\n")) {
			ms = append(ms, editMatcher{"trimmed-boundary", func(content, _ string) []editSpan {
				return literalSpans(content, trimmed)
			}})
		}
	}
	ms = append(ms,
		editMatcher{"line-trimmed", func(content, s string) []editSpan {
			return lineBlockSpans(content, s, func(l string) string { return strings.TrimSpace(l) })
		}},
		editMatcher{"whitespace-normalized", func(content, s string) []editSpan {
			// No TrimSpace here: whitespace runs (including leading ones) all
			// collapse to one space, so only run-length drift is tolerated —
			// indentation level still matters. The indentation-flexible stage
			// below loosens that last constraint.
			return lineBlockSpans(content, s, collapseWS)
		}},
		editMatcher{"indentation-flexible", func(content, s string) []editSpan {
			return lineBlockSpans(content, s, func(l string) string { return collapseWS(strings.TrimLeft(l, " \t")) })
		}},
	)
	return ms
}

// applyEdit replaces oldStr with newStr in content (file named path in error
// messages). Returns the updated content, the replacement count, and the
// matcher that located the block ("exact" when no fuzziness was needed). When
// nothing matches, the error carries the closest matching block as a hint —
// never an applied edit.
func applyEdit(content, oldStr, newStr, path string, replaceAll bool) (string, int, string, error) {
	if oldStr == "" {
		return content, 0, "", fmt.Errorf("old_string must not be empty")
	}
	content, crlf := normalizeNewlines(content)
	newStr, _ = normalizeNewlines(newStr)

	for _, m := range editMatchers(oldStr) {
		spans := m.find(content, oldStr)
		if len(spans) == 0 {
			continue
		}
		if len(spans) > 1 && !replaceAll {
			return content, 0, m.name, fmt.Errorf(
				"old_string is ambiguous: found %d occurrences in %s; use replace_all=true to replace all",
				len(spans), path)
		}
		use := spans
		if !replaceAll {
			use = spans[:1]
		}
		// Replace back-to-front so earlier spans keep their offsets.
		out := content
		for i := len(use) - 1; i >= 0; i-- {
			out = out[:use[i].start] + newStr + out[use[i].end:]
		}
		return denormalizeNewlines(out, crlf), len(use), m.name, nil
	}

	startLine, block := closestBlock(content, oldStr)
	return content, 0, "", fmt.Errorf(
		"old_string not found in %s. Closest match (line %d):\n%s\n"+
			"Update old_string to match the file exactly — milk never applies fuzzy matches silently.",
		path, startLine, block)
}

// closestBlock returns the 1-based line number and text of the line window
// most similar to oldStr — the hint the model gets when nothing matched. The
// block is capped so a pathological file cannot flood the tool result.
func closestBlock(content, oldStr string) (int, string) {
	key := func(l string) string { return collapseWS(strings.TrimSpace(l)) }
	lines := strings.Split(content, "\n")
	oldLines := splitLines(oldStr)
	n := len(oldLines)
	if n == 0 {
		n = 1
	}
	oldKeys := make([]string, n)
	for i := range oldKeys {
		if i < len(oldLines) {
			oldKeys[i] = key(oldLines[i])
		}
	}

	bestScore, bestIdx := -1, 0
	for i := 0; i+n <= len(lines); i++ {
		score := 0
		for j := 0; j < n; j++ {
			a, b := key(lines[i+j]), oldKeys[j]
			switch {
			case a == b && a != "":
				score += 2
			case a != "" && b != "" && (strings.Contains(a, b) || strings.Contains(b, a)):
				score++
			}
		}
		if score > bestScore {
			bestScore, bestIdx = score, i
		}
	}
	if len(lines) == 0 {
		return 1, ""
	}

	const maxLines, maxChars = 40, 4000
	end := min(bestIdx+n, len(lines))
	if end-bestIdx > maxLines {
		end = bestIdx + maxLines
	}
	block := strings.Join(lines[bestIdx:end], "\n")
	if len(block) > maxChars {
		block = block[:maxChars] + "\n… (hint truncated)"
	}
	return bestIdx + 1, block
}

// writeFileAtomic writes data to path via a temp file in the same directory
// plus rename, so a crash or a concurrent reader never sees a half-written
// file. perm is used for the new file; an existing file keeps its mode.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".milk-edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	return nil
}
