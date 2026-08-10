package javascript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Detect reports unused files, exports, imports, and top-level locals.
func Detect(root string, files []walk.File, entries []string) ([]finding.Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var jsFiles []walk.File
	var htmlFiles []walk.File
	for _, f := range files {
		switch f.Lang {
		case "js":
			jsFiles = append(jsFiles, f)
		case "html":
			htmlFiles = append(htmlFiles, f)
		}
	}
	if len(jsFiles) == 0 {
		return nil, nil
	}

	absList := make([]string, 0, len(jsFiles))
	byAbs := map[string]*module{}
	for i := range jsFiles {
		absList = append(absList, jsFiles[i].Abs)
	}
	res := newResolver(root, absList)

	for i := range jsFiles {
		f := jsFiles[i]
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			return nil, err
		}
		if generatedJS(src) {
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
			imp.resolved = res.Resolve(m.File.Abs, imp.Specifier)
		}
		for _, spec := range m.DynamicSpecs {
			if abs := res.Resolve(m.File.Abs, spec); abs != "" {
				m.dynamicResolved = append(m.dynamicResolved, abs)
			}
		}
	}

	entrySet := discoverEntries(root, byAbs, res, entries, htmlFiles)
	liveFiles, usedExports := markLive(byAbs, entrySet)

	var fs []finding.Finding
	// Unused files (only when we actually found entries).
	if len(entrySet) > 0 {
		for abs, m := range byAbs {
			if liveFiles[abs] || m.IgnoreAll {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.JavaScript,
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
			continue // unused file already reported; skip inner noise
		}
		// Entry files are public surface — do not flag their exports.
		if !entrySet[abs] {
			fs = append(fs, unusedExports(m, usedExports[abs])...)
		}
		fs = append(fs, unusedImportsAndLocals(m)...)
	}
	return fs, nil
}

type module struct {
	File walk.File
	Extracted
	src             []byte
	dynamicResolved []string
}

func generatedJS(src []byte) bool {
	head := src
	if len(head) > 512 {
		head = head[:512]
	}
	s := string(head)
	return strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT") ||
		strings.Contains(s, "@generated") ||
		strings.Contains(s, "AUTO-GENERATED")
}

func discoverEntries(root string, byAbs map[string]*module, res *Resolver, extra []string, html []walk.File) map[string]bool {
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
			// allow extension-less extra entries
			if _, ok := byAbs[a]; !ok {
				add(res.resolveFrom(filepath.Dir(a), filepath.Base(a)))
			}
		}
	}
	for _, e := range res.PackageEntries() {
		add(e)
	}
	// HTML <script src>
	for _, h := range html {
		src, err := os.ReadFile(h.Abs)
		if err != nil {
			continue
		}
		for _, spec := range htmlScriptSrc(string(src)) {
			add(res.Resolve(h.Abs, spec))
		}
	}
	// Conventional names if nothing else.
	if len(out) == 0 {
		for _, cand := range []string{
			"index.js", "index.ts", "index.jsx", "index.tsx", "index.mjs",
			"src/index.js", "src/index.ts", "src/index.jsx", "src/index.tsx",
			"src/main.js", "src/main.ts", "src/main.tsx",
			"src/app.js", "src/app.ts", "src/app.tsx",
			"main.js", "main.ts", "app.js", "app.ts",
			"src/index.mjs",
		} {
			add(res.resolveFrom(root, cand))
		}
	}
	return out
}

func htmlScriptSrc(s string) []string {
	var out []string
	low := strings.ToLower(s)
	for {
		i := strings.Index(low, "<script")
		if i < 0 {
			break
		}
		end := strings.Index(low[i:], ">")
		if end < 0 {
			break
		}
		tag := s[i : i+end]
		if spec, ok := attrValue(tag, "src"); ok {
			out = append(out, spec)
		}
		low = low[i+end+1:]
		s = s[i+end+1:]
	}
	return out
}

func attrValue(tag, name string) (string, bool) {
	low := strings.ToLower(tag)
	key := strings.ToLower(name) + "="
	i := strings.Index(low, key)
	if i < 0 {
		return "", false
	}
	rest := tag[i+len(key):]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	q := rest[0]
	if q == '"' || q == '\'' {
		j := strings.IndexByte(rest[1:], q)
		if j < 0 {
			return "", false
		}
		return rest[1 : 1+j], true
	}
	j := strings.IndexAny(rest, " \t\n>")
	if j < 0 {
		return rest, true
	}
	return rest[:j], true
}

func markLive(byAbs map[string]*module, entries map[string]bool) (live map[string]bool, usedExports map[string]map[string]bool) {
	live = map[string]bool{}
	usedExports = map[string]map[string]bool{}
	var queue []string
	for e := range entries {
		queue = append(queue, e)
	}
	// If no entries, every file is considered live for inner analysis.
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
			if imp.TypeOnly || imp.resolved == "" {
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
			if imp.SideEffect {
				continue
			}
			if imp.Default != "" {
				ue["default"] = true
			}
			if imp.Namespace != "" || imp.CJS {
				// Namespace / CJS: mark members actually accessed, or all if unknown.
				markNamespaceUses(m, imp, ue)
			}
			for _, n := range imp.Named {
				if n.TypeOnly {
					continue
				}
				ue[n.Remote] = true
			}
		}
		for _, d := range m.dynamicResolved {
			if !live[d] {
				queue = append(queue, d)
			}
			// Dynamic import: conservatively all exports are used.
			if usedExports[d] == nil {
				usedExports[d] = map[string]bool{}
			}
			usedExports[d]["*"] = true
		}
		if m.HasDynamicUnknown {
			// Cannot see the target; do not mark extra files.
		}
	}
	return live, usedExports
}

func markNamespaceUses(m *module, imp Import, ue map[string]bool) {
	local := imp.Namespace
	if local == "" {
		local = imp.Default
	}
	if local == "" {
		ue["*"] = true
		return
	}
	any := false
	for _, u := range m.Uses {
		if u.Name == local {
			if u.Member != "" {
				ue[u.Member] = true
				any = true
			} else {
				// Passed around as a value: all exports may be used.
				ue["*"] = true
				return
			}
		}
	}
	if !any {
		// Imported namespace never referenced — exports stay unused;
		// the unused import is reported separately.
	}
}

func unusedExports(m *module, used map[string]bool) []finding.Finding {
	if used == nil {
		used = map[string]bool{}
	}
	if used["*"] {
		return nil
	}
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, e := range m.Exports {
		if e.TypeOnly || e.Ignored || e.Name == "*" {
			continue
		}
		if seen[e.Name] {
			continue
		}
		seen[e.Name] = true
		if used[e.Name] {
			continue
		}
		// Re-export from external module: still unused if nobody imports it.
		fs = append(fs, finding.Finding{
			Language: finding.JavaScript,
			Kind:     finding.UnusedExport,
			Path:     m.File.Rel,
			Line:     e.Line,
			Column:   e.Col,
			Name:     e.Name,
			Message:  fmt.Sprintf("unused export %s", e.Name),
		})
	}
	return fs
}

func unusedImportsAndLocals(m *module) []finding.Finding {
	useCount := map[string]int{}
	for _, u := range m.Uses {
		useCount[u.Name]++
	}
	var fs []finding.Finding
	imported := map[string]Import{}
	for _, imp := range m.Imports {
		if imp.TypeOnly {
			continue
		}
		if imp.Default != "" {
			imported[imp.Default] = imp
			if useCount[imp.Default] == 0 && !lineIgnored(m.src, imp.Line) {
				fs = append(fs, finding.Finding{
					Language: finding.JavaScript,
					Kind:     finding.UnusedImport,
					Path:     m.File.Rel,
					Line:     imp.Line,
					Column:   imp.Col,
					Name:     imp.Default,
					Message:  fmt.Sprintf("unused import %s", imp.Default),
				})
			}
		}
		if imp.Namespace != "" {
			imported[imp.Namespace] = imp
			if useCount[imp.Namespace] == 0 && !lineIgnored(m.src, imp.Line) {
				fs = append(fs, finding.Finding{
					Language: finding.JavaScript,
					Kind:     finding.UnusedImport,
					Path:     m.File.Rel,
					Line:     imp.Line,
					Column:   imp.Col,
					Name:     imp.Namespace,
					Message:  fmt.Sprintf("unused import %s", imp.Namespace),
				})
			}
		}
		for _, n := range imp.Named {
			if n.TypeOnly || n.Local == "" {
				continue
			}
			imported[n.Local] = imp
			if useCount[n.Local] == 0 && !lineIgnored(m.src, imp.Line) {
				fs = append(fs, finding.Finding{
					Language: finding.JavaScript,
					Kind:     finding.UnusedImport,
					Path:     m.File.Rel,
					Line:     imp.Line,
					Column:   imp.Col,
					Name:     n.Local,
					Message:  fmt.Sprintf("unused import %s", n.Local),
				})
			}
		}
	}

	seen := map[string]bool{}
	for _, d := range m.Decls {
		if d.Kind == "import" || d.Exported || d.Ignored {
			continue
		}
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if useCount[d.Name] > 0 {
			continue
		}
		if lineIgnored(m.src, d.Line) {
			continue
		}
		kind := finding.UnusedFunction
		switch d.Kind {
		case "class":
			kind = finding.UnusedType
		case "var", "enum":
			kind = finding.UnusedVar
		}
		fs = append(fs, finding.Finding{
			Language: finding.JavaScript,
			Kind:     kind,
			Path:     m.File.Rel,
			Line:     d.Line,
			Column:   d.Col,
			Name:     d.Name,
			Message:  fmt.Sprintf("unused %s %s", d.Kind, d.Name),
		})
	}
	return fs
}

func lineIgnored(src []byte, line int) bool {
	return lineHasIgnore(src, line)
}
