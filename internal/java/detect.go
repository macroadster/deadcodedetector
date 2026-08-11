package java

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Detect reports unused Java files, imports, types, and members.
func Detect(root string, files []walk.File, entries []string) ([]finding.Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var javaFiles []walk.File
	for _, f := range files {
		if f.Lang == "java" {
			javaFiles = append(javaFiles, f)
		}
	}
	if len(javaFiles) == 0 {
		return nil, nil
	}

	byAbs := map[string]*unit{}
	for i := range javaFiles {
		f := javaFiles[i]
		if isSpecialFile(f.Rel) {
			continue
		}
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			return nil, err
		}
		if generatedJava(src) {
			continue
		}
		ex := extract(src)
		byAbs[f.Abs] = &unit{File: f, Extracted: ex, src: src}
	}
	if len(byAbs) == 0 {
		return nil, nil
	}

	res := newResolver(root, byAbs)
	for _, u := range byAbs {
		for i := range u.Imports {
			imp := &u.Imports[i]
			if imp.Star {
				// Star of a local package: resolve to any file in that package.
				if files := res.PackageFiles(imp.Qual); len(files) > 0 {
					imp.resolved = files[0]
				}
				continue
			}
			imp.resolved = res.ResolveFQCN(imp.Qual)
		}
	}

	entrySet := discoverEntries(root, byAbs, entries)
	dyn := collectDynamicRefs(res, byAbs, files)
	liveFiles, usedTypes := markLive(byAbs, res, entrySet, dyn)

	var fs []finding.Finding
	if len(entrySet) > 0 {
		for abs, u := range byAbs {
			if liveFiles[abs] || u.IgnoreAll {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.Java,
				Kind:     finding.UnusedFile,
				Path:     u.File.Rel,
				Line:     1,
				Column:   1,
				Name:     u.File.Rel,
				Message:  fmt.Sprintf("unused file %s", u.File.Rel),
			})
		}
	}

	globalUses := projectUses(byAbs)
	for abs, u := range byAbs {
		if u.IgnoreAll {
			continue
		}
		live := liveFiles[abs] || len(entrySet) == 0
		if !live {
			continue
		}
		if len(entrySet) > 0 {
			fs = append(fs, unusedTypes(u, usedTypes[abs], entrySet[abs])...)
			fs = append(fs, unusedExports(u, globalUses, entrySet[abs])...)
		}
		fs = append(fs, unusedImportsAndPrivates(u, globalUses)...)
	}
	return fs, nil
}

type unit struct {
	File walk.File
	Extracted
	src []byte
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
		if u.HasMain {
			add(abs)
		}
		if isTestFile(u.File.Rel) {
			add(abs)
		}
		if isEntryName(filepath.Base(u.File.Rel)) {
			add(abs)
		}
		for _, a := range u.TypeAnnotations {
			if isEntryAnnotation(a) {
				add(abs)
				break
			}
		}
		for _, td := range u.Types {
			for _, a := range td.Annotations {
				if isEntryAnnotation(a) {
					add(abs)
				}
			}
		}
		for _, mem := range u.Members {
			for _, a := range mem.Annotations {
				if isEntryMethodAnnotation(a) {
					add(abs)
				}
			}
		}
	}
	return out
}

type typeRef struct {
	abs  string
	name string
	all  bool // Class.forName / config: the type is used as a whole
}

func collectDynamicRefs(res *Resolver, byAbs map[string]*unit, files []walk.File) []typeRef {
	var out []typeRef
	seen := map[string]bool{}
	add := func(abs, name string, all bool) {
		if abs == "" {
			return
		}
		key := abs + "\x00" + name
		if all {
			key += "\x00*"
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, typeRef{abs: abs, name: name, all: all})
	}
	visit := func(s string) {
		for _, part := range splitTypeNames(s) {
			if abs := res.ResolveFQCN(part); abs != "" {
				add(abs, simpleName(part), true)
			}
		}
	}
	for _, u := range byAbs {
		for _, s := range u.Strings {
			visit(s)
		}
	}
	for _, f := range files {
		switch f.Lang {
		case "xml", "props", "html":
		default:
			continue
		}
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			continue
		}
		eachFQCNToken(src, visit)
	}
	return out
}

func splitTypeNames(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Class.forName / jobConf lists / XML <value>a,b</value>
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var out []string
	for _, p := range parts {
		p = strings.Trim(p, `"'<>/`)
		if strings.Contains(p, ".") {
			out = append(out, p)
		}
	}
	return out
}

func simpleName(qual string) string {
	if i := strings.LastIndex(qual, "."); i >= 0 {
		return qual[i+1:]
	}
	return qual
}

func eachFQCNToken(src []byte, fn func(string)) {
	i := 0
	for i < len(src) {
		if isIdentStart(src[i]) {
			start := i
			for i < len(src) && (isIdentContinue(src[i]) || src[i] == '.') {
				i++
			}
			tok := strings.Trim(string(src[start:i]), ".")
			if strings.Contains(tok, ".") {
				fn(tok)
			}
			continue
		}
		i++
	}
}

func markLive(byAbs map[string]*unit, res *Resolver, entries map[string]bool, extra []typeRef) (live map[string]bool, usedTypes map[string]map[string]bool) {
	live = map[string]bool{}
	usedTypes = map[string]map[string]bool{}
	var queue []string
	for e := range entries {
		queue = append(queue, e)
	}

	enqueue := func(abs string) {
		if abs == "" || live[abs] {
			return
		}
		// Still allow adding to queue if not yet processed; live is set when popped.
		queue = append(queue, abs)
	}
	ensure := func(abs string) map[string]bool {
		m := usedTypes[abs]
		if m == nil {
			m = map[string]bool{}
			usedTypes[abs] = m
		}
		return m
	}

	for _, r := range extra {
		enqueue(r.abs)
		if r.all {
			ensure(r.abs)["*"] = true
		}
		if r.name != "" {
			ensure(r.abs)[r.name] = true
		}
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
		u, ok := byAbs[abs]
		if !ok {
			continue
		}
		ensure(abs)

		for _, imp := range u.Imports {
			if imp.Star {
				pkg := imp.Qual
				for _, t := range res.PackageFiles(pkg) {
					enqueue(t)
					ensure(t)["*"] = true
				}
				continue
			}
			markQualUsed(res, imp.Qual, enqueue, ensure)
		}

		// Same-package and FQCN references.
		for _, use := range u.Uses {
			if target := resolveUse(res, u, use); target != "" && target != abs {
				enqueue(target)
				if name := typeNameOfUse(res, u, use, target); name != "" {
					ensure(target)[name] = true
				} else {
					ensure(target)["*"] = true
				}
			}
		}

		// Class.forName("a.b.C") and other FQCN string literals in this file.
		for _, s := range u.Strings {
			for _, part := range splitTypeNames(s) {
				if target := res.ResolveFQCN(part); target != "" {
					enqueue(target)
					ensure(target)[simpleName(part)] = true
					ensure(target)["*"] = true
				}
			}
		}
	}
	return live, usedTypes
}

func markQualUsed(res *Resolver, qual string, enqueue func(string), ensure func(string) map[string]bool) {
	if qual == "" {
		return
	}
	parts := strings.Split(qual, ".")
	for n := len(parts); n >= 1; n-- {
		prefix := strings.Join(parts[:n], ".")
		abs := res.ResolveFQCN(prefix)
		if abs == "" {
			continue
		}
		enqueue(abs)
		ensure(abs)[parts[n-1]] = true
	}
}

func resolveUse(res *Resolver, u *unit, use Use) string {
	// FQCN: com.example.Foo or com.example.Foo.member
	if len(use.Chain) > 0 {
		parts := append([]string{use.Name}, use.Chain...)
		for n := len(parts); n >= 2; n-- {
			qual := strings.Join(parts[:n], ".")
			if abs := res.ResolveFQCN(qual); abs != "" {
				return abs
			}
		}
	}
	// Imported simple name.
	for _, imp := range u.Imports {
		if imp.Star || imp.Static {
			continue
		}
		if use.Name == imp.Name && imp.resolved != "" {
			return imp.resolved
		}
	}
	// Same package.
	if abs := res.ResolveSimple(u.Package, use.Name); abs != "" {
		return abs
	}
	return ""
}

func typeNameOfUse(res *Resolver, u *unit, use Use, target string) string {
	if names := res.byPkg[u.Package]; names != nil && names[use.Name] == target {
		return use.Name
	}
	for _, imp := range u.Imports {
		if use.Name == imp.Name && imp.resolved == target {
			return imp.Name
		}
	}
	if len(use.Chain) > 0 {
		parts := append([]string{use.Name}, use.Chain...)
		for n := len(parts); n >= 1; n-- {
			simple := parts[n-1]
			if abs := res.ResolveFQCN(strings.Join(parts[:n], ".")); abs == target {
				return simple
			}
			if abs := res.ResolveSimple(u.Package, simple); abs == target {
				return simple
			}
		}
	}
	return use.Name
}

func projectUses(byAbs map[string]*unit) map[string]int {
	out := map[string]int{}
	for _, u := range byAbs {
		for _, use := range u.Uses {
			if use.Name != "" {
				out[use.Name]++
			}
			if use.Member != "" {
				out[use.Member]++
			}
			for _, c := range use.Chain {
				out[c]++
			}
		}
	}
	return out
}

func unusedTypes(u *unit, used map[string]bool, isEntry bool) []finding.Finding {
	if used == nil {
		used = map[string]bool{}
	}
	if used["*"] || isEntry {
		return nil
	}
	local := localUseCount(u)
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, td := range u.Types {
		if td.Ignored || seen[td.Name] {
			continue
		}
		seen[td.Name] = true
		if used[td.Name] || local[td.Name] > 0 {
			continue
		}
		if stringMentions(u.Strings, td.Name) {
			continue
		}
		// import Outer.Nested (or any use of a nested type) keeps the holder.
		if !td.Nested && nestedChildUsed(u, td, used) {
			continue
		}
		// Top-level public/package type never referenced from the graph.
		// Nested types: only private unused ones (outer type may be the file).
		if td.Nested && !looksPrivateNested(u, td) {
			continue
		}
		if !td.Nested {
			// The compilation unit itself is live (import or same-package use
			// of *another* type). This extra type is dead.
		}
		fs = append(fs, finding.Finding{
			Language: finding.Java,
			Kind:     finding.UnusedType,
			Path:     u.File.Rel,
			Line:     td.Line,
			Column:   td.Col,
			Name:     td.Name,
			Message:  fmt.Sprintf("unused %s %s", td.Kind, td.Name),
		})
	}
	return fs
}

func nestedChildUsed(u *unit, td TypeDecl, used map[string]bool) bool {
	if used["*"] {
		return true
	}
	for _, t := range u.Types {
		if t.Nested && (t.Enclosing == td.Name || enclosedBy(u.Types, t, td.Name)) && used[t.Name] {
			return true
		}
	}
	return false
}

func enclosedBy(all []TypeDecl, td TypeDecl, outer string) bool {
	enc := td.Enclosing
	guard := 0
	for enc != "" && guard < 8 {
		if enc == outer {
			return true
		}
		next := ""
		for _, t := range all {
			if t.Name == enc {
				next = t.Enclosing
				break
			}
		}
		enc = next
		guard++
	}
	return false
}

func looksPrivateNested(u *unit, td TypeDecl) bool {
	for _, t := range u.Types {
		if t.Name == td.Name && t.Nested && !t.Public {
			return true
		}
	}
	return td.Nested && !td.Public
}

func unusedExports(u *unit, global map[string]int, isEntry bool) []finding.Finding {
	// Public static methods that nobody references, on a live library type.
	// Entry types keep public methods (application / test / Spring surface).
	if isEntry {
		return nil
	}
	if hasReflection(u) {
		return nil
	}
	var fs []finding.Finding
	seen := map[string]bool{}
	for _, mem := range u.Members {
		if mem.Ignored || !mem.Public || !mem.Static || mem.Kind != "method" {
			continue
		}
		if mem.IsMain || isLifecycleName(mem.Name) || isBeanAccessor(mem.Name) {
			continue
		}
		if len(mem.Annotations) > 0 {
			continue
		}
		if seen[mem.Name] {
			continue
		}
		seen[mem.Name] = true
		if global[mem.Name] > 0 {
			continue
		}
		if stringMentions(u.Strings, mem.Name) {
			continue
		}
		fs = append(fs, finding.Finding{
			Language: finding.Java,
			Kind:     finding.UnusedExport,
			Path:     u.File.Rel,
			Line:     mem.Line,
			Column:   mem.Col,
			Name:     mem.Name,
			Message:  fmt.Sprintf("unused method %s", mem.Name),
		})
	}
	return fs
}

func unusedImportsAndPrivates(u *unit, global map[string]int) []finding.Finding {
	local := localUseCount(u)
	var fs []finding.Finding

	seenImp := map[string]bool{}
	for _, imp := range u.Imports {
		if imp.Star || imp.Name == "" {
			continue
		}
		if seenImp[imp.Name] {
			continue
		}
		if local[imp.Name] > 0 {
			continue
		}
		if lineHasIgnore(u.src, imp.Line) {
			continue
		}
		seenImp[imp.Name] = true
		fs = append(fs, finding.Finding{
			Language: finding.Java,
			Kind:     finding.UnusedImport,
			Path:     u.File.Rel,
			Line:     imp.Line,
			Column:   imp.Col,
			Name:     imp.Name,
			Message:  fmt.Sprintf("unused import %s", imp.Name),
		})
	}

	seen := map[string]bool{}
	for _, mem := range u.Members {
		if mem.Ignored || mem.IsMain || isLifecycleName(mem.Name) {
			continue
		}
		if mem.Kind == "constructor" {
			continue
		}
		if len(mem.Annotations) > 0 {
			continue
		}
		if seen[mem.Kind+":"+mem.TypeName+"."+mem.Name] {
			continue
		}
		seen[mem.Kind+":"+mem.TypeName+"."+mem.Name] = true
		if local[mem.Name] > 0 || global[mem.Name] > 0 && !mem.Private {
			// Public names used anywhere stay; private names need a local use.
			if !mem.Private || local[mem.Name] > 0 {
				continue
			}
		}
		if local[mem.Name] > 0 {
			continue
		}
		if stringMentions(u.Strings, mem.Name) {
			continue
		}
		if lineHasIgnore(u.src, mem.Line) {
			continue
		}
		// Private unused members only. Public instance methods are a FN
		// (same class of unsoundness as Python class methods).
		if !mem.Private {
			continue
		}
		if isBeanAccessor(mem.Name) {
			continue
		}
		kind := finding.UnusedMethod
		label := "method"
		if mem.Kind == "field" {
			kind = finding.UnusedVar
			label = "field"
		}
		fs = append(fs, finding.Finding{
			Language: finding.Java,
			Kind:     kind,
			Path:     u.File.Rel,
			Line:     mem.Line,
			Column:   mem.Col,
			Name:     mem.Name,
			Message:  fmt.Sprintf("unused %s %s", label, mem.Name),
		})
	}
	return fs
}

func localUseCount(u *unit) map[string]int {
	out := map[string]int{}
	for _, use := range u.Uses {
		if use.Name != "" && use.Name != "this" && use.Name != "super" {
			out[use.Name]++
		}
		if use.Member != "" {
			out[use.Member]++
		}
		if use.Name == "this" || use.Name == "super" {
			if use.Member != "" {
				out[use.Member]++
			}
		}
		for _, c := range use.Chain {
			out[c]++
		}
	}
	return out
}

func hasReflection(u *unit) bool {
	for _, use := range u.Uses {
		switch use.Name {
		case "Class", "Method", "Field", "Constructor":
			return true
		}
		switch use.Member {
		case "forName", "getMethod", "getDeclaredMethod", "getField", "getDeclaredField",
			"newInstance", "invoke", "getConstructor":
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
		if s == name || strings.Contains(s, name) && isIdentString(s) {
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
			if r != '_' && r != '$' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
			continue
		}
		if r != '_' && r != '$' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
