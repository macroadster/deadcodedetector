package golang

import (
	"go/ast"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/eric/deadcodedetector/internal/finding"
)

func unusedSymbols(pkgs []*packages.Package, reportExported bool, moduleDir string) []finding.Finding {
	used := map[string]bool{}
	ifaces := collectInterfaces(pkgs)
	generatedFiles := map[string]bool{}

	for _, pkg := range pkgs {
		for i, f := range pkg.Syntax {
			filename := ""
			if pkg.Fset != nil && f.Pos().IsValid() {
				filename = pkg.Fset.Position(f.Pos()).Filename
			}
			if filename == "" && i < len(pkg.CompiledGoFiles) {
				filename = pkg.CompiledGoFiles[i]
			}
			if filename != "" {
				if b, err := os.ReadFile(filename); err == nil && generated(b) {
					generatedFiles[filename] = true
				}
			}
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, obj := range pkg.TypesInfo.Uses {
			if id := objectID(obj); id != "" {
				used[id] = true
			}
		}
		// Selections (method values / field accesses) also count.
		for _, sel := range pkg.TypesInfo.Selections {
			if sel == nil {
				continue
			}
			if id := objectID(sel.Obj()); id != "" {
				used[id] = true
			}
		}
	}

	var fs []finding.Finding
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil {
			continue
		}
		if isGeneratedTestPkg(pkg) {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			id := objectID(obj)
			if obj == nil || id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if skipObject(obj, reportExported) {
				continue
			}
			if used[id] {
				continue
			}
			if f := findingFor(pkg, obj, moduleDir, generatedFiles); f != nil {
				fs = append(fs, *f)
			}
		}
		// Methods on named types declared in this package.
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			tn, ok := obj.(*types.TypeName)
			if !ok || tn.Type() == nil {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			// Value and pointer method sets; Object() is shared.
			for _, set := range []*types.MethodSet{
				types.NewMethodSet(named),
				types.NewMethodSet(types.NewPointer(named)),
			} {
				for i := 0; i < set.Len(); i++ {
					fn, _ := set.At(i).Obj().(*types.Func)
					id := objectID(fn)
					if fn == nil || id == "" || seen[id] {
						continue
					}
					// Skip methods promoted from embedded fields (declared elsewhere).
					if fn.Pkg() == nil || fn.Pkg().Path() != pkg.Types.Path() {
						continue
					}
					seen[id] = true
					if skipObject(fn, reportExported) {
						continue
					}
					if used[id] {
						continue
					}
					if implementsUsedInterface(fn, named, ifaces) {
						continue
					}
					if f := findingFor(pkg, fn, moduleDir, generatedFiles); f != nil {
						fs = append(fs, *f)
					}
				}
			}
		}
	}
	return fs
}

func skipObject(obj types.Object, reportExported bool) bool {
	if obj == nil {
		return true
	}
	if obj.Parent() == types.Universe {
		return true
	}
	name := obj.Name()
	if name == "_" || name == "init" || name == "main" {
		return true
	}
	if ast.IsExported(name) && !reportExported {
		return true
	}
	if isTestEntry(name) {
		return true
	}
	// Blank-assigned interface assertions: var _ I = T{} lives on the var.
	if v, ok := obj.(*types.Var); ok && v.Name() == "_" {
		return true
	}
	return false
}

func isTestEntry(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			// TestX where X is uppercase or a digit, per go test convention.
			r := rune(name[len(p)])
			if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return true
			}
		}
	}
	return false
}

func findingFor(pkg *packages.Package, obj types.Object, moduleDir string, generatedFiles map[string]bool) *finding.Finding {
	pos := obj.Pos()
	if !pos.IsValid() {
		return nil
	}
	p := pkg.Fset.Position(pos)
	if p.Filename == "" || !inModule(p.Filename, moduleDir) {
		return nil
	}
	if generatedFiles[p.Filename] {
		return nil
	}
	file := fileForPos(pkg, pos)
	if ignoredPos(pkg.Fset, file, pos) {
		return nil
	}
	k := kindOf(obj)
	name := obj.Name()
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			name = recvPrefix(sig.Recv()) + "." + fn.Name()
		}
	}
	return &finding.Finding{
		Language: finding.Go,
		Kind:     k,
		Path:     relPath(p.Filename, moduleDir),
		Line:     p.Line,
		Column:   p.Column,
		Name:     name,
		Message:  messageOf(k, name),
	}
}

func recvPrefix(v *types.Var) string {
	if v == nil {
		return ""
	}
	t := v.Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return types.TypeString(t, func(*types.Package) string { return "" })
}

func collectInterfaces(pkgs []*packages.Package) []*types.Interface {
	var out []*types.Interface
	seen := map[string]bool{}
	add := func(t types.Type) {
		if t == nil {
			return
		}
		t = t.Underlying()
		iface, ok := t.(*types.Interface)
		if !ok || iface.Empty() {
			return
		}
		key := iface.String()
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, iface)
	}
	addFromObj := func(obj types.Object) {
		if obj == nil {
			return
		}
		add(obj.Type())
		if fn, ok := obj.(*types.Func); ok {
			sig, _ := fn.Type().(*types.Signature)
			if sig == nil {
				return
			}
			if p := sig.Params(); p != nil {
				for i := 0; i < p.Len(); i++ {
					add(p.At(i).Type())
				}
			}
			if r := sig.Results(); r != nil {
				for i := 0; i < r.Len(); i++ {
					add(r.At(i).Type())
				}
			}
		}
	}
	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if tn, ok := obj.(*types.TypeName); ok && tn.Type() != nil {
				add(tn.Type())
			}
		}
		if pkg.TypesInfo == nil {
			continue
		}
		for _, tv := range pkg.TypesInfo.Types {
			add(tv.Type)
		}
		for _, obj := range pkg.TypesInfo.Uses {
			addFromObj(obj)
		}
		for _, obj := range pkg.TypesInfo.Defs {
			addFromObj(obj)
		}
	}
	return out
}

func objectID(obj types.Object) string {
	if obj == nil {
		return ""
	}
	pkg := obj.Pkg()
	path := ""
	if pkg != nil {
		path = normalizePkgPath(pkg.Path())
	}
	if fn, ok := obj.(*types.Func); ok {
		if sig, _ := fn.Type().(*types.Signature); sig != nil && sig.Recv() != nil {
			return path + ":" + recvPrefix(sig.Recv()) + "." + fn.Name()
		}
	}
	return path + ":" + obj.Name()
}

func normalizePkgPath(path string) string {
	path = strings.TrimSuffix(path, ".test")
	return path
}

func isGeneratedTestPkg(p *packages.Package) bool {
	if strings.HasSuffix(p.PkgPath, ".test") {
		return true
	}
	if strings.HasSuffix(p.ID, ".test") && !strings.Contains(p.ID, "[") {
		return true
	}
	return false
}

// implementsUsedInterface reports whether fn is required to satisfy an
// interface that actually appears in the program (so a missing use of the
// method name is not enough to call it dead).
func implementsUsedInterface(fn *types.Func, named *types.Named, ifaces []*types.Interface) bool {
	ptr := types.NewPointer(named)
	for _, iface := range ifaces {
		if !types.Implements(named, iface) && !types.Implements(ptr, iface) {
			continue
		}
		for i := 0; i < iface.NumMethods(); i++ {
			m := iface.Method(i)
			if m.Name() != fn.Name() {
				continue
			}
			if types.Identical(m.Type(), fn.Type()) || signatureCompatible(m, fn) {
				return true
			}
		}
	}
	return false
}

func signatureCompatible(ifaceMeth, impl *types.Func) bool {
	a, ok1 := ifaceMeth.Type().(*types.Signature)
	b, ok2 := impl.Type().(*types.Signature)
	if !ok1 || !ok2 {
		return false
	}
	// Compare without the receiver.
	return types.Identical(
		types.NewSignatureType(nil, nil, nil, a.Params(), a.Results(), a.Variadic()),
		types.NewSignatureType(nil, nil, nil, b.Params(), b.Results(), b.Variadic()),
	)
}
