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
