package engine

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eric/deadcodedetector/internal/config"
	"github.com/eric/deadcodedetector/internal/finding"
)

func TestRunJSAndCSS(t *testing.T) {
	root := testdata(t, "css", "site")
	fs, err := Run(config.Config{Root: root, Tests: true, Reachable: true, Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	var css, js int
	for _, f := range fs {
		switch f.Language {
		case finding.CSS:
			css++
		case finding.JavaScript:
			js++
		}
	}
	if css == 0 {
		t.Fatalf("expected CSS findings, got %v", fs)
	}
}

func TestRunPython(t *testing.T) {
	root := testdata(t, "py", "app")
	fs, err := Run(config.Config{Root: root, Tests: true, Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	var py int
	for _, f := range fs {
		if f.Language == finding.Python {
			py++
		}
	}
	if py == 0 {
		t.Fatalf("expected Python findings, got %v", fs)
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
