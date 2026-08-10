package golang

import (
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/eric/deadcodedetector/internal/finding"
)

func unreachable(pkgs []*packages.Package, tests bool, moduleDir string, reportExported bool) ([]finding.Finding, error) {
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var roots []*ssa.Function
	for _, sp := range ssaPkgs {
		if sp == nil {
			continue
		}
		// Package initializers are always roots when the package itself is
		// reachable from a main; RTA starts at main+init of main packages.
		_ = tests
	}
	mains := ssautil.MainPackages(ssaPkgs)
	if len(mains) == 0 {
		return nil, nil
	}
	for _, m := range mains {
		if fn := m.Func("init"); fn != nil {
			roots = append(roots, fn)
		}
		if fn := m.Func("main"); fn != nil {
			roots = append(roots, fn)
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}

	res := rta.Analyze(roots, false)
	if res == nil {
		return nil, nil
	}

	// Test-variant packages rebuild the same source into different SSA
	// functions. If any clone of a function is reachable, it is live.
	livePos := map[string]bool{}
	for fn := range res.Reachable {
		if fn == nil || fn.Object() == nil || !fn.Object().Pos().IsValid() {
			continue
		}
		p := prog.Fset.Position(fn.Object().Pos())
		livePos[p.Filename+":"+itoa(p.Line)+":"+fn.Object().Name()] = true
	}

	var fs []finding.Finding
	for fn := range ssautil.AllFunctions(prog) {
		if fn == nil || fn.Object() == nil {
			continue
		}
		if _, live := res.Reachable[fn]; live {
			continue
		}
		if fn.Object().Pos().IsValid() {
			p := prog.Fset.Position(fn.Object().Pos())
			if livePos[p.Filename+":"+itoa(p.Line)+":"+fn.Object().Name()] {
				continue
			}
		}
		obj := fn.Object()
		if skipObject(obj, reportExported) {
			continue
		}
		pos := obj.Pos()
		if !pos.IsValid() {
			continue
		}
		p := prog.Fset.Position(pos)
		if p.Filename == "" || !inModule(p.Filename, moduleDir) {
			continue
		}
		// Skip compiler-generated bodies (wrappers, thunks).
		if fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "package initializer") {
			continue
		}
		pkg := packageForFile(pkgs, p.Filename)
		if pkg != nil {
			if file := fileForPos(pkg, pos); ignoredPos(pkg.Fset, file, pos) {
				continue
			}
		}
		name := fn.RelString(fn.Pkg.Pkg)
		// RelString is like "(T).M" or "foo". Prefer a short display name.
		if obj, ok := obj.(*types.Func); ok {
			if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
				name = recvPrefix(sig.Recv()) + "." + obj.Name()
			} else {
				name = obj.Name()
			}
		}
		fs = append(fs, finding.Finding{
			Language: finding.Go,
			Kind:     finding.UnreachableFunction,
			Path:     relPath(p.Filename, moduleDir),
			Line:     p.Line,
			Column:   p.Column,
			Name:     name,
			Message:  messageOf(finding.UnreachableFunction, name),
		})
	}
	return fs, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

func packageForFile(pkgs []*packages.Package, filename string) *packages.Package {
	for _, p := range pkgs {
		for _, f := range p.GoFiles {
			if f == filename {
				return p
			}
		}
		for _, f := range p.CompiledGoFiles {
			if f == filename {
				return p
			}
		}
	}
	return nil
}
