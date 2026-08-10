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
			if imp.From && !imp.Star && imp.Name != "" {
				imp.resolvedSub = res.ResolveSubmodule(m.File.Abs, imp.Module, imp.Level, imp.Name)
				if imp.resolvedSub == imp.resolved {
					imp.resolvedSub = ""
				}
			}
		}
	}

	entrySet := discoverEntries(root, byAbs, entries)
	liveFiles, usedExports := markLive(byAbs, entrySet, res)

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
	for _, d := range m.Decls {
		if !isDunder(d.Name) {
			return false
		}
	}
	// Package marker or dunder-only noise. Re-export surfaces have imports.
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

func markLive(byAbs map[string]*module, entries map[string]bool, res *Resolver) (live map[string]bool, usedExports map[string]map[string]bool) {
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

	enqueue := func(abs string) {
		if abs == "" || live[abs] {
			return
		}
		queue = append(queue, abs)
	}
	ensureUE := func(abs string) map[string]bool {
		ue := usedExports[abs]
		if ue == nil {
			ue = map[string]bool{}
			usedExports[abs] = ue
		}
		return ue
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
		ensureUE(abs)
		for _, imp := range m.Imports {
			targets := make([]string, 0, 4)
			if imp.resolved != "" {
				targets = append(targets, imp.resolved)
			}
			if imp.resolvedSub != "" {
				targets = append(targets, imp.resolvedSub)
			}
			if res != nil {
				for _, t := range targets {
					for _, parent := range res.ParentInits(t) {
						enqueue(parent)
					}
				}
			}
			for _, t := range targets {
				enqueue(t)
			}
			if imp.resolved == "" && imp.resolvedSub == "" {
				continue
			}
			if imp.Star {
				if imp.resolved != "" {
					ensureUE(imp.resolved)["*"] = true
				}
				if imp.resolvedSub != "" {
					ensureUE(imp.resolvedSub)["*"] = true
				}
				continue
			}
			if imp.From {
				if imp.resolved != "" && imp.Name != "" {
					ensureUE(imp.resolved)[imp.Name] = true
				}
				if imp.resolvedSub != "" {
					// from pkg import util; util.helper() — helper is an export of util.
					markModuleAttrUses(m, Import{Local: imp.Local, Module: imp.Local}, ensureUE(imp.resolvedSub))
				}
				continue
			}
			if imp.resolved != "" {
				markModuleAttrUses(m, imp, ensureUE(imp.resolved))
			}
		}
	}
	return live, usedExports
}

func markModuleAttrUses(m *module, imp Import, ue map[string]bool) {
	// import scripts.starlight_utils as u  → local is alias
	// import pkg.util                     → local is first component
	local := imp.Local
	if local == "" {
		ue["*"] = true
		return
	}
	modParts := strings.Split(imp.Module, ".")
	aliased := imp.Module == "" || local != modParts[0]
	dotted := !aliased && len(modParts) > 1

	for _, u := range m.Uses {
		if u.Name != local {
			continue
		}
		chain := useChain(u)
		if dotted {
			remainder := modParts[1:]
			if len(chain) == 0 {
				// `import pkg.util` then only `pkg` is referenced — cannot see
				// which member of util is used. Prefer FN.
				ue["*"] = true
				return
			}
			if !hasStringPrefix(chain, remainder) {
				continue
			}
			extra := chain[len(remainder):]
			if len(extra) == 0 {
				ue["*"] = true
				return
			}
			ue[extra[0]] = true
			continue
		}
		if len(chain) == 0 {
			// Passed around as a value: all exports may be used.
			ue["*"] = true
			return
		}
		ue[chain[0]] = true
	}
}

func useChain(u Use) []string {
	if len(u.Chain) > 0 {
		return u.Chain
	}
	if u.Member != "" {
		return []string{u.Member}
	}
	return nil
}

func hasStringPrefix(chain, prefix []string) bool {
	if len(chain) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if chain[i] != p {
			return false
		}
	}
	return true
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
		if d.Ignored || d.Decorated || isDunder(d.Name) {
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
		if d.Ignored || d.Decorated || isDunder(d.Name) {
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
		base := filepath.Base(m.File.Rel)
		if !priv {
			// conftest hooks / fixtures are pytest discovery, not call-graph uses.
			if base == "conftest.py" {
				continue
			}
			if isEntry || m.HasMain || isTest || isEntryName(base) {
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
