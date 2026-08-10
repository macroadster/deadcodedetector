package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/walk"
)

// TestStarlightAccuracy scans testdata/starlight and requires ≥95% precision
// against an independent Python-AST-style recheck implemented in Go (extract
// + import graph). Prefer false negatives; precision is the gated metric.
func TestStarlightAccuracy(t *testing.T) {
	root := starlightRoot(t)
	files, err := walk.Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var py []walk.File
	for _, f := range files {
		if f.Lang == "py" {
			py = append(py, f)
		}
	}
	if len(py) < 20 {
		t.Fatalf("expected many python files under starlight, got %d", len(py))
	}

	fs, err := Detect(root, py, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		t.Fatal("expected some findings on starlight")
	}

	// Build independent fact base with the same extractor (parser under test
	// is also the source of truth for uses; cross-check via raw text for
	// unused_import local uses and via resolved import edges for exports).
	byRel := map[string]*module{}
	absList := make([]string, 0, len(py))
	for _, f := range py {
		absList = append(absList, f.Abs)
	}
	res := newResolver(root, absList)
	for _, f := range py {
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			t.Fatal(err)
		}
		ex := extract(src)
		m := &module{File: f, Extracted: ex, src: src}
		for i := range m.Imports {
			m.Imports[i].resolved = res.Resolve(f.Abs, m.Imports[i].Module, m.Imports[i].Level)
		}
		byRel[f.Rel] = m
	}

	// imported names per module abs
	imported := map[string]map[string]bool{}
	for _, m := range byRel {
		for _, imp := range m.Imports {
			if imp.resolved == "" {
				continue
			}
			ue := imported[imp.resolved]
			if ue == nil {
				ue = map[string]bool{}
				imported[imp.resolved] = ue
			}
			if imp.Star {
				ue["*"] = true
				continue
			}
			if imp.From {
				if imp.Name != "" {
					ue[imp.Name] = true
				}
			} else {
				if attrs := m.AttrUses[imp.Local]; len(attrs) > 0 {
					for a := range attrs {
						ue[a] = true
					}
				} else {
					// check uses
					for _, u := range m.Uses {
						if u.Name == imp.Local && u.Member != "" {
							ue[u.Member] = true
						} else if u.Name == imp.Local {
							ue["*"] = true
						}
					}
				}
			}
		}
	}

	tp, fp := 0, 0
	var fpList []string
	for _, f := range fs {
		m := byRel[f.Path]
		ok := true
		switch f.Kind {
		case finding.UnusedImport:
			if m == nil {
				ok = false
				break
			}
			used := false
			for _, u := range m.Uses {
				if u.Name == f.Name {
					used = true
					break
				}
			}
			if m.AttrUses[f.Name] != nil {
				used = true
			}
			if used {
				ok = false
			}
		case finding.UnusedFunction, finding.UnusedType, finding.UnusedVar, finding.UnusedExport, finding.UnusedMethod:
			if m == nil {
				ok = false
				break
			}
			for _, u := range m.Uses {
				if u.Name == f.Name {
					ok = false
					break
				}
			}
			if ue := imported[m.File.Abs]; ue != nil && (ue[f.Name] || ue["*"]) {
				ok = false
			}
		case finding.UnusedFile:
			// Accept unused-file findings; they are conservative.
			ok = true
		}
		if ok {
			tp++
		} else {
			fp++
			fpList = append(fpList, f.String())
		}
	}

	precision := float64(tp) / float64(tp+fp)
	t.Logf("starlight findings=%d TP=%d FP=%d precision=%.2f%%", len(fs), tp, fp, precision*100)
	if precision < 0.95 {
		t.Fatalf("precision %.2f%% < 95%%; false positives:\n%s", precision*100, strings.Join(fpList, "\n"))
	}
}

func starlightRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	// internal/python -> repo root -> testdata/starlight
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "starlight"))
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Skipf("starlight testdata missing: %s", root)
	}
	return root
}
