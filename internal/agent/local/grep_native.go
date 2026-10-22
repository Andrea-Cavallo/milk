package local

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// grepNativeMaxFileBytes bounds a single file the native search reads whole;
// larger files (logs, dumps, bundles) are skipped rather than slurped.
const grepNativeMaxFileBytes = 8 << 20

// nativeGrep is runGrep's last-resort backend, used when neither ripgrep nor a
// git work tree is available. It needs no external binary (Windows has no
// grep) and uses Go's RE2 syntax, a superset of what models write for ERE
// (foo|bar, a+, plus \d \w \s and (?i)). Like rg it skips hidden entries,
// binary files, symlinks and walkSkipDirs, and prints rg's format:
// "path:line:text" for matches, "path-line-text" for context and "--" between
// non-adjacent groups. Without git there is no ignore-file semantics to honor.
// Returns the output, an rg-style exit code (0 matches, 1 none, 2 bad
// pattern) and an error message for stderr.
func nativeGrep(ctx context.Context, pattern, root string, ignoreCase bool, contextLines int, include string, maxResults int) (out string, code int, errMsg string) {
	expr := pattern
	if ignoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", 2, "invalid pattern: " + err.Error()
	}

	var b strings.Builder
	matched := 0
	more := 0 // matching lines beyond maxResults (counted, not printed)
	wroteAny := false

	searchFile := func(path string) {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > grepNativeMaxFileBytes {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return // binary
		}
		lines := strings.Split(string(data), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		var hits []int
		for i, l := range lines {
			if re.MatchString(strings.TrimSuffix(l, "\r")) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 0 {
			return
		}
		isHit := make(map[int]bool, len(hits))
		for _, h := range hits {
			isHit[h] = true
		}
		next := 0 // first line index not yet printed for this file
		firstGroup := true
		for _, h := range hits {
			if h >= next && matched >= maxResults {
				more++
				continue
			}
			start := max(next, h-contextLines)
			end := min(len(lines)-1, h+contextLines)
			if contextLines > 0 && wroteAny && h >= next && (firstGroup || start > next) {
				b.WriteString("--\n")
			}
			firstGroup = false
			// A hit already printed inside an earlier group's context still
			// extends the trailing context (start == next > h-contextLines).
			for i := start; i <= end; i++ {
				sep := "-"
				if isHit[i] {
					sep = ":"
					matched++
				}
				fmt.Fprintf(&b, "%s%s%d%s%s\n", path, sep, i+1, sep, strings.TrimSuffix(lines[i], "\r"))
				wroteAny = true
			}
			next = max(next, end+1)
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", 2, err.Error()
	}
	if !info.IsDir() {
		searchFile(root)
	} else {
		var includeRe *regexp.Regexp
		if include != "" {
			includeRe = globToRegex(include)
		}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error { //nolint:errcheck
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if werr != nil {
				return nil
			}
			name := d.Name()
			if p != root && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if walkSkipDirs[name] {
					return filepath.SkipDir
				}
				return nil
			}
			if includeRe != nil {
				target := name
				if strings.Contains(include, "/") {
					rel, _ := filepath.Rel(root, p)
					target = filepath.ToSlash(rel)
				}
				if !includeRe.MatchString(target) {
					return nil
				}
			}
			searchFile(p)
			return nil
		})
	}

	if matched == 0 {
		return "", 1, ""
	}
	out = strings.TrimSuffix(b.String(), "\n")
	if more > 0 {
		out += fmt.Sprintf("\n[truncated: %d more matching lines; refine the pattern or raise max_results]", more)
	}
	return out, 0, ""
}
