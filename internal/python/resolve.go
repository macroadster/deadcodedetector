package python

import (
	"path/filepath"
	"strings"
)

// Resolver maps Python import specifiers to files on disk under the scan root.
type Resolver struct {
	root  string
	files map[string]string // slash-rel path -> abs
	// byMod maps dotted module path -> abs (for package roots under scan root)
	byMod map[string]string
}

func newResolver(root string, absFiles []string) *Resolver {
	r := &Resolver{
		root:  root,
		files: map[string]string{},
		byMod: map[string]string{},
	}
	for _, abs := range absFiles {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		slash := filepath.ToSlash(rel)
		r.files[slash] = abs
		mod := strings.TrimSuffix(slash, ".py")
		if strings.HasSuffix(mod, "/__init__") {
			pkg := strings.TrimSuffix(mod, "/__init__")
			if pkg == "" {
				// top-level __init__.py: empty module name not useful
			} else {
				r.byMod[strings.ReplaceAll(pkg, "/", ".")] = abs
			}
		} else {
			r.byMod[strings.ReplaceAll(mod, "/", ".")] = abs
		}
	}
	return r
}

// Resolve returns the absolute path of a local module, or "" if external/unresolved.
func (r *Resolver) Resolve(fromAbs string, module string, level int) string {
	if level > 0 {
		return r.resolveRelative(fromAbs, module, level)
	}
	if module == "" {
		return ""
	}
	// Absolute from project root (scripts.foo, starlight.agents, …)
	if abs := r.byMod[module]; abs != "" {
		return abs
	}
	// Try as path under root
	if abs := r.tryPath(strings.ReplaceAll(module, ".", "/")); abs != "" {
		return abs
	}
	// Same-directory bare import (common in script folders: from dataset import X)
	if abs := r.resolveSameDir(fromAbs, module); abs != "" {
		return abs
	}
	// Parent-directory walk for partial package matches
	parts := strings.Split(module, ".")
	for i := 0; i < len(parts); i++ {
		// try suffix packages? skip — prefer FN
		_ = i
	}
	return ""
}

func (r *Resolver) resolveRelative(fromAbs string, module string, level int) string {
	rel, err := filepath.Rel(r.root, fromAbs)
	if err != nil {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		dir = ""
	}
	parts := splitSlash(dir)
	// level 1 = current package; level 2 = parent; etc.
	up := level - 1
	if up > len(parts) {
		return ""
	}
	base := parts[:len(parts)-up]
	var full string
	if module == "" {
		full = strings.Join(base, "/")
	} else {
		modParts := strings.Split(module, ".")
		full = strings.Join(append(base, modParts...), "/")
	}
	if full == "" {
		// project-root package
		if module == "" {
			if abs, ok := r.files["__init__.py"]; ok {
				return abs
			}
		}
		return r.tryPath(strings.ReplaceAll(module, ".", "/"))
	}
	return r.tryPath(full)
}

// ResolveSubmodule maps `from module import name` to a local submodule file.
// `from pkg import util` → pkg/util.py; `from . import util` → <pkg>/util.py.
func (r *Resolver) ResolveSubmodule(fromAbs, module string, level int, name string) string {
	if name == "" || name == "*" {
		return ""
	}
	if module == "" {
		return r.Resolve(fromAbs, name, level)
	}
	return r.Resolve(fromAbs, module+"."+name, level)
}

// ParentInits returns ancestor __init__.py files of a resolved module.
// Importing pkg.sub always executes pkg/__init__.py in CPython.
func (r *Resolver) ParentInits(abs string) []string {
	if abs == "" {
		return nil
	}
	rel, err := filepath.Rel(r.root, abs)
	if err != nil {
		return nil
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	var out []string
	for dir != "" && dir != "." {
		if init, ok := r.files[dir+"/__init__.py"]; ok && init != abs {
			out = append(out, init)
		}
		parent := filepath.ToSlash(filepath.Dir(dir))
		if parent == dir {
			break
		}
		dir = parent
	}
	if init, ok := r.files["__init__.py"]; ok && init != abs {
		out = append(out, init)
	}
	return out
}

func (r *Resolver) resolveSameDir(fromAbs, module string) string {
	rel, err := filepath.Rel(r.root, fromAbs)
	if err != nil {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	modPath := strings.ReplaceAll(module, ".", "/")
	if dir == "." || dir == "" {
		return r.tryPath(modPath)
	}
	return r.tryPath(dir + "/" + modPath)
}

func (r *Resolver) tryPath(modPath string) string {
	modPath = strings.TrimPrefix(modPath, "/")
	if modPath == "" {
		if abs, ok := r.files["__init__.py"]; ok {
			return abs
		}
		return ""
	}
	candidates := []string{
		modPath + ".py",
		modPath + "/__init__.py",
	}
	for _, c := range candidates {
		if abs, ok := r.files[c]; ok {
			return abs
		}
	}
	return ""
}

func splitSlash(s string) []string {
	if s == "" || s == "." {
		return nil
	}
	return strings.Split(s, "/")
}

// isTestFile reports whether rel path looks like a test module.
func isTestFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return true
	}
	if base == "conftest.py" {
		return true
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "tests" || p == "test" {
			return true
		}
	}
	return false
}

// isEntryName reports conventional script entry file names.
func isEntryName(base string) bool {
	switch base {
	case "__main__.py", "setup.py", "manage.py", "conftest.py", "app.py", "wsgi.py", "asgi.py":
		return true
	}
	return false
}
