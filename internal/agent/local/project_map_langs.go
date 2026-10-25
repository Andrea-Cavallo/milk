package local

// Heuristic symbol extractors for project_map (issue #324): JS/TS, Python,
// Java and Rust outlines without a parser dependency. Milk ships pure-Go
// single binaries (release.yml builds with CGO_ENABLED=0), so CGO
// tree-sitter bindings are out; these line/brace scanners are the pragmatic
// middle between "nothing" and a parse tree. They are precise about the
// declarations that matter (types, funcs/methods with their signatures,
// consts) and deliberately never emit bodies. A tree-sitter WASM path is
// the precision upgrade if heuristics ever mislead — see ADR-0081.
//
// Every extractor returns []projectMapSym in declaration order. Full lines
// are trimmed at an opening brace / `=>` / `;` and capped; names use the
// Go-style qualified form for methods, e.g. `(Store).get`.

import (
	"regexp"
	"strings"
)

const projectMapSigCap = 120

// projectMapCapSig trims and bounds one signature line.
func projectMapCapSig(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > projectMapSigCap {
		return s[:projectMapSigCap-1] + "…"
	}
	return s
}

// projectMapCutDecl returns a declaration line cut before its body: at the
// first `{`, `=>` or `;` outside brackets. `:` is deliberately not a cut
// point — TS/JS method return types (`foo(): void {`) live there.
func projectMapCutDecl(s string) string {
	s = strings.TrimSpace(s)
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ';':
			if depth <= 0 {
				return projectMapCapSig(strings.TrimSpace(s[:i]))
			}
		case '=':
			if i+1 < len(s) && s[i+1] == '>' && depth <= 0 {
				return projectMapCapSig(strings.TrimSpace(s[:i+2]))
			}
		}
	}
	return projectMapCapSig(s)
}

var (
	pyClassRe = regexp.MustCompile(`^class\s+(\w+)(?:\(([^)]*)\))?`)
	pyDefRe   = regexp.MustCompile(`^(async\s+)?def\s+(\w+)\s*\((.*)\)\s*(->.*?)?\s*:`)
	pyConstRe = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)\s*[:=]`)
)

// projectMapExtractPython outlines Python sources line-by-line, tracking the
// indent stack to qualify methods as `(Class).method`. Multiline signatures
// are joined before matching.
func projectMapExtractPython(src []byte) []projectMapSym {
	var out []projectMapSym
	type frame struct {
		indent  int
		name    string
		isClass bool
	}
	var stack []frame

	lines := strings.Split(string(src), "\n")
	docMarker := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		// Triple-quoted docstrings never outline: track their spans and skip.
		if docMarker != "" {
			if strings.Contains(trimmed, docMarker) {
				docMarker = ""
			}
			continue
		}
		for _, marker := range []string{"\"\"\"", "'''"} {
			if strings.HasPrefix(trimmed, marker) {
				if !strings.Contains(trimmed[len(marker):], marker) {
					docMarker = marker
				}
				trimmed = ""
				break
			}
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Join a continued signature (unclosed brackets) into one line.
		for pyOpenBrackets(trimmed) > 0 && i+1 < len(lines) {
			i++
			trimmed += " " + strings.TrimSpace(lines[i])
		}
		indent := projectMapPythonIndent(line)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if m := pyClassRe.FindStringSubmatch(trimmed); m != nil {
			full := "class " + m[1]
			if m[2] != "" {
				full += "(" + projectMapCapSig(m[2]) + ")"
			}
			out = append(out, projectMapSym{Name: m[1], Full: full})
			stack = append(stack, frame{indent: indent, name: m[1], isClass: true})
			continue
		}
		if m := pyDefRe.FindStringSubmatch(trimmed); m != nil {
			name := m[2]
			full := "def " + name + "(" + projectMapCapSig(m[3]) + ")"
			if m[1] != "" {
				full = "async " + full
			}
			short := name
			if len(stack) > 0 && stack[len(stack)-1].isClass {
				short = "(" + stack[len(stack)-1].name + ")." + name
			}
			out = append(out, projectMapSym{Name: short, Full: full})
			stack = append(stack, frame{indent: indent, name: name})
			continue
		}
		if indent == 0 {
			if m := pyConstRe.FindStringSubmatch(trimmed); m != nil {
				out = append(out, projectMapSym{Name: m[1], Full: "const " + m[1]})
			}
		}
	}
	return out
}

// pyOpenBrackets counts unclosed ( [ { on a (joined) signature line.
func pyOpenBrackets(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
	}
	return depth
}

// projectMapPythonIndent counts leading spaces (a tab counts as 4).
func projectMapPythonIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

var (
	jsFuncRe   = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(async\s+)?function\s*\*?\s*(\w+)`)
	jsClassRe  = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?class\s+(\w+)`)
	jsTypeRe   = regexp.MustCompile(`^(?:export\s+)?(interface|type|enum)\s+(\w+)`)
	jsArrowRe  = regexp.MustCompile(`^(?:export\s+)?(const|let|var)\s+(\w+)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:function\b|\([^)]*\)\s*=>|\w+\s*=>)`)
	jsMethodRe = regexp.MustCompile(`^(?:static\s+)?(?:async\s+)?(?:get\s+|set\s+)?(\w+)\s*\([^)]*\)\s*(?::[^{]+)?\{`)
)

// projectMapExtractJS outlines JS/TS/JSX/TSX sources: a string/comment-stripped
// line scan with a brace-depth counter. Class members qualify as
// `(Class).method`; top-level arrow/function consts count as functions.
func projectMapExtractJS(src []byte) []projectMapSym {
	var out []projectMapSym
	depth := 0
	classDepth := -1
	className := ""
	inBlockComment := false

	for _, raw := range strings.Split(string(src), "\n") {
		line, still := projectMapStripJSLine(raw, inBlockComment)
		inBlockComment = still
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if classDepth >= 0 && depth <= classDepth {
			classDepth, className = -1, ""
		}

		switch {
		case jsClassRe.MatchString(trimmed):
			m := jsClassRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[1], Full: projectMapCutDecl(trimmed)})
			classDepth, className = depth, m[1]
		case jsFuncRe.MatchString(trimmed):
			m := jsFuncRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[2], Full: projectMapCutDecl(trimmed)})
		case jsTypeRe.MatchString(trimmed):
			m := jsTypeRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[2], Full: m[1] + " " + m[2]})
		case jsArrowRe.MatchString(trimmed):
			m := jsArrowRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[2], Full: projectMapCutDecl(trimmed)})
		case classDepth >= 0 && depth == classDepth+1 && jsMethodRe.MatchString(trimmed):
			m := jsMethodRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: "(" + className + ")." + m[1], Full: projectMapCutDecl(trimmed)})
		}

		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return out
}

// projectMapStripJSLine removes string/comment content from one line (block
// comments may continue across lines — the flag rides in the return value),
// leaving the structural skeleton and braces intact.
func projectMapStripJSLine(line string, inBlockComment bool) (string, bool) {
	var b strings.Builder
	inStr := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inBlockComment {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inStr != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String(), false
		case c == '/' && i+1 < len(line) && line[i+1] == '*':
			inBlockComment = true
			i++
		case c == '"' || c == '\'' || c == '`':
			inStr = c
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), inBlockComment
}

var (
	javaTypeRe   = regexp.MustCompile(`^(?:(?:public|private|protected|static|final|abstract|sealed|non-sealed)\s+)*(class|interface|enum|record)\s+(\w+)`)
	javaMethodRe = regexp.MustCompile(`^(?:(?:public|private|protected|static|final|abstract|synchronized|native|default)\s+)*(?:<[^>]+>\s*)?[\w<>\[\],.?\s]+\s+(\w+)\s*\([^)]*\)\s*(?:throws\s+[\w.,\s]+)?\s*[{;]`)
	javaCtorRe   = regexp.MustCompile(`^(?:(?:public|private|protected|static|final|abstract|synchronized|native|default)\s+)*(\w+)\s*\([^)]*\)\s*(?:throws\s+[\w.,\s]+)?\s*\{`)
)

// projectMapExtractJava outlines Java sources: type declarations at any
// depth, constructors and modifier-bearing methods at class-body depth
// (fields and imports are skipped — the outline is for orientation, not
// inventory; package-private no-modifier methods are a known heuristic gap).
func projectMapExtractJava(src []byte) []projectMapSym {
	var out []projectMapSym
	depth := 0
	typeDepth := -1
	typeName := ""

	for _, raw := range strings.Split(string(src), "\n") {
		line, _ := projectMapStripJSLine(raw, false)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "import ") {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth < 0 {
				depth = 0
			}
			continue
		}

		if typeDepth >= 0 && depth <= typeDepth {
			typeDepth, typeName = -1, ""
		}

		switch {
		case javaTypeRe.MatchString(trimmed):
			m := javaTypeRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[2], Full: m[1] + " " + m[2]})
			typeDepth, typeName = depth, m[2]
		case typeDepth >= 0 && depth == typeDepth+1 && !strings.Contains(trimmed, "=") && javaMethodRe.MatchString(trimmed):
			m := javaMethodRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: "(" + typeName + ")." + m[1], Full: projectMapCutDecl(trimmed)})
		case typeDepth >= 0 && depth == typeDepth+1 && !strings.Contains(trimmed, "=") && javaCtorRe.MatchString(trimmed):
			m := javaCtorRe.FindStringSubmatch(trimmed)
			if m[1] == typeName {
				out = append(out, projectMapSym{Name: "(" + typeName + ")." + typeName, Full: projectMapCutDecl(trimmed)})
			}
		}

		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return out
}

var (
	rustFnRe    = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:default\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]*"\s+)?fn\s+(\w+)`)
	rustTypeRe  = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait|type|union|mod)\s+(\w+)`)
	rustImplRe  = regexp.MustCompile(`^impl(?:<[^>]*>)?\s+(?:[\w:]+(?:<[^>]*>)?\s+for\s+)?([\w:]+)`)
	rustConstRe = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+(\w+)\s*:`)
)

// projectMapExtractRust outlines Rust sources: fns, type-ish declarations,
// consts/statics, and impl members qualified as `(Type).fn`.
func projectMapExtractRust(src []byte) []projectMapSym {
	var out []projectMapSym
	depth := 0
	implDepth := -1
	implType := ""

	for _, raw := range strings.Split(string(src), "\n") {
		line, _ := projectMapStripJSLine(raw, false)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth < 0 {
				depth = 0
			}
			continue
		}

		if implDepth >= 0 && depth <= implDepth {
			implDepth, implType = -1, ""
		}

		switch {
		case rustImplRe.MatchString(trimmed):
			m := rustImplRe.FindStringSubmatch(trimmed)
			implType = m[1] // the target type (`impl X` or `impl Trait for X`)
			out = append(out, projectMapSym{Name: "impl " + implType, Full: projectMapCutDecl(trimmed)})
			implDepth = depth
		case rustFnRe.MatchString(trimmed):
			m := rustFnRe.FindStringSubmatch(trimmed)
			short := m[1]
			if implDepth >= 0 {
				short = "(" + implType + ")." + m[1]
			}
			out = append(out, projectMapSym{Name: short, Full: projectMapCutDecl(trimmed)})
		case rustTypeRe.MatchString(trimmed):
			m := rustTypeRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[2], Full: m[1] + " " + m[2]})
		case rustConstRe.MatchString(trimmed):
			m := rustConstRe.FindStringSubmatch(trimmed)
			out = append(out, projectMapSym{Name: m[1], Full: projectMapCutDecl(trimmed)})
		}

		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			depth = 0
		}
	}
	return out
}
