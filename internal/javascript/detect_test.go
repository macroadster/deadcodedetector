package javascript

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestTSXTypeAliasDoesNotSwallowUses(t *testing.T) {
	src := []byte(`
import { useEffect, useState } from 'react'
import { ErrorBox, Loading } from '../components/Loading'

type Props = {
  token: string
  onLoggedIn: (session: Session) => void
  onNavigate: (path: string) => void
}

export function MagicLinkPage({ token, onLoggedIn, onNavigate }: Props) {
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    onLoggedIn(token)
    onNavigate('/demo')
  }, [token, onLoggedIn, onNavigate])
  return (
    <div>
      {!error && <Loading label="Validating…" />}
      {error && <ErrorBox message={error} />}
    </div>
  )
}
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	for _, name := range []string{"useState", "useEffect", "onLoggedIn", "onNavigate", "Loading", "ErrorBox", "error"} {
		if !uses[name] {
			t.Errorf("missing use %s; uses=%v exports=%v", name, uses, ex.Exports)
		}
	}
	var exports []string
	for _, e := range ex.Exports {
		exports = append(exports, e.Name)
	}
	if !contains(exports, "MagicLinkPage") {
		t.Fatalf("type Props swallowed the export; exports=%v uses=%v", exports, uses)
	}
}

func TestUseClientPreambleStillParsesImports(t *testing.T) {
	src := []byte(`
'use client'
import { useState } from 'react'
import { libraryTone, DOCK_PINS_KEY } from './desktop/helpers'

export function Desktop() {
  const [pins, setPins] = useState<string[]>([])
  return <div className={libraryTone('folder')}>{DOCK_PINS_KEY}</div>
}
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["useState"] || !uses["libraryTone"] || !uses["DOCK_PINS_KEY"] {
		t.Fatalf("uses=%v", uses)
	}
	found := false
	for _, imp := range ex.Imports {
		for _, n := range imp.Named {
			if n.Local == "libraryTone" || n.Remote == "libraryTone" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("import of libraryTone missing: %+v", ex.Imports)
	}
}

func TestReturnTypeDoesNotSwallowFunctionBody(t *testing.T) {
	src := []byte(`
import { useState } from 'react'
import { helper } from './h.js'

function fromProduct(m: MailboxEmail): DemoEmail {
  return helper(m)
}

function kindLabel(kind: DemoEmail['kind']): string {
  return helper(kind)
}

export function Page(): JSX.Element {
  const [v, setV] = useState<string | null>(null)
  return <div>{fromProduct(v)}{kindLabel('x')}{setV}</div>
}
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	for _, name := range []string{"helper", "useState", "fromProduct", "kindLabel"} {
		if !uses[name] {
			t.Errorf("missing use %s; uses=%v", name, uses)
		}
	}
	var exports []string
	for _, e := range ex.Exports {
		exports = append(exports, e.Name)
	}
	if !contains(exports, "Page") {
		t.Fatalf("export swallowed; exports=%v uses=%v", exports, uses)
	}
}

func TestObjectReturnTypeThenBody(t *testing.T) {
	src := []byte(`
import { used } from './u.js'
export function wrap(): { a: number } {
  return { a: used() }
}
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["used"] {
		t.Fatalf("object return type swallowed body; uses=%v", uses)
	}
}

func TestTemplateInterpolationIsUse(t *testing.T) {
	src := []byte("import { DOCK_PINS_KEY, libraryTone } from './h.js'\nexport function f(me) { return `${DOCK_PINS_KEY}:${me.id}` + libraryTone('x') }\n")
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["DOCK_PINS_KEY"] {
		t.Fatalf("template interpolation not a use; uses=%v strings=%v", uses, ex.Strings)
	}
	if !uses["libraryTone"] {
		t.Fatalf("call not a use; uses=%v", uses)
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

func TestTSConfigStarAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{
  "compilerOptions": { "paths": { "@/*": ["./src/*"] } }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(dir, "src")
	comp := filepath.Join(srcDir, "components")
	if err := os.MkdirAll(filepath.Join(srcDir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(comp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app", "page.tsx"), []byte(`
import { HomeClient } from '@/components/HomeClient'
export default function Page() { return <HomeClient /> }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(comp, "HomeClient.tsx"), []byte(`
export function HomeClient() { return <div /> }
function deadLocal() { return 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := ignore.FromPatterns(nil)
	files, err := walk.Discover(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && (f.Name == "src/components/HomeClient.tsx" || strings.Contains(f.Path, "HomeClient")) {
			t.Fatalf("@/ alias did not mark HomeClient live: %v", fs)
		}
	}
	foundDead := false
	for _, f := range fs {
		if f.Name == "deadLocal" {
			foundDead = true
		}
	}
	if !foundDead {
		t.Fatalf("expected deadLocal in live file; got %v", fs)
	}
}

func TestNoUnusedExportsWithoutEntries(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`
export const DOCK_PINS_KEY = 'sl-desktop-dock-pins'
export function libraryTone(icon?: string): string { return icon || 'tone' }
function localDead() { return 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "helpers.ts"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	m := ignore.FromPatterns(nil)
	files, err := walk.Discover(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Kind == finding.UnusedExport || f.Kind == finding.UnusedFile {
			t.Errorf("isolated module must not report %s %s (no entry graph)", f.Kind, f.Name)
		}
	}
	foundDead := false
	for _, f := range fs {
		if f.Name == "localDead" {
			foundDead = true
		}
	}
	if !foundDead {
		t.Fatalf("still expect unused local; got %v", fs)
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
