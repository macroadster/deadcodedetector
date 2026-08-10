package javascript

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/ignore"
	"github.com/eric/deadcodedetector/internal/walk"
)

func TestExtractImportsExports(t *testing.T) {
	src := []byte(`
import { used, leftover as alias } from './mod.js';
import def from './d.js';
import * as ns from './n.js';
import './side.js';
export function live() { return used() + def + ns.x; }
export const deadExport = 1;
function localDead() {}
const live = 1;
`)
	ex := extract(src)
	if len(ex.Imports) != 4 {
		t.Fatalf("imports=%d %+v", len(ex.Imports), ex.Imports)
	}
	var names []string
	for _, e := range ex.Exports {
		names = append(names, e.Name)
	}
	if !contains(names, "live") || !contains(names, "deadExport") {
		t.Fatalf("exports %v", names)
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["used"] || !uses["def"] || !uses["ns"] {
		t.Fatalf("uses %#v", uses)
	}
}

func TestExtractTopLevelDecls(t *testing.T) {
	src := []byte(`
export function live() { return used(); }
function localDead() { return 0; }
function used() { return 1; }
live();
`)
	ex := extract(src)
	decls := map[string]bool{}
	for _, d := range ex.Decls {
		decls[d.Name] = d.Exported
	}
	if !decls["live"] || decls["localDead"] != false || decls["used"] != false {
		t.Fatalf("decls %+v", ex.Decls)
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["used"] || !uses["live"] {
		t.Fatalf("uses %#v", uses)
	}
}

func TestExtractTypeScript(t *testing.T) {
	src := []byte(`
import type { Foo } from './types.ts';
import { Bar } from './bar.ts';
export function keep(x: number): number { return Bar(x); }
export interface I { n: number }
type Alias = string;
export type OnlyType = Alias;
function deadTS(n: number) { return n; }
`)
	ex := extract(src)
	var exports []string
	for _, e := range ex.Exports {
		if !e.TypeOnly {
			exports = append(exports, e.Name)
		}
	}
	if !contains(exports, "keep") {
		t.Fatalf("missing keep export: %v", exports)
	}
	if contains(exports, "OnlyType") || contains(exports, "I") {
		t.Fatalf("type-only leaked into value exports: %v", exports)
	}
	foundDead := false
	for _, d := range ex.Decls {
		if d.Name == "deadTS" && !d.Exported {
			foundDead = true
		}
	}
	if !foundDead {
		t.Fatalf("expected deadTS decl, got %+v", ex.Decls)
	}
	if len(ex.Imports) < 1 || !ex.Imports[0].TypeOnly {
		t.Fatalf("expected type-only import first, got %+v", ex.Imports)
	}
}

func TestJSXComponentUse(t *testing.T) {
	src := []byte(`
import { Button } from './ui.js';
export function App() { return <Button className="ok">Hi</Button>; }
`)
	ex := extract(src)
	found := false
	for _, u := range ex.Uses {
		if u.Name == "Button" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected JSX use of Button, uses=%v", ex.Uses)
	}
}

func TestAppDeadCode(t *testing.T) {
	root := testdata(t, "js", "app")
	m := ignore.FromPatterns(nil)
	files, err := walk.Discover(root, m)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Detect(root, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]finding.Kind{}
	for _, f := range fs {
		byName[f.Name] = f.Kind
	}
	want := map[string]finding.Kind{
		"leftover":      finding.UnusedImport,
		"neverUsed":     finding.UnusedImport,
		"localDead":     finding.UnusedFunction,
		"unusedNamed":   finding.UnusedExport,
		"src/orphan.js": finding.UnusedFile,
		"hidden":        finding.UnusedFunction,
	}
	// leftover is imported but unused — unused import. The export leftover
	// is used (imported), so should NOT be unused export.
	for name, kind := range want {
		if byName[name] != kind {
			t.Errorf("%s: got %q want %q\nall=%v", name, byName[name], kind, byName)
		}
	}
	if _, ok := byName["used"]; ok {
		t.Errorf("used should be live")
	}
	if _, ok := byName["src/side.js"]; ok {
		t.Errorf("side-effect import should keep side.js live")
	}
	if _, ok := byName["live"]; ok {
		t.Errorf("live is the entry export / function used locally")
	}
}

func testdata(t *testing.T, elems ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	parts := append([]string{filepath.Dir(file), "..", "..", "testdata"}, elems...)
	return filepath.Clean(filepath.Join(parts...))
}

func contains(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}
