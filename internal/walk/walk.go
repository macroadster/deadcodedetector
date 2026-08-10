// Package walk discovers source files for analysis.
package walk

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/eric/deadcodedetector/internal/ignore"
)

// File is a source file discovered under the scan root.
type File struct {
	Abs  string
	Rel  string // slash-separated, relative to root
	Lang string // "go", "js", "css", "html"
}

// Discover walks root and returns files that look like source, applying m.
func Discover(root string, m *ignore.Matcher) ([]File, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []File
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") && base != "." {
				// Hidden dirs are ignored unless they were already excluded
				// by a more specific matcher; still skip typical VCS/tooling.
				if m == nil || m.Ignore(rel, true) || defaultHiddenDir(base) {
					return filepath.SkipDir
				}
			}
			if m != nil && m.Ignore(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if m != nil && m.Ignore(rel, false) {
			return nil
		}
		lang := langOf(d.Name())
		if lang == "" {
			return nil
		}
		out = append(out, File{Abs: path, Rel: rel, Lang: lang})
		return nil
	})
	return out, err
}

func defaultHiddenDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".idea", ".vscode", ".next", ".nuxt", ".cache", ".output":
		return true
	default:
		return false
	}
}

func langOf(name string) string {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, ".d.ts") || strings.HasSuffix(low, ".d.mts") || strings.HasSuffix(low, ".d.cts") {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(low))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return "js"
	case ".css":
		return "css"
	case ".html", ".htm", ".shtml":
		return "html"
	case ".tmpl", ".gohtml", ".gotmpl":
		return "html"
	default:
		return ""
	}
}

// DetectedLangs returns the set of analyzable languages present in files.
func DetectedLangs(files []File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		switch f.Lang {
		case "go", "js", "css":
			out[f.Lang] = true
		}
	}
	return out
}
