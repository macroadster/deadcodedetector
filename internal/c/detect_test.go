package c

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

func TestExtractFunctionsMacrosIncludes(t *testing.T) {
	src := []byte(`
#ifndef UTIL_H
#define UTIL_H

#include "used.h"
#include <stdio.h>

#define USED_MACRO 1
#define UNUSED_MACRO 2
#define USED_FN(x) ((x) + 1)
#define UNUSED_FN(x) ((x) + 2)

static int unused_static(void) { return UNUSED_MACRO; }

int used_fn(void) {
    return USED_MACRO + USED_FN(1);
}

int unused_export(void) { return 0; }

int main(void) {
    return used_fn();
}
#endif
`)
	ex := extract(src)
	if ex.HeaderGuard != "UTIL_H" {
		t.Fatalf("header guard=%q", ex.HeaderGuard)
	}
	if !ex.HasMain {
		t.Fatal("expected HasMain")
	}
	macros := map[string]bool{}
	for _, m := range ex.Macros {
		macros[m.Name] = m.FuncLike
	}
	if _, ok := macros["USED_MACRO"]; !ok {
		t.Fatalf("macros %+v", ex.Macros)
	}
	if _, ok := macros["UNUSED_MACRO"]; !ok {
		t.Fatalf("missing UNUSED_MACRO: %+v", ex.Macros)
	}
	if !macros["USED_FN"] || !macros["UNUSED_FN"] {
		t.Fatalf("func-like %+v", ex.Macros)
	}
	if macros["USED_MACRO"] || macros["UTIL_H"] {
		t.Fatalf("object-like flagged as func: %+v", ex.Macros)
	}
	decls := map[string]Decl{}
	for _, d := range ex.Decls {
		decls[d.Name] = d
	}
	if decls["used_fn"].Kind != "function" || decls["unused_static"].Kind != "function" {
		t.Fatalf("decls %+v", ex.Decls)
	}
	if !decls["unused_static"].Static {
		t.Fatal("expected static")
	}
	if !decls["main"].Keep && !ex.HasMain {
		t.Fatal("main should be an entry")
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["used_fn"] || !uses["USED_MACRO"] || !uses["USED_FN"] {
		t.Fatalf("uses %#v", uses)
	}
	if uses["unused_export"] {
		t.Fatalf("unused_export should not be used: %#v", uses)
	}
	var hasUsed, hasStdio bool
	for _, inc := range ex.Includes {
		if inc.Path == "used.h" && !inc.System {
			hasUsed = true
		}
		if inc.Path == "stdio.h" && inc.System {
			hasStdio = true
		}
	}
	if !hasUsed || !hasStdio {
		t.Fatalf("includes %+v", ex.Includes)
	}
}

func TestExtractDlsymAndDlopen(t *testing.T) {
	src := []byte(`
#include <dlfcn.h>
int plugin_init(void);
int main(void) {
    void *h = dlopen("libplugin.so", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)fn;
    return 0;
}
`)
	ex := extract(src)
	foundSym, foundLib := false, false
	for _, s := range ex.DynSyms {
		if s == "plugin_init" {
			foundSym = true
		}
	}
	for _, s := range ex.DynLibs {
		if s == "libplugin.so" {
			foundLib = true
		}
	}
	if !foundSym || !foundLib {
		t.Fatalf("dyn syms=%v libs=%v", ex.DynSyms, ex.DynLibs)
	}
	if ex.HasIndirectDlsym {
		t.Fatal("literal dlsym should not be indirect")
	}
}

func TestExtractIndirectDlsym(t *testing.T) {
	src := []byte(`
int main(void) {
    const char *n = "plugin_init";
    void *fn = dlsym(0, n);
    (void)fn;
    return 0;
}
`)
	ex := extract(src)
	if !ex.HasIndirectDlsym {
		t.Fatal("expected indirect dlsym")
	}
}

func TestExtractFunctionPointerIsNotFunction(t *testing.T) {
	src := []byte(`
static int helper(int x) { return x; }
static int (*fp)(int) = helper;
int main(void) { return fp(1); }
`)
	ex := extract(src)
	for _, d := range ex.Decls {
		if d.Name == "fp" && d.Kind == "function" {
			t.Fatalf("fp should be a var, got %+v", d)
		}
		if d.Name == "fp" && d.Kind != "var" {
			t.Fatalf("fp kind=%s", d.Kind)
		}
	}
	uses := map[string]bool{}
	for _, u := range ex.Uses {
		uses[u.Name] = true
	}
	if !uses["helper"] {
		t.Fatalf("helper should be used via initializer: %#v", uses)
	}
}

func TestExtractConstructorKept(t *testing.T) {
	src := []byte(`
__attribute__((constructor))
static void onload(void) {}
`)
	ex := extract(src)
	found := false
	for _, d := range ex.Decls {
		if d.Name == "onload" {
			found = true
			if !d.Keep {
				t.Fatalf("constructor should be kept: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("missing onload: %+v", ex.Decls)
	}
}

func TestExtractIfZeroSkipped(t *testing.T) {
	src := []byte(`
int live(void) { return 1; }
#if 0
int dead_disabled(void) { return 0; }
#define DEAD_DISABLED 1
#endif
int main(void) { return live(); }
`)
	ex := extract(src)
	for _, d := range ex.Decls {
		if d.Name == "dead_disabled" {
			t.Fatalf("should skip #if 0 function: %+v", ex.Decls)
		}
	}
	for _, m := range ex.Macros {
		if m.Name == "DEAD_DISABLED" {
			t.Fatalf("should skip #if 0 macro: %+v", ex.Macros)
		}
	}
}

func TestDetectUnusedMacrosAndFunctions(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"util.h": `
#ifndef UTIL_H
#define UTIL_H
#define USED_MACRO 1
#define UNUSED_MACRO 2
#define USED_FN(x) ((x) + 1)
#define UNUSED_FN(x) ((x) + 2)
int used_fn(void);
int leftover(void);
#endif
`,
		"used.c": `
#include "util.h"
int used_fn(void) { return USED_MACRO + USED_FN(1); }
int leftover(void) { return 0; }
static int hidden(void) { return 1; }
`,
		"main.c": `
#include "util.h"
int main(void) { return used_fn(); }
static int unused_static(void) { return 0; }
`,
		"orphan.c": `
int orphan_fn(void) { return 0; }
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertFinding(t, fs, "UNUSED_MACRO")
	assertFinding(t, fs, "UNUSED_FN")
	assertFinding(t, fs, "leftover")
	assertFinding(t, fs, "hidden")
	assertFinding(t, fs, "unused_static")
	assertNoFinding(t, fs, "USED_MACRO")
	assertNoFinding(t, fs, "USED_FN")
	assertNoFinding(t, fs, "used_fn")
	assertNoFinding(t, fs, "main")
	assertNoFinding(t, fs, "UTIL_H")
	foundOrphan := false
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "orphan.c") {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Fatalf("expected unused file orphan.c; got %v", fs)
	}
}

func TestDetectDlsymKeepsDynamicExport(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"plugin.c": `
int plugin_init(void) { return 1; }
int plugin_unused(void) { return 2; }
static int plugin_hidden(void) { return 3; }
`,
		"loader.c": `
#include <dlfcn.h>
int main(void) {
    void *h = dlopen("./libplugin.so", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)fn;
    return 0;
}
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertNoFinding(t, fs, "plugin_init")
	assertFinding(t, fs, "plugin_unused")
	assertFinding(t, fs, "plugin_hidden")
	assertNoFinding(t, fs, "main")
}

func TestDetectLibraryOnlyKeepsPublicAPI(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"CMakeLists.txt": "add_library(foo SHARED foo.c)\n",
		"foo.h": `
#ifndef FOO_H
#define FOO_H
#define FOO_API 1
int foo_init(void);
#endif
`,
		"foo.c": `
#include "foo.h"
int foo_init(void) { return FOO_API; }
int foo_unused_export(void) { return 0; }
static int foo_hidden(void) { return 1; }
#define LOCAL_UNUSED 3
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertFinding(t, fs, "foo_hidden")
	assertFinding(t, fs, "LOCAL_UNUSED")
	assertNoFinding(t, fs, "foo_init")
	assertNoFinding(t, fs, "foo_unused_export")
	assertNoFinding(t, fs, "FOO_API")
	assertNoFinding(t, fs, "FOO_H")
}

func TestDetectIgnoreDirectives(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.c": `
int main(void) { return 0; }
// dcd:ignore
static int ignored_dead(void) { return 0; }
static int real_dead(void) { return 1; }
#define DEAD_MACRO 1
`,
		"skip.c": `
// dcd:ignore-file
static int also_dead(void) { return 0; }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "ignored_dead")
	assertFinding(t, fs, "real_dead")
	assertFinding(t, fs, "DEAD_MACRO")
	for _, f := range fs {
		if strings.Contains(f.Path, "skip.c") {
			t.Fatalf("ignore-file leaked: %v", fs)
		}
	}
}

func TestDetectUnusedInclude(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"used.h": `
#ifndef USED_H
#define USED_H
#define NEED_THIS 1
#endif
`,
		"dead.h": `
#ifndef DEAD_H
#define DEAD_H
#define NEVER 2
#endif
`,
		"main.c": `
#include "used.h"
#include "dead.h"
int main(void) { return NEED_THIS; }
`,
	})
	fs := mustDetect(t, dir)
	found := false
	for _, f := range fs {
		if f.Kind == finding.UnusedImport && f.Name == "dead.h" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unused include dead.h; got %v", fs)
	}
	for _, f := range fs {
		if f.Kind == finding.UnusedImport && f.Name == "used.h" {
			t.Fatalf("used.h should not be unused: %v", fs)
		}
	}
}

func TestDetectDylibBinaryExports(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin.c")
	if err := os.WriteFile(plugin, []byte(`
int plugin_init(void) { return 1; }
int plugin_unused(void) { return 2; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	libName := "libplugin.so"
	if runtime.GOOS == "darwin" {
		libName = "libplugin.dylib"
	} else if runtime.GOOS == "windows" {
		libName = "plugin.dll"
	}
	lib := filepath.Join(dir, libName)
	cmd := exec.Command(cc, "-shared", "-fPIC", "-o", lib, plugin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cc -shared failed: %v\n%s", err, out)
	}
	// Drop the source so unused exports are attributed to the shared library.
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{
		"loader.c": `
int main(void) {
    void *h = dlopen("` + libName + `", 1);
    void *fn = dlsym(h, "plugin_init");
    (void)h; (void)fn;
    return 0;
}
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertNoFinding(t, fs, "plugin_init")
	assertFinding(t, fs, "plugin_unused")
}

func TestTokenPasteKeepsMacros(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"opts.h": `
#ifndef OPTS_H
#define OPTS_H
#define setopt_nv_CURLOPT_HSTS_CTRL setopt_nv_CURLHSTS
#define setopt_nv_CURLOPT_HTTPAUTH  setopt_nv_CURLAUTH
#define UNUSED_PLAIN 1
#define my_setopt_enum(x, y, z) tool_setopt_enum(x, #y, y, setopt_nv_ ## y, z)
#endif
`,
		"main.c": `
#include "opts.h"
int main(void) { my_setopt_enum(0, CURLOPT_HSTS_CTRL, 0); return UNUSED_PLAIN; }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "setopt_nv_CURLOPT_HSTS_CTRL")
	assertNoFinding(t, fs, "setopt_nv_CURLOPT_HTTPAUTH")
	assertNoFinding(t, fs, "UNUSED_PLAIN") // used
}

func TestPublicHeaderMacrosKeptInLibPlusCLI(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"include/curl/curl.h": `
#ifndef CURL_H
#define CURL_H
#define CURL_STRICTER
#define CURLE_OBSOLETE CURLE_OBSOLETE50
void curl_easy_upkeep(void);
#endif
`,
		"lib/easy.c": `
#include "../include/curl/curl.h"
void curl_easy_upkeep(void) {}
static int hidden(void) { return 0; }
#define LOCAL_UNUSED 1
`,
		"lib/CMakeLists.txt": "add_library(libcurl SHARED easy.c)\n",
		"src/main.c": `
int main(void) { return 0; }
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertNoFinding(t, fs, "CURL_STRICTER")
	assertNoFinding(t, fs, "CURLE_OBSOLETE")
	assertNoFinding(t, fs, "curl_easy_upkeep")
	assertFinding(t, fs, "hidden")
	assertFinding(t, fs, "LOCAL_UNUSED")
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "easy.c") {
			t.Fatalf("lib/*.c should not be unused files: %v", fs)
		}
	}
}

func TestHarnessTestIsEntry(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"tests/libtest/first.h": `
#ifndef FIRST_H
#define FIRST_H
#define easy_init() do { } while(0)
#define UNUSED_HARNESS 1
#endif
`,
		"tests/libtest/lib1500.c": `
#include "first.h"
int test(void) { easy_init(); return 0; }
static int dead_helper(void) { return 1; }
`,
		"src/main.c": `
int main(void) { return 0; }
`,
	})
	fs := mustDetect(t, dir)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertNoFinding(t, fs, "easy_init")
	assertFinding(t, fs, "UNUSED_HARNESS")
	assertFinding(t, fs, "dead_helper")
	assertNoFinding(t, fs, "test")
	for _, f := range fs {
		if f.Kind == finding.UnusedFile && strings.Contains(f.Path, "lib1500") {
			t.Fatalf("harness test should be an entry: %v", fs)
		}
	}
}

func TestTrueFalseMacroNotReported(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.c": `
#if defined(__hpux)
#define false 0
#define true 1
#endif
int main(void) { return true && !false; }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "true")
	assertNoFinding(t, fs, "false")
}

func TestAttributeMacroIsUseNotVar(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.c": `
#ifdef __GNUC__
#define CURL_ALIGN8 __attribute__((aligned(8)))
#else
#define CURL_ALIGN8
#endif
int flag CURL_ALIGN8 = 1;
int main(void) { return flag; }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "CURL_ALIGN8")
	assertNoFinding(t, fs, "flag")
}

func TestMacroWrappedFunction(t *testing.T) {
	src := []byte(`
static LIBSSH2_ALLOC_FUNC(my_libssh2_malloc)
{
    return 0;
}
int main(void) { return 0; }
`)
	ex := extract(src)
	names := map[string]bool{}
	for _, d := range ex.Decls {
		names[d.Name] = true
	}
	if names["LIBSSH2_ALLOC_FUNC"] {
		t.Fatalf("wrapper macro should not be the function: %+v", ex.Decls)
	}
	if !names["my_libssh2_malloc"] {
		t.Fatalf("expected wrapped function name: %+v", ex.Decls)
	}
}

func TestGetProcAddressTEXT(t *testing.T) {
	src := []byte(`
int main(void) {
    void *fn = GetProcAddress(GetModuleHandle(TEXT("ntdll")), TEXT("RtlVerifyVersionInfo"));
    (void)fn;
    return 0;
}
`)
	ex := extract(src)
	found := false
	for _, s := range ex.DynSyms {
		if s == "RtlVerifyVersionInfo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected TEXT() symbol, got %v", ex.DynSyms)
	}
	if ex.HasIndirectDlsym {
		t.Fatal("TEXT(\"...\") should not be treated as indirect")
	}
}

func TestConfigHeaderMacrosSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"lib/config-os400.h": `
#define HAVE_LDAP_H 1
#define SIZEOF_INT 4
`,
		"lib/util.c": `
#define LOCAL_UNUSED 2
int used(void) { return 1; }
`,
		"src/main.c": `
int used(void);
int main(void) { return used(); }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "HAVE_LDAP_H")
	assertNoFinding(t, fs, "SIZEOF_INT")
	assertFinding(t, fs, "LOCAL_UNUSED")
}

func TestPublicAPICalleesStayLive(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"include/foo.h": `
#ifndef FOO_H
#define FOO_H
int foo_init(void);
#endif
`,
		"lib/foo.c": `
#include "../include/foo.h"
static int helper(int n) {
    if (n) return n;
    return 1;
}
int foo_init(void) { return helper(0); }
`,
		"lib/CMakeLists.txt": "add_library(foo SHARED foo.c)\n",
		"src/main.c": `
int main(void) { return 0; }
`,
	})
	fs := mustDetect(t, dir)
	assertNoFinding(t, fs, "foo_init")
	assertNoFinding(t, fs, "helper")
}

func TestDetectOnlyUsedFromDeadCode(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.c": `
int used(void) { return 1; }
int main(void) { return used(); }

static int dead_rec(int n) {
    if (n <= 0) return 0;
    return dead_rec(n - 1);
}

static int dead_leaf(int n) {
    if (n) return n;
    return -1;
}
static int (*dead_fp)(int) = dead_leaf;

static int dead_child(int n) { return n ? n : 1; }
static int dead_parent(int n) {
    if (n > 0) return dead_child(n);
    return 0;
}
`,
	})
	fs := mustDetect(t, dir)
	assertFinding(t, fs, "dead_rec")
	assertFinding(t, fs, "dead_leaf")
	assertFinding(t, fs, "dead_fp")
	assertFinding(t, fs, "dead_parent")
	assertFinding(t, fs, "dead_child")
	assertNoFinding(t, fs, "used")
	assertNoFinding(t, fs, "main")
}

func TestExtractUseOwner(t *testing.T) {
	src := []byte(`
static int leaf(int n) { return n ? n : 0; }
static int (*fp)(int) = leaf;
int main(void) { return fp(1); }
`)
	ex := extract(src)
	var sawLeafFromFP, sawFPFromMain bool
	for _, u := range ex.Uses {
		if u.Name == "leaf" && u.Owner == "fp" {
			sawLeafFromFP = true
		}
		if u.Name == "fp" && u.Owner == "main" {
			sawFPFromMain = true
		}
	}
	if !sawLeafFromFP || !sawFPFromMain {
		t.Fatalf("owners not attached: %+v", ex.Uses)
	}
}

func TestTestdataApp(t *testing.T) {
	root := testdata(t, "c", "app")
	fs := mustDetect(t, root)
	for _, f := range fs {
		t.Logf("finding: %s", f)
	}
	assertFinding(t, fs, "UNUSED_MACRO")
	assertFinding(t, fs, "unused_static")
	assertFinding(t, fs, "plugin_unused")
	assertFinding(t, fs, "unused_classify")
	assertFinding(t, fs, "unused_rank")
	assertFinding(t, fs, "unused_cb")
	assertFinding(t, fs, "unused_scale")
	assertNoFinding(t, fs, "USED_MACRO")
	assertNoFinding(t, fs, "used_fn")
	assertNoFinding(t, fs, "plugin_init")
	assertNoFinding(t, fs, "main")
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

func mustWalk(t *testing.T, dir string) []walk.File {
	t.Helper()
	var out []walk.File
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		low := strings.ToLower(path)
		lang := ""
		switch {
		case strings.HasSuffix(low, ".c") || strings.HasSuffix(low, ".h"):
			lang = "c"
		case strings.HasSuffix(low, ".so") || strings.HasSuffix(low, ".dylib") || strings.HasSuffix(low, ".dll"):
			lang = "dylib"
		default:
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		out = append(out, walk.File{Abs: path, Rel: filepath.ToSlash(rel), Lang: lang})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
