package local

// project_map (issue #324): a token-cheap structural map of a codebase —
// files grouped by directory with their symbols and signatures, never
// bodies. Go files are outlined with go/ast (stdlib: no CGO/WASM, the
// single-binary distribution story stays intact); every other file is
// listed by name (file-tree degradation).
//
// Budget first: the outline degrades gracefully to fit max_chars —
// signatures → names-only → bare file tree — and textbudget is the hard
// backstop. Per-file symbol summaries are cached and re-parsed only when
// mtime/size change, so repeated calls in a session don't re-parse.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scoutme/milk/internal/textbudget"
)

const (
	projectMapDefaultMaxFiles = 200
	projectMapDefaultMaxChars = 6000
	projectMapFilesCap        = 1000  // hard cap regardless of max_files
	projectMapCharsCap        = 50000 // hard cap regardless of max_chars
	projectMapCacheMax        = 20000 // cached files before a wholesale reset
)

// projectMapEntry is one cached per-file symbol summary. full carries
// signatures ("func Load(path string) (*Config, error)"), names the compact
// form ("Load, Config, (*Store).Get").
type projectMapEntry struct {
	modTime time.Time
	size    int64
	full    string
	names   string
}

var projectMapCache = struct {
	sync.Mutex
	m map[string]projectMapEntry
}{m: map[string]projectMapEntry{}}

// projectMapFile is one file as rendered: rel path plus its two summaries
// (both empty for non-Go files).
type projectMapFile struct {
	rel   string
	full  string
	names string
}

func runProjectMap(ctx context.Context, args map[string]any, cwd string) (string, bool) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	root := resolvePath(path, cwd)
	info, err := os.Stat(root)
	if err != nil {
		return toolResult{Error: err.Error()}.String(), false
	}

	// A single file is allowed: map just it, rooted at its directory.
	scopeFile := ""
	if !info.IsDir() {
		scopeFile = filepath.Base(root)
		root = filepath.Dir(root)
	}

	maxFiles := projectMapIntArg(args, "max_files", projectMapDefaultMaxFiles, projectMapFilesCap)
	maxChars := projectMapIntArg(args, "max_chars", projectMapDefaultMaxChars, projectMapCharsCap)

	relFiles, ok := gitEnumerate(ctx, root)
	if !ok {
		relFiles = walkEnumerate(root)
	}
	if scopeFile != "" {
		var one []string
		for _, rel := range relFiles {
			if rel == scopeFile {
				one = append(one, rel)
				break
			}
		}
		relFiles = one
	}
	sort.Strings(relFiles)

	truncNote := ""
	if len(relFiles) > maxFiles {
		n := len(relFiles) - maxFiles
		word := "files"
		if n == 1 {
			word = "file"
		}
		truncNote = fmt.Sprintf("\n[%d more %s not shown — narrow with path= or raise max_files]", n, word)
		relFiles = relFiles[:maxFiles]
	}

	files := make([]projectMapFile, 0, len(relFiles))
	for _, rel := range relFiles {
		f := projectMapFile{rel: rel}
		if strings.EqualFold(filepath.Ext(rel), ".go") {
			f.full, f.names = projectMapFileSymbols(filepath.Join(root, rel))
		}
		files = append(files, f)
	}

	if len(files) == 0 {
		return toolResult{Output: "no files under " + root}.String(), false
	}

	// Degrade to fit the budget; textbudget is the hard backstop.
	out := projectMapRender(root, cwd, files, "full") + truncNote
	if len(out) > maxChars {
		out = projectMapRender(root, cwd, files, "names") + truncNote
	}
	if len(out) > maxChars {
		out = projectMapRender(root, cwd, files, "tree") + truncNote
	}
	out = textbudget.SummarizeLong(out, maxChars)
	return toolResult{Output: out}.String(), false
}

// projectMapIntArg reads a positive integer tool argument (JSON numbers
// arrive as float64), falling back to def and clamped to max.
func projectMapIntArg(args map[string]any, key string, def, max int) int {
	v := def
	switch n := args[key].(type) {
	case float64:
		if n > 0 {
			v = int(n)
		}
	case int:
		if n > 0 {
			v = n
		}
	}
	if v > max {
		return max
	}
	return v
}

// projectMapRender draws the outline in one of three modes: "full"
// (signatures), "names" (identifiers only), "tree" (bare file names).
// Non-Go files always render bare.
func projectMapRender(root, cwd string, files []projectMapFile, mode string) string {
	var b strings.Builder
	curDir := ""
	for _, f := range files {
		if dir := filepath.Dir(f.rel); dir != curDir {
			curDir = dir
			b.WriteString(projectMapDirLabel(root, cwd, dir) + "/\n")
		}
		b.WriteString("  " + filepath.Base(f.rel))
		summary := ""
		switch mode {
		case "full":
			summary = f.full
		case "names":
			summary = f.names
		}
		if summary != "" {
			b.WriteString("  " + summary)
		}
		b.WriteString("\n")
	}
	switch mode {
	case "names":
		b.WriteString("[signatures omitted to fit the budget — narrow with path= or raise max_chars]\n")
	case "tree":
		b.WriteString("[symbols omitted to fit the budget — narrow with path= or raise max_chars]\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// projectMapDirLabel renders a directory header: subdirs as their slash rel
// path; the scope root itself as the root path relative to the session cwd
// when that is shorter/clearer than the absolute path.
func projectMapDirLabel(root, cwd, dir string) string {
	if dir != "." {
		return filepath.ToSlash(dir)
	}
	label := root
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, root); err == nil && !strings.HasPrefix(rel, "..") {
			label = rel
		}
	}
	return filepath.ToSlash(label)
}

// projectMapFileSymbols returns a Go file's (full, names) summaries, from
// cache when the file is unchanged (mtime + size), else by parsing.
func projectMapFileSymbols(abs string) (full, names string) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", ""
	}
	projectMapCache.Lock()
	defer projectMapCache.Unlock()
	if e, ok := projectMapCache.m[abs]; ok && e.modTime.Equal(info.ModTime()) && e.size == info.Size() {
		return e.full, e.names
	}
	e := projectMapParse(abs, info.Size(), info.ModTime())
	if len(projectMapCache.m) >= projectMapCacheMax {
		projectMapCache.m = map[string]projectMapEntry{}
	}
	projectMapCache.m[abs] = e
	return e.full, e.names
}

// projectMapParse extracts declaration summaries with go/ast. Parse failure
// (syntax errors mid-edit) degrades the file to a bare listing rather than
// failing the whole map.
func projectMapParse(abs string, size int64, modTime time.Time) projectMapEntry {
	e := projectMapEntry{modTime: modTime, size: size}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, 0)
	if err != nil || f == nil {
		return e
	}
	var fullParts, nameParts []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			fullParts = append(fullParts, projectMapFuncSig(d))
			nameParts = append(nameParts, projectMapFuncShort(d))
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					fullParts = append(fullParts, projectMapTypeSig(s))
					nameParts = append(nameParts, s.Name.Name)
				case *ast.ValueSpec:
					var ns []string
					for _, n := range s.Names {
						if n.Name != "_" {
							ns = append(ns, n.Name)
						}
					}
					if len(ns) == 0 {
						continue
					}
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					fullParts = append(fullParts, kind+" "+strings.Join(ns, ", "))
					nameParts = append(nameParts, strings.Join(ns, ", "))
				}
			}
		}
	}
	e.full = strings.Join(fullParts, "; ")
	e.names = strings.Join(nameParts, ", ")
	return e
}

// projectMapFuncSig renders a function/method signature without its body:
// `func (s *Store) Get(key string) (string, error)`.
func projectMapFuncSig(fn *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		b.WriteString("(" + projectMapFieldList(fn.Recv) + ") ")
	}
	b.WriteString(fn.Name.Name)
	b.WriteString("(" + projectMapFieldList(fn.Type.Params) + ")")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		res := projectMapFieldList(fn.Type.Results)
		if len(fn.Type.Results.List) > 1 || strings.Contains(res, ", ") {
			b.WriteString(" (" + res + ")")
		} else {
			b.WriteString(" " + res)
		}
	}
	return b.String()
}

// projectMapFuncShort is the names-mode form: `Load` or `(*Store).Get`.
func projectMapFuncShort(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return "(" + types.ExprString(fn.Recv.List[0].Type) + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

// projectMapFieldList renders a parameter/result list: types only where
// unnamed, `name Type` otherwise (names help orientation, bodies never).
func projectMapFieldList(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		t := types.ExprString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, t)
			continue
		}
		var ns []string
		for _, n := range f.Names {
			ns = append(ns, n.Name)
		}
		parts = append(parts, strings.Join(ns, ", ")+" "+t)
	}
	return strings.Join(parts, ", ")
}

// projectMapTypeSig renders a type declaration: `type Config struct`,
// `type Handler interface`, `type Fn func(string) error`.
func projectMapTypeSig(ts *ast.TypeSpec) string {
	name := "type " + ts.Name.Name
	switch ts.Type.(type) {
	case *ast.StructType:
		return name + " struct"
	case *ast.InterfaceType:
		return name + " interface"
	default:
		return name + " " + types.ExprString(ts.Type)
	}
}
