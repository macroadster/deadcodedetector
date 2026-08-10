package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

func TestExtractImportsAndDefs(t *testing.T) {
	src := []byte(`
import os
from pathlib import Path
from .config import Config as Cfg
import scripts.utils as u

def used():
    return os.getcwd() + str(Path("."))

def dead():
    return 1

class DeadClass:
    pass

LIVE = used()
DEAD_VAR = 2
`)
	ex := extract(src)
	if len(ex.Imports) < 4 {
		t.Fatalf("imports=%d %+v", len(ex.Imports), ex.Imports)
	}
	names := map[string]string{}
	for _, d := range ex.Decls {
		names[d.Name] = d.Kind
	}
	if names["used"] != "function" || names["dead"] != "function" {
		t.Fatalf("decls %+v", ex.Decls)
	}
	if names["DeadClass"] != "class" {
		t.Fatalf("missing class: %+v", ex.Decls)
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["os"] || !uses["Path"] || !uses["used"] {
		t.Fatalf("uses %#v", uses)
	}
	if uses["dead"] {
		t.Fatalf("dead should not be used: %#v", uses)
	}
}

func TestExtractMainGuard(t *testing.T) {
	src := []byte(`
def main():
    print("hi")

if __name__ == "__main__":
    main()
`)
	ex := extract(src)
	if !ex.HasMain {
		t.Fatal("expected HasMain")
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["main"] {
		t.Fatalf("main should be used: %#v", uses)
	}
}

func TestExtractAll(t *testing.T) {
	src := []byte(`
from .scanner import StarlightScanner
__all__ = ["StarlightScanner"]
`)
	ex := extract(src)
	if len(ex.All) != 1 || ex.All[0] != "StarlightScanner" {
		t.Fatalf("__all__=%v", ex.All)
	}
}

func TestDetectUnused(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.py", `
from lib import keep
import os

def entry():
    return keep()

if __name__ == "__main__":
    entry()
`)
	write("lib.py", `
def keep():
    return 1

def gone():
    return 2

def _private_dead():
    return 3
`)
	write("orphan.py", `
def never_imported():
    return 0
`)

	files := mustWalk(t, dir)
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]finding.Finding{}
	for _, f := range fs {
		byName[f.Name] = f
		t.Logf("finding: %s", f)
	}
	if _, ok := byName["gone"]; !ok {
		t.Fatalf("expected unused export gone; got %v", fs)
	}
	if _, ok := byName["_private_dead"]; !ok {
		t.Fatalf("expected unused private; got %v", fs)
	}
	if _, ok := byName["os"]; !ok {
		t.Fatalf("expected unused import os; got %v", fs)
	}
	if _, ok := byName["orphan.py"]; !ok {
		// unused file may be reported as path name
		found := false
		for _, f := range fs {
			if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "orphan") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected unused file orphan.py; got %v", fs)
		}
	}
	// keep and entry must not be reported
	for _, f := range fs {
		if f.Name == "keep" || f.Name == "entry" {
			t.Fatalf("false positive on %s", f.Name)
		}
	}
}

func TestFStringUsesImport(t *testing.T) {
	src := []byte(`
from datetime import datetime
def fmt():
    return f"now={datetime.now()}"
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["datetime"] {
		t.Fatalf("f-string should use datetime; uses=%v", uses)
	}
}

func TestEntryExportUsedByImporter(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib_entry.py", `
def shared():
    return 1

def only_here_dead():
    return 2

if __name__ == "__main__":
    print("run")
`)
	write("test_use.py", `
from lib_entry import shared

def test_shared():
    assert shared() == 1
`)
	files := mustWalk(t, dir)
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Name == "shared" {
			t.Fatalf("shared is imported by test: %v", fs)
		}
	}
	found := false
	for _, f := range fs {
		if f.Name == "only_here_dead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected only_here_dead; got %v", fs)
	}
}

func TestNestedImportCounts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("util.py", `
def helper():
    return 1

def unused_util():
    return 2
`)
	write("app.py", `
def run():
    from util import helper
    return helper()

if __name__ == "__main__":
    run()
`)
	files := mustWalk(t, dir)
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Name == "helper" {
			t.Fatalf("helper is used via nested import: %v", fs)
		}
	}
	found := false
	for _, f := range fs {
		if f.Name == "unused_util" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused_util dead; got %v", fs)
	}
}

func TestExtractBareRelativeImport(t *testing.T) {
	ex := extract([]byte("from . import util\nfrom ..pkg import helper\n"))
	if len(ex.Imports) != 2 {
		t.Fatalf("imports=%+v", ex.Imports)
	}
	bare := ex.Imports[0]
	if !bare.From || bare.Level != 1 || bare.Module != "" || bare.Name != "util" || bare.Local != "util" {
		t.Fatalf("from . import util: %+v", bare)
	}
	rel := ex.Imports[1]
	if !rel.From || rel.Level != 2 || rel.Module != "pkg" || rel.Name != "helper" {
		t.Fatalf("from ..pkg import helper: %+v", rel)
	}
}

func TestExtractDottedAttrChain(t *testing.T) {
	src := []byte("import pkg.util\nreturn pkg.util.helper()\n")
	ex := extract(src)
	found := false
	for _, u := range ex.Uses {
		if u.Name == "pkg" && len(u.Chain) >= 2 && u.Chain[0] == "util" && u.Chain[1] == "helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected chain pkg.util.helper; uses=%+v", ex.Uses)
	}
}

func TestFromImportSubmodule(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg/__init__.py": "VERSION = \"1\"\n",
		"pkg/util.py": `
def helper():
    return 1

def unused_in_util():
    return 2
`,
		"main.py": `
from pkg import util

if __name__ == "__main__":
    print(util.helper())
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "helper")
	assertNoUnusedFile(t, fs, "util.py")
	assertFinding(t, fs, "unused_in_util")
}

func TestFromRelativeImportSubmodule(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg/__init__.py": `
from . import util

def run():
    return util.helper()
`,
		"pkg/util.py": `
def helper():
    return 1
`,
		"main.py": `
from pkg import run

if __name__ == "__main__":
    print(run())
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "helper")
	assertNoUnusedFile(t, fs, "util.py")
}

func TestParentPackageInitLive(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg/__init__.py": `
VERSION = "1"
def pkg_setup():
    return VERSION
`,
		"pkg/sub.py": `
def helper():
    return 1
`,
		"main.py": `
from pkg.sub import helper

if __name__ == "__main__":
    print(helper())
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "__init__.py")
}

func TestModuleObjectKeepsExports(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"lib.py": `
def keep():
    return 1

def also_keep():
    return 2
`,
		"main.py": `
import lib

def run():
    return lib

if __name__ == "__main__":
    print(run())
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "keep")
	assertNoFinding(t, fs, "also_keep")
}

func TestDottedImportAttr(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg/__init__.py": "",
		"pkg/util.py": `
def helper():
    return 1

def unused_in_util():
    return 2
`,
		"main.py": `
import pkg.util

if __name__ == "__main__":
    print(pkg.util.helper())
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "helper")
	assertNoUnusedFile(t, fs, "util.py")
	assertNoUnusedFile(t, fs, "__init__.py")
	assertFinding(t, fs, "unused_in_util")
}

func TestDecoratedEntryView(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"app.py": `
def route(path):
    def wrap(fn):
        return fn
    return wrap

@route("/")
def index():
    return "ok"

def dead_helper():
    return 1

if __name__ == "__main__":
    pass
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "index")
	assertFinding(t, fs, "dead_helper")
}

func TestPytestFixtureNotReported(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"conftest.py": `
import pytest

@pytest.fixture
def client():
    return 1

def pytest_configure(config):
    pass
`,
		"test_app.py": `
def test_ok():
    assert True
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "client")
	assertNoFinding(t, fs, "pytest_configure")
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustDetect(t *testing.T, dir string) []finding.Finding {
	t.Helper()
	fs, err := Detect(dir, mustWalk(t, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func assertFinding(t *testing.T, fs []finding.Finding, name string) {
	t.Helper()
	for _, f := range fs {
		if f.Name == name {
			return
		}
	}
	t.Fatalf("expected finding %q; got %v", name, fs)
}

func assertNoFinding(t *testing.T, fs []finding.Finding, name string) {
	t.Helper()
	for _, f := range fs {
		if f.Name == name {
			t.Fatalf("false positive on %s: %v", name, fs)
		}
	}
}

func assertNoUnusedFile(t *testing.T, fs []finding.Finding, part string) {
	t.Helper()
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, part) {
			t.Fatalf("unused file %s: %v", f.Path, fs)
		}
	}
}

func mustWalk(t *testing.T, dir string) []walk.File {
	t.Helper()
	var out []walk.File
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".py") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, walk.File{Abs: path, Rel: filepath.ToSlash(rel), Lang: "py"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
