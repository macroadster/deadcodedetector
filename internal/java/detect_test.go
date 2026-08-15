package java

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

func TestExtractPackageImportsTypes(t *testing.T) {
	src := []byte(`
package com.example.app;

import java.util.List;
import static java.util.Collections.emptyList;
import com.example.app.Used;

public class Main extends Base implements IFace {
    private int unusedField;
    public static void main(String[] args) {
        List<String> xs = emptyList();
        Used.ok();
    }
    private void unusedMethod() {}
}
`)
	ex := extract(src)
	if ex.Package != "com.example.app" {
		t.Fatalf("package=%q", ex.Package)
	}
	if len(ex.Imports) != 3 {
		t.Fatalf("imports=%d %+v", len(ex.Imports), ex.Imports)
	}
	if !ex.Imports[1].Static || ex.Imports[1].Name != "emptyList" {
		t.Fatalf("static import: %+v", ex.Imports[1])
	}
	if !ex.HasMain {
		t.Fatal("expected HasMain")
	}
	types := map[string]bool{}
	for _, td := range ex.Types {
		types[td.Name] = true
	}
	if !types["Main"] {
		t.Fatalf("types %+v", ex.Types)
	}
	members := map[string]string{}
	for _, m := range ex.Members {
		members[m.Name] = m.Kind
	}
	if members["main"] != "method" || members["unusedMethod"] != "method" || members["unusedField"] != "field" {
		t.Fatalf("members %+v", ex.Members)
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
		if u.Member != "" {
			uses[u.Member] = true
		}
	}
	if !uses["Used"] || !uses["ok"] || !uses["emptyList"] || !uses["List"] || !uses["Base"] || !uses["IFace"] {
		t.Fatalf("uses %#v", uses)
	}
}

func TestExtractRecordEnumAndAnnotations(t *testing.T) {
	src := []byte(`
package com.example;

import org.junit.jupiter.api.Test;
import org.springframework.stereotype.Service;

@Service
public class Beans {
    @Test
    void testOk() {}

    private void dead() {}
}

enum Color { RED, GREEN }

public record Point(int x, int y) {}
`)
	ex := extract(src)
	ann := map[string]bool{}
	for _, a := range ex.TypeAnnotations {
		ann[a] = true
	}
	if !ann["Service"] {
		t.Fatalf("type annotations %v", ex.TypeAnnotations)
	}
	kinds := map[string]string{}
	for _, td := range ex.Types {
		kinds[td.Name] = td.Kind
	}
	if kinds["Beans"] != "class" || kinds["Color"] != "enum" || kinds["Point"] != "record" {
		t.Fatalf("kinds %+v", ex.Types)
	}
	foundTest := false
	for _, m := range ex.Members {
		if m.Name == "testOk" {
			foundTest = true
			if len(m.Annotations) == 0 || m.Annotations[0] != "Test" {
				t.Fatalf("test annotations %+v", m.Annotations)
			}
		}
	}
	if !foundTest {
		t.Fatalf("missing testOk: %+v", ex.Members)
	}
}

func TestExtractThisFieldUse(t *testing.T) {
	src := []byte(`
class C {
    private int usedField;
    private int unusedField;
    int live() { return this.usedField; }
}
`)
	ex := extract(src)
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		if u.Name == "this" && u.Member != "" {
			uses[u.Member] = true
		}
		if u.Member != "" {
			uses[u.Member] = true
		}
	}
	if !uses["usedField"] {
		t.Fatalf("this.usedField not recorded: %+v", ex.Uses)
	}
}

func TestDetectUnused(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
import app.Used;
import java.util.List;
public class Main {
    public static void main(String[] args) {
        Used.ok();
    }
    private static int unusedPrivate() { return 0; }
}
`,
		"Used.java": `
package app;
public class Used {
    public static int ok() { return 1; }
    public static int leftover() { return 2; }
    private static int hidden() { return 3; }
}
`,
		"Orphan.java": `
package app;
public class Orphan {
    public static int neverImported() { return 0; }
}
`,
	})
	fs := mustDetect(t, dir)
	byName := map[string]finding.Finding{}
	for _, f := range fs {
		byName[f.Name] = f
		t.Logf("finding: %s", f)
	}
	assertFinding(t, fs, "unusedPrivate")
	assertFinding(t, fs, "hidden")
	assertFinding(t, fs, "leftover")
	assertFinding(t, fs, "List")
	foundOrphan := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "Orphan") {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Fatalf("expected unused file Orphan.java; got %v", fs)
	}
	assertNoFinding(t, fs, "ok")
	assertNoFinding(t, fs, "main")
	assertNoFinding(t, fs, "Used")
}

func TestSamePackageNoImport(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
public class Main {
    public static void main(String[] args) {
        Side.touch();
    }
}
`,
		"Side.java": `
package app;
public class Side {
    public static void touch() {}
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "Side.java")
	assertNoFinding(t, fs, "touch")
}

func TestStarImportConservative(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
import app.util.*;
public class Main {
    public static void main(String[] args) {
        Helper.go();
    }
}
`,
		"util/Helper.java": `
package app.util;
public class Helper {
    public static void go() {}
    public static void leftover() {}
}
`,
		"util/Other.java": `
package app.util;
public class Other {
    public static void x() {}
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "Helper.java")
	assertNoUnusedFile(t, fs, "Other.java")
	// leftover is public static and never referenced — still reported
	assertFinding(t, fs, "leftover")
}

func TestJUnitIsEntry(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"src/test/java/app/FooTest.java": `
package app;
import org.junit.jupiter.api.Test;
public class FooTest {
    @Test
    void testOk() {}
    private void unusedHelper() {}
}
`,
		"src/main/java/app/Lib.java": `
package app;
public class Lib {
    public static int unusedLib() { return 1; }
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "testOk")
	assertFinding(t, fs, "unusedHelper")
	// Lib is never referenced from the test entry
	found := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "Lib.java") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused Lib.java; got %v", fs)
	}
}

func TestSpringBeanIsEntry(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"WidgetService.java": `
package app;
import org.springframework.stereotype.Service;
@Service
public class WidgetService {
    public int api() { return 1; }
    private int unusedPrivate() { return 2; }
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "WidgetService.java")
	assertNoFinding(t, fs, "api")
	assertFinding(t, fs, "unusedPrivate")
}

func TestIgnoreDirectives(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
public class Main {
    public static void main(String[] args) {}
    // dcd:ignore
    private static int ignoredDead() { return 0; }
    private static int realDead() { return 1; }
}
`,
		"Skip.java": `
// dcd:ignore-file
package app;
public class Skip {
    private static int alsoDead() { return 0; }
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "ignoredDead")
	assertFinding(t, fs, "realDead")
	for _, f := range fs {
		if strings.Contains(f.Path, "Skip.java") {
			t.Fatalf("ignore-file leaked: %v", fs)
		}
	}
}

func TestMethodReferenceCountsAsUse(t *testing.T) {
	src := []byte(`
class C {
    private static int helper() { return 1; }
    static int run() { return java.util.stream.Stream.of(1).map(C::helper).findFirst().orElse(0); }
}
`)
	ex := extract(src)
	found := false
	for _, u := range ex.Uses {
		if u.Name == "C" && (u.Member == "helper" || containsStr(u.Chain, "helper")) {
			found = true
		}
		if u.Member == "helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("C::helper not recorded: %+v", ex.Uses)
	}
}

func TestClassForNameKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
public class Main {
    public static void main(String[] args) throws Exception {
        Class.forName("app.Plugin");
    }
}
`,
		"Plugin.java": `
package app;
public class Plugin {
    public static int leftover() { return 1; }
}
`,
		"Orphan.java": `
package app;
public class Orphan {}
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "Plugin.java")
	assertNoFinding(t, fs, "Plugin")
	found := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "Orphan") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused Orphan.java; got %v", fs)
	}
}

func TestWebXmlServletClassKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"src/main/java/app/Main.java": `
package app;
public class Main {
    public static void main(String[] args) {}
}
`,
		"src/main/java/app/Workspace.java": `
package app;
public class Workspace {}
`,
		"src/main/java/app/HttpProxy.java": `
package app;
public class HttpProxy {}
`,
		"src/main/webapp/WEB-INF/web.xml": `
<web-app>
  <servlet>
    <servlet-class>app.Workspace</servlet-class>
  </servlet>
  <filter>
    <filter-class>app.HttpProxy</filter-class>
  </filter>
</web-app>
`,
		"src/main/java/app/Orphan.java": `
package app;
public class Orphan {}
`,
	})
	files := mustWalkAll(t, dir)
	fs, err := Detect(dir, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNoUnusedFile(t, fs, "Workspace.java")
	assertNoUnusedFile(t, fs, "HttpProxy.java")
	found := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "Orphan") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused Orphan.java; got %v", fs)
	}
}

func TestNestedImportKeepsOuterType(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
import app.Holder.Inner;
public class Main {
    public static void main(String[] args) {
        Inner.ok();
    }
}
`,
		"Holder.java": `
package app;
public class Holder {
    public static class Inner {
        public static int ok() { return 1; }
    }
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoUnusedFile(t, fs, "Holder.java")
	assertNoFinding(t, fs, "Holder")
	assertNoFinding(t, fs, "Inner")
	assertNoFinding(t, fs, "ok")
}

func TestGeneratedRecordMethodsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Main.java": `
package app;
public class Main {
    public static void main(String[] args) {
        new Rec();
    }
}
`,
		"Rec.java": `
package app;
public class Rec {
    public static String signature() { return "LRec()"; }
    public static int slurpRaw(byte[] b, int s, int l) { return 0; }
    public static int compareRaw(byte[] a, int b, int c, byte[] d, int e, int f) { return 0; }
    public static int leftover() { return 1; }
}
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "signature")
	assertNoFinding(t, fs, "slurpRaw")
	assertNoFinding(t, fs, "compareRaw")
	assertFinding(t, fs, "leftover")
}

func TestAppDeadCode(t *testing.T) {
	root := testdata(t, "java", "app")
	files, err := walk.Discover(root, ignore.FromPatterns(nil))
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
		t.Logf("finding: %s", f)
	}
	want := map[string]finding.Kind{
		"UnusedExport":   finding.UnusedImport,
		"List":           finding.UnusedImport,
		"unusedField":    finding.UnusedVar,
		"unusedPrivate":  finding.UnusedMethod,
		"hidden":         finding.UnusedMethod,
		"leftover":       finding.UnusedExport,
		"leftoverExport": finding.UnusedExport,
	}
	for name, kind := range want {
		if byName[name] != kind {
			t.Errorf("%s: got %q want %q\nall=%v", name, byName[name], kind, byName)
		}
	}
	foundOrphan := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "Orphan.java") {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Errorf("expected unused file Orphan.java; all=%v", byName)
	}
	for _, live := range []string{"ok", "live", "usedField", "touch", "ignoredDead", "main"} {
		if _, ok := byName[live]; ok {
			t.Errorf("%s should be live", live)
		}
	}
	assertNoUnusedFile(t, fs, "Used.java")
	assertNoUnusedFile(t, fs, "Side.java")
	assertNoUnusedFile(t, fs, "Main.java")
	assertNoUnusedFile(t, fs, "UnusedExport.java")
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
		if !strings.HasSuffix(path, ".java") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, walk.File{Abs: path, Rel: filepath.ToSlash(rel), Lang: "java"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustWalkAll(t *testing.T, dir string) []walk.File {
	t.Helper()
	files, err := walk.Discover(dir, ignore.FromPatterns(nil))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
