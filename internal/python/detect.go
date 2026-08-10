package python

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Detect reports unused Python imports, functions, classes, variables, and files.
func Detect(root string, files []walk.File, entries []string) ([]finding.Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var pyFiles []walk.File
	for _, f := range files {
		if f.Lang == "py" || f.Lang == "python" {
			pyFiles = append(pyFiles, f)
		}
	}
	if len(pyFiles) == 0 {
		return nil, nil
	}

	absList := make([]string, 0, len(pyFiles))
	byAbs := map[string]*module{}
	for i := range pyFiles {
		absList = append(absList, pyFiles[i].Abs)
	}
	res := newResolver(root, absList)

	for i := range pyFiles {
		f := pyFiles[i]
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			return nil, err
		}
		if generatedPy(src) {
			continue
		}
		ex := extract(src)
		m := &module{
			File:      f,
			Extracted: ex,
			src:       src,
		}
		byAbs[f.Abs] = m
	}

	// Resolve imports.
	for _, m := range byAbs {
		for i := range m.Imports {
			imp := &m.Imports[i]
			imp.resolved = res.Resolve(m.File.Abs, imp.Module, imp.Level)
		}
	}

	entrySet := discoverEntries(root, byAbs, entries)
	liveFiles, usedExports := markLive(byAbs, entrySet)

	var fs []finding.Finding
	// Unused files — only when we have real entries.
	if len(entrySet) > 0 {
		for abs, m := range byAbs {
			if liveFiles[abs] || m.IgnoreAll {
				continue
			}
			// Empty __init__.py package markers are rarely "dead code".
			if filepath.Base(m.File.Rel) == "__init__.py" && isEffectivelyEmpty(m) {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.Python,
				Kind:     finding.UnusedFile,
				Path:     m.File.Rel,
				Line:     1,
				Column:   1,
				Name:     m.File.Rel,
				Message:  fmt.Sprintf("unused file %s", m.File.Rel),
			})
		}
	}

	for abs, m := range byAbs {
		if m.IgnoreAll {
			continue
		}
		live := liveFiles[abs] || len(entrySet) == 0
		if !live {
			continue // unused file already reported
		}
		// Unused exports (module-level public defs never imported elsewhere).
		// Only when we have an entry graph; prefer FN when isolated.
		if len(entrySet) > 0 {
			fs = append(fs, unusedExports(m, usedExports[abs], entrySet[abs])...)
		}
		fs = append(fs, unusedImportsAndLocals(m, usedExports[abs], entrySet[abs])...)
	}
	return fs, nil
}

type module struct {
	File walk.File
	Extracted
	src []byte
}

func isEffectivelyEmpty(m *module) bool {
	if len(m.Decls) > 0 {
		return false
	}
	// Only imports / docstring / __all__ style noise
	for _, d := range m.Decls {
		if !isDunder(d.Name) {
			return false
		}
	}
	// If it only re-exports via imports, it is meaningful — keep as live noise.
	// Empty means no decls and maybe only imports: still a package surface.
	// Treat as empty only when no imports either.
	return len(m.Imports) == 0
}

func discoverEntries(root string, byAbs map[string]*module, extra []string) map[string]bool {
	out := map[string]bool{}
	add := func(abs string) {
		if abs == "" {
			return
		}
		if _, ok := byAbs[abs]; ok {
			out[abs] = true
		}
	}
	for _, e := range extra {
		if !filepath.IsAbs(e) {
			e = filepath.Join(root, e)
		}
		if a, err := filepath.Abs(e); err == nil {
			add(a)
		}
	}
	for abs, m := range byAbs {
		if m.HasMain {
			add(abs)
		}
		if isTestFile(m.File.Rel) {
			add(abs)
		}
		if isEntryName(filepath.Base(m.File.Rel)) {
			add(abs)
		}
	}
	return out
}

func markLive(byAbs map[string]*module, entries map[string]bool) (live map[string]bool, usedExports map[string]map[string]bool) {
	live = map[string]bool{}
	usedExports = map[string]map[string]bool{}
	var queue []string
	for e := range entries {
		queue = append(queue, e)
	}
	if len(queue) == 0 {
		for abs := range byAbs {
			live[abs] = true
		}
	}

	for len(queue) > 0 {
		abs := queue[0]
		queue = queue[1:]
		if live[abs] {
			continue
		}
		live[abs] = true
		m, ok := byAbs[abs]
		if !ok {
			continue
		}
		if usedExports[abs] == nil {
			usedExports[abs] = map[string]bool{}
		}
		for _, imp := range m.Imports {
			if imp.resolved == "" {
				continue
			}
			if !live[imp.resolved] {
				queue = append(queue, imp.resolved)
			}
			ue := usedExports[imp.resolved]
			if ue == nil {
				ue = map[string]bool{}
				usedExports[imp.resolved] = ue
			}
			if imp.Star {
				ue["*"] = true
				continue
			}
			if imp.From {
				if imp.Name != "" {
					ue[imp.Name] = true
				}
			} else {
				// import mod — whole module object; attribute uses on local name
				// mark specific members if we can see them.
				markModuleAttrUses(m, imp, ue)
			}
		}
	}
	return live, usedExports
}

func markModuleAttrUses(m *module, imp Import, ue map[string]bool) {
	// import scripts.starlight_utils as u  → local is first component or alias
	local := imp.Local
	if local == "" {
		ue["*"] = true
		return
	}
	attrs := m.AttrUses[local]
	if len(attrs) == 0 {
		// Module imported but never used as attribute — may still be side-effect
		// or re-exported. Mark nothing as used for export purposes; unused import
		// is reported separately. Exception: `import pkg` then only referenced
		// as name is a module use, not symbol use.
		return
	}
	any := false
	for attr := range attrs {
		ue[attr] = true
		any = true
	}
	// Also check Uses with Member
	for _, u := range m.Uses {
		if u.Name == local && u.Member != "" {
			ue[u.Member] = true
			any = true
		}
	}
	if !any {
		// namespace imported and referenced without attr → all exports may be used
		for _, u := range m.Uses {
			if u.Name == local && u.Member == "" {
				ue["*"] = true
				return
			}
		}
	}
}

func unusedExports(m *module, used map[string]bool, isEntry bool) []finding.Finding {
	if used == nil {
		used = map[string]bool{}
	}
	if used["*"] {
		return nil
	}
	// Entry scripts' top-level public functions are often CLI helpers or
	// dynamically discovered; only report clearly private unused ones from entries.
	// For non-entry library modules, report public + private unused exports.
	inAll := map[string]bool{}
	for _, n := range m.All {
		inAll[n] = true
	}
	// Names referenced via strings in this file (dynamic) — keep.
	strRef := map[string]bool{}
	for _, s := range m.Strings {
		if s != "" && len(s) < 120 && isIdentString(s) {
			strRef[s] = true
		}
	}

	localUses := map[string]int{}
	for _, u := range m.Uses {
		localUses[u.Name]++
	}

	var fs []finding.Finding
	seen := map[string]bool{}
	for _, d := range m.Decls {
		if d.Ignored || isDunder(d.Name) {
			continue
		}
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if inAll[d.Name] || used[d.Name] || localUses[d.Name] > 0 || strRef[d.Name] {
			continue
		}
		// Test discovery surface
		if isTestFile(m.File.Rel) && looksLikeTestName(d.Name) {
			continue
		}
		// Prefer FN: for entry files, only report private unused helpers.
		if isEntry && !strings.HasPrefix(d.Name, "_") {
			continue
		}
		// Public names that are never imported: dead export.
		// Private names that are never used locally: dead local (handled in unusedImportsAndLocals too).
		// Here we only report symbols that look like API of a library module.
		if strings.HasPrefix(d.Name, "_") {
			// private: only if also unused locally (already checked localUses)
			// report as unused function/var, not export
			continue
		}
		kind := finding.UnusedExport
		msg := fmt.Sprintf("unused export %s", d.Name)
		// For Python, "export" = importable top-level name.
		fs = append(fs, finding.Finding{
			Language: finding.Python,
			Kind:     kind,
			Path:     m.File.Rel,
			Line:     d.Line,
			Column:   d.Col,
			Name:     d.Name,
			Message:  msg,
		})
	}
	return fs
}

func unusedImportsAndLocals(m *module, usedExports map[string]bool, isEntry bool) []finding.Finding {
	useCount := map[string]int{}
	for _, u := range m.Uses {
		useCount[u.Name]++
	}
	// Attribute-only uses also count as using the base import name.
	for base := range m.AttrUses {
		if useCount[base] == 0 {
			useCount[base] = 1
		}
	}
	if usedExports == nil {
		usedExports = map[string]bool{}
	}

	var fs []finding.Finding
	// Unused imports
	seenImp := map[string]bool{}
	for _, imp := range m.Imports {
		if imp.Star {
			continue
		}
		if imp.Local == "" {
			continue
		}
		if seenImp[imp.Local] {
			continue
		}
		if useCount[imp.Local] > 0 {
			continue
		}
		if lineHasIgnore(m.src, imp.Line) {
			continue
		}
		// __init__.py re-exports: import name listed in __all__ counts as used.
		if inStringList(m.All, imp.Local) || inStringList(m.All, imp.Name) {
			continue
		}
		// Prefer FN for package __init__ re-export surfaces.
		if filepath.Base(m.File.Rel) == "__init__.py" && isPublicLocal(imp) {
			continue
		}
		seenImp[imp.Local] = true
		fs = append(fs, finding.Finding{
			Language: finding.Python,
			Kind:     finding.UnusedImport,
			Path:     m.File.Rel,
			Line:     imp.Line,
			Column:   imp.Col,
			Name:     imp.Local,
			Message:  fmt.Sprintf("unused import %s", imp.Local),
		})
	}

	// Unused locals (module-level defs never referenced in this file).
	// Cross-module use is recorded in usedExports.
	// - private (_x): report if unused locally and not imported
	// - public: library modules via unusedExports; entry files here, unless imported
	seen := map[string]bool{}
	isTest := isTestFile(m.File.Rel)
	for _, d := range m.Decls {
		if d.Ignored || isDunder(d.Name) {
			continue
		}
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if useCount[d.Name] > 0 {
			continue
		}
		if usedExports[d.Name] || usedExports["*"] {
			continue
		}
		if lineHasIgnore(m.src, d.Line) {
			continue
		}
		if inStringList(m.All, d.Name) {
			continue
		}
		if isTest && looksLikeTestName(d.Name) {
			continue
		}
		if stringMentions(m.Strings, d.Name) {
			continue
		}
		priv := strings.HasPrefix(d.Name, "_")
		if !priv {
			if isEntry || m.HasMain || isTest || isEntryName(filepath.Base(m.File.Rel)) {
				// public unused in entry/test files
			} else {
				// library module: unusedExports handles public
				continue
			}
		}
		kind := finding.UnusedFunction
		label := d.Kind
		switch d.Kind {
		case "class":
			kind = finding.UnusedType
		case "var":
			kind = finding.UnusedVar
		}
		fs = append(fs, finding.Finding{
			Language: finding.Python,
			Kind:     kind,
			Path:     m.File.Rel,
			Line:     d.Line,
			Column:   d.Col,
			Name:     d.Name,
			Message:  fmt.Sprintf("unused %s %s", label, d.Name),
		})
	}
	return fs
}

func isPublicLocal(imp Import) bool {
	name := imp.Local
	if name == "" {
		name = imp.Name
	}
	return name != "" && !strings.HasPrefix(name, "_")
}

func inStringList(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

func stringMentions(strs []string, name string) bool {
	if name == "" || len(name) < 2 {
		return false
	}
	for _, s := range strs {
		if s == name {
			return true
		}
	}
	return false
}

func isIdentString(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
