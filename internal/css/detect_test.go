package css

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/ignore"
	"github.com/eric/deadcodedetector/internal/walk"
)

func TestParseSelector(t *testing.T) {
	r := parseSelector(".hero.title #app[data-x] custom-el", 1, 1)
	if !has(r.classes, "hero") || !has(r.classes, "title") {
		t.Fatalf("classes %v", r.classes)
	}
	if !has(r.ids, "app") {
		t.Fatalf("ids %v", r.ids)
	}
	if !has(r.attrs, "data-x") {
		t.Fatalf("attrs %v", r.attrs)
	}
	if !has(r.tags, "custom-el") {
		t.Fatalf("tags %v", r.tags)
	}
}

func TestSiteCSS(t *testing.T) {
	root := testdata(t, "css", "site")
	files, err := walk.Discover(root, ignore.FromPatterns(nil))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Detect(root, files)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]finding.Kind{}
	for _, f := range fs {
		got[f.Name] = f.Kind
	}
	if got[".old-modal"] != finding.UnusedSelector {
		t.Errorf(".old-modal: %q\n%v", got[".old-modal"], got)
	}
	if got["#unused-id"] != finding.UnusedSelector {
		t.Errorf("#unused-id: %q", got["#unused-id"])
	}
	if got["unused-spin"] != finding.UnusedKeyframes {
		t.Errorf("unused-spin: %q", got["unused-spin"])
	}
	for _, live := range []string{".hero", ".hero.title", ".is-ready", "#app", "fade-in", "html, body"} {
		if _, ok := got[live]; ok {
			t.Errorf("false positive %s", live)
		}
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

func has(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}
