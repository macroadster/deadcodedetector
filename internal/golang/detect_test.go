package golang

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
)

func testdata(t *testing.T, elems ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	parts := append([]string{filepath.Dir(file), "..", "..", "testdata"}, elems...)
	return filepath.Clean(filepath.Join(parts...))
}

func names(fs []finding.Finding) map[string]finding.Kind {
	out := map[string]finding.Kind{}
	for _, f := range fs {
		out[f.Name] = f.Kind
	}
	return out
}

func TestProgDeadCode(t *testing.T) {
	fs, err := Detect(Options{
		Root:      testdata(t, "go", "prog"),
		Tests:     true,
		Reachable: true,
		Exported:  boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(fs)
	for _, want := range []string{"dead", "goodbye", "Goodbyer.Greet", "leftover", "deadConst", "deadVar"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing finding %s\nall: %v", want, got)
		}
	}
	for _, live := range []string{"main", "used", "hello", "Helloer.Greet", "Greeter"} {
		if _, ok := got[live]; ok {
			t.Errorf("false positive %s (%s)", live, got[live])
		}
	}
}

func TestLibUnexportedOnly(t *testing.T) {
	fs, err := Detect(Options{
		Root:      testdata(t, "go", "lib"),
		Tests:     true,
		Reachable: true,
		// Exported nil = auto = false (no main)
	})
	if err != nil {
		t.Fatal(err)
	}
	got := names(fs)
	if _, ok := got["unusedHelper"]; !ok {
		t.Fatalf("expected unusedHelper, got %v", got)
	}
	if _, ok := got["Public"]; ok {
		t.Errorf("should not report exported Public in library mode")
	}
	if _, ok := got["helper"]; ok {
		t.Errorf("helper is used")
	}
	if _, ok := got["ignoredDead"]; ok {
		t.Errorf("ignoredDead should be suppressed by dcd:ignore")
	}
}

func TestLibExportedFlag(t *testing.T) {
	yes := true
	fs, err := Detect(Options{
		Root:     testdata(t, "go", "lib"),
		Exported: &yes,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Public is used by... nobody except it's the API. With -exported=true
	// it is still used? No, nothing calls Public. So it should be reported.
	got := names(fs)
	if _, ok := got["Public"]; !ok {
		t.Errorf("expected Public when -exported=true, got %v", got)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestIsHeavyPath(t *testing.T) {
	if !isHeavyPath("github.com/btcsuite/btcd/wire") {
		t.Fatal("expected btcd to be heavy")
	}
	if !isHeavyPath("github.com/libp2p/go-libp2p") {
		t.Fatal("expected libp2p to be heavy")
	}
	if isHeavyPath("example.com/foo") {
		t.Fatal("example.com/foo should not be heavy")
	}
	if isHeavyPath("nova.teachx.ai/trace-analysis/starlight") {
		t.Fatal("local module path should not be heavy")
	}
}

func TestSelfNoFalsePositives(t *testing.T) {
	root := filepath.Clean(filepath.Join(testdata(t), ".."))
	fs, err := Detect(Options{Root: root, Tests: true, Reachable: true})
	if err != nil {
		t.Fatal(err)
	}
	// The detector itself should not flag live API such as ignore.New
	// or flag.Value methods on the CLI.
	for _, f := range fs {
		switch f.Name {
		case "New", "Matcher.loadFile", "multiFlag.String", "multiFlag.Set":
			t.Errorf("false positive: %s", f)
		}
	}
}
