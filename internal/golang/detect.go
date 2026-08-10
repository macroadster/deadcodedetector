// Package golang detects unused and unreachable Go code.
package golang

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/eric/deadcodedetector/internal/finding"
)

// Options control Go analysis.
type Options struct {
	Root      string
	Patterns  []string
	Tests     bool
	Exported  *bool // nil = auto
	Reachable bool
}

// Detect loads the Go module under root and reports dead code.
func Detect(opts Options) ([]finding.Finding, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	mode := packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
		packages.NeedImports | packages.NeedTypes | packages.NeedTypesSizes |
		packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule |
		packages.NeedDeps
	if opts.Reachable {
		mode = packages.LoadAllSyntax | packages.NeedModule
	}

	cfg := &packages.Config{
		Mode:  mode,
		Dir:   root,
		Tests: opts.Tests,
		Env:   os.Environ(),
		Fset:  token.NewFileSet(),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		// Still try to analyze packages that type-checked.
	}

	initial := initialPackages(pkgs)
	if len(initial) == 0 {
		return nil, nil
	}
	moduleDir := moduleDirOf(initial, root)
	reportExported := decideExported(opts.Exported, initial)

	var fs []finding.Finding
	fs = append(fs, unusedSymbols(initial, reportExported, moduleDir)...)

	if opts.Reachable {
		if extra, err := unreachable(pkgs, opts.Tests, moduleDir, reportExported); err == nil {
			fs = dedup(append(fs, extra...))
		}
	}
	return fs, nil
}

func initialPackages(pkgs []*packages.Package) []*packages.Package {
	var out []*packages.Package
	seen := map[*packages.Package]bool{}
	for _, p := range pkgs {
		if p == nil || p.Types == nil || len(p.GoFiles) == 0 {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if isStdOrExternal(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isStdOrExternal(p *packages.Package) bool {
	if p.Module == nil {
		// stdlib or GOPATH without module info
		if !strings.Contains(p.PkgPath, ".") {
			return true
		}
	}
	return false
}

func moduleDirOf(pkgs []*packages.Package, root string) string {
	for _, p := range pkgs {
		if p.Module != nil && p.Module.Dir != "" {
			return p.Module.Dir
		}
	}
	return root
}

func decideExported(flag *bool, pkgs []*packages.Package) bool {
	if flag != nil {
		return *flag
	}
	for _, p := range pkgs {
		if p.Name == "main" && !strings.HasSuffix(p.ID, ".test]") && !strings.HasSuffix(p.PkgPath, ".test") {
			// A real main package (not the generated test main) implies whole-program.
			if !isTestMain(p) {
				return true
			}
		}
	}
	return false
}

func isTestMain(p *packages.Package) bool {
	if strings.HasSuffix(p.PkgPath, ".test") {
		return true
	}
	if strings.Contains(p.ID, ".test]") || strings.HasSuffix(p.ID, ".test") {
		return true
	}
	return false
}

func inModule(path, moduleDir string) bool {
	if moduleDir == "" {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	mod, err := filepath.Abs(moduleDir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(mod, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func generated(src []byte) bool {
	// https://go.dev/s/generatedcode
	first := src
	if len(first) > 512 {
		first = first[:512]
	}
	s := string(first)
	return strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT")
}

func ignoredPos(fset *token.FileSet, file *ast.File, pos token.Pos) bool {
	if file == nil || !pos.IsValid() {
		return false
	}
	p := fset.Position(pos)
	cmap := ast.NewCommentMap(fset, file, file.Comments)
	// Same-line or immediately preceding comment.
	for node, groups := range cmap {
		np := fset.Position(node.Pos())
		if np.Filename != p.Filename {
			continue
		}
		if np.Line != p.Line && np.Line != p.Line-1 && np.Line != p.Line+1 {
			// still check comments whose end is on the same line as pos
		}
		for _, g := range groups {
			gp := fset.Position(g.Pos())
			ge := fset.Position(g.End())
			if gp.Filename != p.Filename {
				continue
			}
			if ge.Line == p.Line || gp.Line == p.Line || gp.Line == p.Line-1 {
				if commentIgnores(g.Text()) {
					return true
				}
			}
		}
	}
	// Also scan every comment on the same / previous line (CommentMap can miss GenDecl specs).
	for _, cg := range file.Comments {
		gp := fset.Position(cg.Pos())
		if gp.Filename == p.Filename && (gp.Line == p.Line || gp.Line == p.Line-1) && commentIgnores(cg.Text()) {
			return true
		}
	}
	return false
}

func commentIgnores(text string) bool {
	low := strings.ToLower(text)
	return strings.Contains(low, "dcd:ignore") ||
		strings.Contains(low, "deadcode:ignore") ||
		strings.Contains(low, "nolint:dcd") ||
		strings.Contains(low, "nolint:deadcode")
}

func fileForPos(pkg *packages.Package, pos token.Pos) *ast.File {
	if !pos.IsValid() {
		return nil
	}
	tf := pkg.Fset.File(pos)
	if tf == nil {
		return nil
	}
	name := tf.Name()
	for _, f := range pkg.Syntax {
		tff := pkg.Fset.File(f.Pos())
		if tff != nil && tff.Name() == name {
			return f
		}
	}
	return nil
}

func relPath(path, moduleDir string) string {
	if moduleDir == "" {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(moduleDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func kindOf(obj types.Object) finding.Kind {
	switch obj := obj.(type) {
	case *types.Func:
		if obj.Type() != nil {
			if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
				return finding.UnusedMethod
			}
		}
		return finding.UnusedFunction
	case *types.TypeName:
		return finding.UnusedType
	case *types.Const:
		return finding.UnusedConst
	case *types.Var:
		return finding.UnusedVar
	default:
		return finding.UnusedVar
	}
}

func messageOf(k finding.Kind, name string) string {
	switch k {
	case finding.UnusedFunction:
		return fmt.Sprintf("unused function %s", name)
	case finding.UnusedMethod:
		return fmt.Sprintf("unused method %s", name)
	case finding.UnusedType:
		return fmt.Sprintf("unused type %s", name)
	case finding.UnusedConst:
		return fmt.Sprintf("unused const %s", name)
	case finding.UnusedVar:
		return fmt.Sprintf("unused var %s", name)
	case finding.UnreachableFunction:
		return fmt.Sprintf("unreachable func %s", name)
	default:
		return fmt.Sprintf("unused %s", name)
	}
}

func dedup(fs []finding.Finding) []finding.Finding {
	// Prefer unused_* over unreachable for the same object.
	seen := map[string]int{}
	var out []finding.Finding
	id := func(f finding.Finding) string {
		return fmt.Sprintf("%s|%s|%d", f.Path, f.Name, f.Line)
	}
	for _, f := range fs {
		k := id(f)
		if i, ok := seen[k]; ok {
			// If one is unused_* and the other unreachable, keep unused.
			if out[i].Kind == finding.UnreachableFunction && f.Kind != finding.UnreachableFunction {
				out[i] = f
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, f)
	}
	return out
}
