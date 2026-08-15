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
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/eric/deadcodedetector/internal/finding"
)

// DefaultReachTimeout bounds Rapid Type Analysis. SSA+RTA over trees that
// pull in btcd/libp2p/IPFS can run for many minutes with no output; the
// unused-reference pass still completes.
const DefaultReachTimeout = 45 * time.Second

// MaxRTAPackages is the largest import graph we will build SSA for.
const MaxRTAPackages = 400

// Options control Go analysis.
type Options struct {
	Root      string
	Patterns  []string
	Tests     bool
	Exported  *bool // nil = auto
	Reachable bool
	// Timeout bounds the optional RTA pass. Zero uses DefaultReachTimeout.
	// A negative value disables the time limit (still subject to MaxRTAPackages
	// and the heavy-module heuristic).
	Timeout time.Duration
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

	// Load local packages only (export data for imports). This is enough
	// for unused-reference analysis and does not build SSA of every dep.
	mode := packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
		packages.NeedImports | packages.NeedTypes | packages.NeedTypesSizes |
		packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule

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
		// Monorepos (starlight): the scan root has no go.mod, but
		// backend/ does. Analyze each nested module instead of silently
		// reporting nothing.
		return detectNestedModules(opts, root)
	}
	moduleDir := moduleDirOf(initial, root)
	reportExported := decideExported(opts.Exported, initial)

	var fs []finding.Finding
	fs = append(fs, unusedSymbols(initial, reportExported, moduleDir)...)

	if opts.Reachable {
		if extra, why := maybeUnreachable(opts, root, patterns, initial, moduleDir, reportExported); why != "" {
			fmt.Fprintf(os.Stderr, "dcd: skipping Go reachability: %s\n", why)
		} else if len(extra) > 0 {
			fs = dedup(append(fs, extra...))
		}
	}
	return fs, nil
}

func detectNestedModules(opts Options, root string) ([]finding.Finding, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var all []finding.Finding
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			continue
		}
		sub := opts
		sub.Root = dir
		fs, err := Detect(sub)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dcd: go: skip %s: %v\n", filepath.Base(dir), err)
			continue
		}
		all = append(all, fs...)
	}
	return all, nil
}

func maybeUnreachable(opts Options, root string, patterns []string, initial []*packages.Package, moduleDir string, reportExported bool) ([]finding.Finding, string) {
	if why := rtaSkipReason(initial); why != "" {
		return nil, why
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultReachTimeout
	}
	type result struct {
		fs  []finding.Finding
		err error
	}
	ch := make(chan result, 1)
	go func() {
		cfg := &packages.Config{
			Mode:  packages.LoadAllSyntax | packages.NeedModule,
			Dir:   root,
			Tests: opts.Tests,
			Env:   os.Environ(),
			Fset:  token.NewFileSet(),
		}
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			ch <- result{nil, err}
			return
		}
		fs, err := unreachable(pkgs, opts.Tests, moduleDir, reportExported)
		ch <- result{fs, err}
	}()
	if timeout < 0 {
		r := <-ch
		if r.err != nil {
			return nil, r.err.Error()
		}
		return r.fs, ""
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err.Error()
		}
		return r.fs, ""
	case <-time.After(timeout):
		return nil, fmt.Sprintf("timed out after %s", timeout)
	}
}

func rtaSkipReason(pkgs []*packages.Package) string {
	seen := map[*packages.Package]bool{}
	var heavy string
	var walk func(*packages.Package)
	walk = func(p *packages.Package) {
		if p == nil || seen[p] || heavy != "" {
			return
		}
		seen[p] = true
		if isHeavyPath(p.PkgPath) {
			heavy = p.PkgPath
			return
		}
		for _, imp := range p.Imports {
			walk(imp)
		}
	}
	for _, p := range pkgs {
		walk(p)
	}
	if heavy != "" {
		return fmt.Sprintf("import graph includes %s (SSA/RTA is not practical)", heavy)
	}
	if len(seen) > MaxRTAPackages {
		return fmt.Sprintf("import graph has %d packages (limit %d)", len(seen), MaxRTAPackages)
	}
	return ""
}

func isHeavyPath(p string) bool {
	// These dependency trees are correct RTA targets but routinely take
	// many minutes to load-all-syntax + SSA + RTA on a laptop.
	for _, pre := range []string{
		"github.com/btcsuite/btcd",
		"github.com/btcsuite/btcutil",
		"github.com/libp2p/",
		"github.com/ipfs/",
		"github.com/multiformats/",
		"github.com/ethereum/",
		"github.com/gogo/protobuf",
		"k8s.io/",
		"sigs.k8s.io/",
		"cloud.google.com/",
		"github.com/aws/aws-sdk-go",
		"github.com/Azure/",
		"google.golang.org/api",
		"go.opentelemetry.io/",
	} {
		if p == strings.TrimSuffix(pre, "/") || strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
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
