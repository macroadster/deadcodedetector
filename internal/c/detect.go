package c

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Detect reports unused C functions, variables, macros, includes, files,
// and unused dynamic-library exports.
func Detect(root string, files []walk.File, entries []string) ([]finding.Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var cFiles []walk.File
	var walkDylibs []walkFile
	for _, f := range files {
		switch f.Lang {
		case "c":
			cFiles = append(cFiles, f)
		case "dylib":
			walkDylibs = append(walkDylibs, walkFile{Abs: f.Abs, Rel: f.Rel, Lang: f.Lang})
		}
	}
	if len(cFiles) == 0 {
		return nil, nil
	}

	byAbs := map[string]*unit{}
	var headers []string
	for i := range cFiles {
		f := cFiles[i]
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			return nil, err
		}
		if generatedC(src) {
			continue
		}
		ex := extract(src)
		ex.IsHeader = isHeader(f.Rel)
		u := &unit{File: f, Extracted: ex, src: src}
		byAbs[f.Abs] = u
		if ex.IsHeader {
			headers = append(headers, f.Abs)
		}
	}
	if len(byAbs) == 0 {
		return nil, nil
	}

	res := newResolver(root, headers)
	for _, u := range byAbs {
		for i := range u.Includes {
			inc := &u.Includes[i]
			inc.resolved = res.Resolve(u.File.Abs, inc.Path, inc.System)
		}
	}

	dylibs := findDylibs(root, walkDylibs)
	entrySet := discoverEntries(root, byAbs, entries)
	libProject := len(dylibs) > 0 || looksLikeSharedLibProject(root, byAbs) || hasDllexport(byAbs)
	if libProject {
		// Every lib/*.c is a build-selected TU, not an orphan file.
		for abs, u := range byAbs {
			if strings.HasPrefix(filepath.ToSlash(u.File.Rel), "lib/") && !u.IsHeader {
				entrySet[abs] = true
			}
		}
	}
	dynSyms, dynLibs, indirect := collectDynamic(byAbs)
	liveFiles := markLive(byAbs, entrySet)

	hasMain := false
	for _, u := range byAbs {
		if u.HasMain {
			hasMain = true
			break
		}
	}
	// Report unused non-static internals when something in-tree runs.
	// Public API declared under include/ is kept even if a CLI main exists
	// (curl's src/tool vs libcurl).
	reportExports := hasMain || len(dynSyms) > 0 || (len(entrySet) > 0 && !libProject)
	publicAPI := publicAPINames(byAbs)
	pastePre, pasteSuf := collectPaste(byAbs)

	globalUses := projectUses(byAbs)
	// Dynamic lookups keep the named symbol.
	for _, s := range dynSyms {
		globalUses[s]++
	}
	if indirect {
		// Prefer FN: any exported name might be loaded by a computed string.
		for _, u := range byAbs {
			for _, d := range u.Decls {
				if !d.Static && d.Kind == "function" {
					globalUses[d.Name]++
				}
			}
		}
		for _, lib := range dylibs {
			for _, s := range lib.Exports {
				globalUses[s]++
			}
		}
	}

	var fs []finding.Finding
	if len(entrySet) > 0 {
		for abs, u := range byAbs {
			if liveFiles[abs] || u.IgnoreAll {
				continue
			}
			// .c files are chosen by the build (optional TLS backends,
			// platform ports, per-test binaries). Prefer FN: only report
			// unused headers, plus stray .c next to the app.
			if !u.IsHeader && skipUnusedCFile(u.File.Rel) {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.C,
				Kind:     finding.UnusedFile,
				Path:     u.File.Rel,
				Line:     1,
				Column:   1,
				Name:     u.File.Rel,
				Message:  fmt.Sprintf("unused file %s", u.File.Rel),
			})
		}
	}

	for abs, u := range byAbs {
		if u.IgnoreAll {
			continue
		}
		live := liveFiles[abs] || len(entrySet) == 0
		if !live {
			continue
		}
		fs = append(fs, unusedIncludes(u, byAbs)...)
		fs = append(fs, unusedMacros(u, byAbs, pastePre, pasteSuf)...)
		fs = append(fs, unusedDecls(u, globalUses, reportExports, libProject, publicAPI)...)
	}

	fs = append(fs, unusedDylibExports(dylibs, byAbs, globalUses, dynSyms, dynLibs, indirect)...)
	return fs, nil
}

type unit struct {
	File walk.File
	Extracted
	src []byte
}

func isHeader(rel string) bool {
	return strings.HasSuffix(strings.ToLower(rel), ".h")
}

func discoverEntries(root string, byAbs map[string]*unit, extra []string) map[string]bool {
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
	for abs, u := range byAbs {
		if u.HasMain || u.HasTest {
			add(abs)
		}
		if isTestFile(u.File.Rel) || isHarnessTestFile(u.File.Rel) {
			add(abs)
		}
		if isEntryName(filepath.Base(u.File.Rel)) {
			add(abs)
		}
		for _, d := range u.Decls {
			if d.Keep {
				add(abs)
				break
			}
		}
	}
	return out
}

func isTestFile(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return strings.HasPrefix(name, "test_") ||
		strings.HasSuffix(name, "_test") ||
		strings.HasSuffix(name, "_tests") ||
		strings.HasSuffix(name, "_spec")
}

// curl libtest/unit convention: lib1500.c, unit1300.c, cli_h2_pausing.c
func isHarnessTestFile(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.HasPrefix(name, "cli_") {
		return true
	}
	return harnessNumName(name, "lib") || harnessNumName(name, "unit")
}

func harnessNumName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	if rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isEntryName(base string) bool {
	switch strings.ToLower(base) {
	case "main.c", "wmain.c", "winmain.c", "app.c", "program.c":
		return true
	}
	return false
}

func markLive(byAbs map[string]*unit, entries map[string]bool) map[string]bool {
	live := map[string]bool{}
	var queue []string
	for e := range entries {
		queue = append(queue, e)
	}
	if len(queue) == 0 {
		for abs := range byAbs {
			live[abs] = true
		}
		return live
	}
	for len(queue) > 0 {
		abs := queue[0]
		queue = queue[1:]
		if live[abs] {
			continue
		}
		live[abs] = true
		u, ok := byAbs[abs]
		if !ok {
			continue
		}
		for _, inc := range u.Includes {
			if inc.resolved == "" || live[inc.resolved] {
				continue
			}
			queue = append(queue, inc.resolved)
		}
		// A .c file that defines a symbol used from a live file is live.
		// Done in a second pass below so order does not matter.
	}
	// Pull in translation units whose exported symbols are referenced from live files.
	used := map[string]bool{}
	for abs, u := range byAbs {
		if !live[abs] {
			continue
		}
		for _, use := range u.Uses {
			used[use.Name] = true
		}
		for _, s := range u.DynSyms {
			used[s] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for abs, u := range byAbs {
			if live[abs] || u.IgnoreAll {
				continue
			}
			for _, d := range u.Decls {
				if d.Static || d.Kind == "type" {
					continue
				}
				if used[d.Name] || d.Keep {
					live[abs] = true
					changed = true
					for _, use := range u.Uses {
						used[use.Name] = true
					}
					for _, inc := range u.Includes {
						if inc.resolved != "" && !live[inc.resolved] {
							live[inc.resolved] = true
							changed = true
						}
					}
					break
				}
			}
		}
	}
	return live
}

func collectDynamic(byAbs map[string]*unit) (syms []string, libs []string, indirect bool) {
	seenS, seenL := map[string]bool{}, map[string]bool{}
	for _, u := range byAbs {
		if u.HasIndirectDlsym {
			indirect = true
		}
		for _, s := range u.DynSyms {
			if s != "" && !seenS[s] {
				seenS[s] = true
				syms = append(syms, s)
			}
		}
		for _, s := range u.DynLibs {
			if s != "" && !seenL[s] {
				seenL[s] = true
				libs = append(libs, s)
			}
		}
	}
	return syms, libs, indirect
}

func projectUses(byAbs map[string]*unit) map[string]int {
	out := map[string]int{}
	for _, u := range byAbs {
		for _, use := range u.Uses {
			if use.Name != "" {
				out[use.Name]++
			}
		}
		for _, s := range u.Strings {
			if isIdentString(s) {
				out[s]++
			}
		}
	}
	return out
}

func hasDllexport(byAbs map[string]*unit) bool {
	for _, u := range byAbs {
		for _, d := range u.Decls {
			if d.Exported {
				return true
			}
		}
	}
	return false
}

func unusedIncludes(u *unit, byAbs map[string]*unit) []finding.Finding {
	// Library TUs include headers for types/macros the tokenizer cannot
	// prove. Prefer FN on lib/include; still report stray app includes.
	rel := filepath.ToSlash(u.File.Rel)
	if strings.HasPrefix(rel, "lib/") || strings.HasPrefix(rel, "include/") ||
		strings.HasPrefix(rel, "tests/") || isConfigHeader(rel) {
		return nil
	}
	local := localUseCount(u)
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, inc := range u.Includes {
		if inc.System || inc.resolved == "" || inc.Path == "" {
			continue
		}
		if seen[inc.resolved] {
			continue
		}
		seen[inc.resolved] = true
		if lineHasIgnore(u.src, inc.Line) {
			continue
		}
		h, ok := byAbs[inc.resolved]
		if !ok || h.IgnoreAll {
			continue
		}
		// Public / barrel headers are the install surface — prefer FN.
		if isPublicHeader(u.File.Rel) || isPublicHeader(h.File.Rel) || len(h.Includes) > 0 {
			continue
		}
		defined := map[string]bool{}
		for _, d := range u.Decls {
			if d.Name != "" {
				defined[d.Name] = true
			}
		}
		names := 0
		used := false
		for _, d := range h.Decls {
			if d.Name == "" {
				continue
			}
			names++
			if local[d.Name] > 0 || defined[d.Name] {
				used = true
			}
		}
		for _, m := range h.Macros {
			if m.Guard || m.Name == "" {
				continue
			}
			names++
			if local[m.Name] > 0 || defined[m.Name] {
				used = true
			}
		}
		if names == 0 || used {
			continue
		}
		fs = append(fs, finding.Finding{
			Language: finding.C,
			Kind:     finding.UnusedImport,
			Path:     u.File.Rel,
			Line:     inc.Line,
			Column:   inc.Col,
			Name:     inc.Path,
			Message:  fmt.Sprintf("unused include %s", inc.Path),
		})
	}
	return fs
}

func unusedMacros(u *unit, byAbs map[string]*unit, pastePre, pasteSuf []string) []finding.Finding {
	if isPublicHeader(u.File.Rel) || isConfigHeader(u.File.Rel) {
		return nil
	}
	// Count uses in every file. A reference from a test or optional TU
	// still means the macro is not dead (prefer FN).
	uses := map[string]int{}
	for _, other := range byAbs {
		for _, use := range other.Uses {
			uses[use.Name]++
		}
	}
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, m := range u.Macros {
		if m.Ignored || m.Guard || m.Name == "" {
			continue
		}
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		if isReservedMacro(m.Name) {
			continue
		}
		if uses[m.Name] > 0 {
			continue
		}
		if pasteKeeps(m.Name, pastePre, pasteSuf) {
			continue
		}
		if lineHasIgnore(u.src, m.Line) {
			continue
		}
		fs = append(fs, finding.Finding{
			Language: finding.C,
			Kind:     finding.UnusedMacro,
			Path:     u.File.Rel,
			Line:     m.Line,
			Column:   m.Col,
			Name:     m.Name,
			Message:  fmt.Sprintf("unused macro %s", m.Name),
		})
	}
	return fs
}

func unusedDecls(u *unit, global map[string]int, reportExports, libProject bool, publicAPI map[string]bool) []finding.Finding {
	local := localUseCount(u)
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, d := range u.Decls {
		if d.Ignored || d.Keep || d.Kind == "type" {
			continue
		}
		if seen[d.Kind+":"+d.Name] {
			continue
		}
		seen[d.Kind+":"+d.Name] = true
		if local[d.Name] > 0 || global[d.Name] > 0 {
			continue
		}
		if d.Kind == "var" && isAttrMacroName(d.Name) {
			continue
		}
		if lineHasIgnore(u.src, d.Line) {
			continue
		}
		if (isTestFile(u.File.Rel) || isHarnessTestFile(u.File.Rel) || u.HasTest) && looksLikeTestName(d.Name) {
			continue
		}
		if !d.Static {
			if publicAPI[d.Name] {
				continue
			}
			if !reportExports && !d.Exported {
				continue
			}
			if libProject && !reportExports {
				continue
			}
		}
		kind := finding.UnusedFunction
		label := d.Kind
		switch d.Kind {
		case "var":
			kind = finding.UnusedVar
		case "function":
			if !d.Static {
				kind = finding.UnusedExport
				label = "function"
			}
		}
		fs = append(fs, finding.Finding{
			Language: finding.C,
			Kind:     kind,
			Path:     u.File.Rel,
			Line:     d.Line,
			Column:   d.Col,
			Name:     d.Name,
			Message:  fmt.Sprintf("unused %s %s", label, d.Name),
		})
	}
	return fs
}

func publicAPINames(byAbs map[string]*unit) map[string]bool {
	out := map[string]bool{}
	for _, u := range byAbs {
		if !isPublicHeader(u.File.Rel) {
			continue
		}
		for _, d := range u.Decls {
			if d.Name != "" {
				out[d.Name] = true
			}
		}
	}
	return out
}

func collectPaste(byAbs map[string]*unit) (prefixes, suffixes []string) {
	seenP, seenS := map[string]bool{}, map[string]bool{}
	for _, u := range byAbs {
		for _, p := range u.PastePrefixes {
			if p == "" || seenP[p] {
				continue
			}
			seenP[p] = true
			prefixes = append(prefixes, p)
		}
		for _, s := range u.PasteSuffixes {
			if s == "" || seenS[s] {
				continue
			}
			seenS[s] = true
			suffixes = append(suffixes, s)
		}
	}
	return prefixes, suffixes
}

func pasteKeeps(name string, prefixes, suffixes []string) bool {
	for _, p := range prefixes {
		if len(p) >= 3 && strings.HasPrefix(name, p) {
			return true
		}
		if strings.HasSuffix(p, "_") && strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, s := range suffixes {
		if len(s) >= 2 && strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func isPublicHeader(rel string) bool {
	rel = filepath.ToSlash(rel)
	return strings.HasPrefix(rel, "include/") || strings.HasPrefix(rel, "inc/")
}

func isConfigHeader(rel string) bool {
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "CMake/") || strings.HasPrefix(rel, "cmake/") {
		return true
	}
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasPrefix(base, "config-"), strings.HasPrefix(base, "setup-"):
		return true
	case base == "config.h" || base == "curl_config.h" ||
		base == "curl_setup.h" || base == "tool_setup.h":
		return true
	}
	return false
}

func skipUnusedCFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	slash := strings.Index(rel, "/")
	if slash < 0 {
		return false
	}
	switch rel[:slash] {
	case "lib", "tests", "projects", "docs", "packages":
		return true
	}
	return false
}

func unusedDylibExports(libs []dylibInfo, byAbs map[string]*unit, global map[string]int, dynSyms, dynLibs []string, indirect bool) []finding.Finding {
	if indirect || len(libs) == 0 {
		return nil
	}
	// Only report binary-level unused exports when something in the tree
	// actually loads a library. Otherwise every system .so would flood.
	if len(dynSyms) == 0 && len(dynLibs) == 0 && !hasDlopenUse(byAbs) {
		return nil
	}
	defined := map[string]bool{}
	for _, u := range byAbs {
		for _, d := range u.Decls {
			if d.Kind == "function" || d.Kind == "var" {
				defined[d.Name] = true
			}
		}
	}
	loaded := map[string]bool{}
	for _, p := range dynLibs {
		loaded[filepath.Base(p)] = true
		loaded[p] = true
	}
	restrict := len(loaded) > 0
	dynSet := map[string]bool{}
	for _, s := range dynSyms {
		dynSet[s] = true
	}

	var fs []finding.Finding
	for _, lib := range libs {
		if restrict && !dylibLoaded(lib, loaded) {
			// Still consider the lib if none of the dlopen paths matched
			// anything — then check every lib (already handled by restrict
			// only when we have names). If we have names and this isn't one,
			// skip to avoid reporting libc exports.
			continue
		}
		seen := map[string]bool{}
		for _, name := range lib.Exports {
			if seen[name] || reservedExport(name) {
				continue
			}
			seen[name] = true
			if global[name] > 0 || dynSet[name] {
				continue
			}
			// Source already reports this symbol with a line number.
			if defined[name] {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.C,
				Kind:     finding.UnusedExport,
				Path:     lib.Rel,
				Line:     1,
				Column:   1,
				Name:     name,
				Message:  fmt.Sprintf("unused dynamic export %s", name),
			})
		}
	}
	return fs
}

func dylibLoaded(lib dylibInfo, loaded map[string]bool) bool {
	base := filepath.Base(lib.Rel)
	if loaded[base] || loaded[lib.Rel] || loaded[lib.Abs] {
		return true
	}
	// dlopen("libplugin.so") vs libplugin.dylib on macOS.
	stem := trimLibExt(base)
	for k := range loaded {
		if trimLibExt(filepath.Base(k)) == stem {
			return true
		}
	}
	return false
}

func trimLibExt(name string) string {
	low := strings.ToLower(name)
	for _, suf := range []string{".dylib", ".dll", ".so"} {
		if strings.HasSuffix(low, suf) {
			return name[:len(name)-len(suf)]
		}
	}
	if i := strings.Index(low, ".so."); i >= 0 {
		return name[:i]
	}
	return name
}

func hasDlopenUse(byAbs map[string]*unit) bool {
	for _, u := range byAbs {
		if len(u.DynLibs) > 0 || len(u.DynSyms) > 0 {
			return true
		}
		for _, use := range u.Uses {
			switch use.Name {
			case "dlopen", "dlsym", "LoadLibrary", "GetProcAddress":
				return true
			}
		}
	}
	return false
}

func localUseCount(u *unit) map[string]int {
	out := map[string]int{}
	for _, use := range u.Uses {
		if use.Name != "" {
			out[use.Name]++
		}
	}
	return out
}

func looksLikeTestName(name string) bool {
	low := strings.ToLower(name)
	return strings.HasPrefix(low, "test") || strings.HasPrefix(low, "check_")
}
