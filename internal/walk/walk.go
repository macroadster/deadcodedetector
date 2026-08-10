// Package walk discovers source files for analysis.
package walk

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eric/deadcodedetector/internal/ignore"
)

// MaxSourceBytes is the largest source file Discover will keep.
// Minified bundles and accidental data files above this are skipped so
// the scanners cannot pin a CPU core on a multi-megabyte token stream.
const MaxSourceBytes = 2 << 20 // 2 MiB

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
	seenDir := map[fileID]struct{}{}
	if id, ok := fileIdent(root); ok {
		seenDir[id] = struct{}{}
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission / disappearing files: skip, do not abort the scan.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
			// Symlink / bind-mount cycles: skip a directory we have already
			// entered via another path. WalkDir itself does not follow
			// symlinks, but hard-linked or re-entered mount points can.
			if id, ok := fileIdent(path); ok {
				if _, dup := seenDir[id]; dup {
					return filepath.SkipDir
				}
				seenDir[id] = struct{}{}
			}
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
		if info, err := d.Info(); err == nil && info.Size() > MaxSourceBytes {
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
	case ".git", ".hg", ".svn", ".idea", ".vscode",
		".next", ".nuxt", ".cache", ".output",
		".venv", ".tox", ".mypy_cache", ".pytest_cache", ".ruff_cache",
		".direnv", ".grok", ".grok-home", ".playwright-mcp",
		".tmp", ".tmp-e2e":
		return true
	default:
		return false
	}
}

type fileID struct {
	dev, ino uint64
}

func fileIdent(path string) (fileID, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileID{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
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
