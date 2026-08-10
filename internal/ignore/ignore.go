// Package ignore implements gitignore-style path filtering.
package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPatterns are always ignored unless the user scans inside them directly.
var DefaultPatterns = []string{
	".git/",
	".hg/",
	".svn/",
	"node_modules/",
	"vendor/",
	"dist/",
	"build/",
	"coverage/",
	".next/",
	".nuxt/",
	".output/",
	".cache/",
	"out/",
	"tmp/",
	"testdata/",
	".venv/",
	"__pycache__/",
	".grok-home/",
	"agent-sandbox/",
	".playwright-mcp/",
	"*.min.js",
	"*.min.css",
	"*.min.mjs",
	"*.bundle.js",
	"*.map",
	"*.pb.go",
	"*_gen.go",
	"*_generated.go",
	"generated.go",
}

// Matcher decides whether a slash-separated relative path should be skipped.
type Matcher struct {
	patterns []pattern
}

type pattern struct {
	raw       string
	neg       bool
	dirOnly   bool
	anchored  bool
	segments  []string
	matchFile bool // last segment is a file glob
}

// New builds a matcher from default patterns, optional .gitignore/.dcdignore
// files under root, and extra user patterns.
func New(root string, extra []string) (*Matcher, error) {
	m := &Matcher{}
	for _, p := range DefaultPatterns {
		m.add(p)
	}
	for _, name := range []string{".gitignore", ".dcdignore"} {
		path := filepath.Join(root, name)
		if err := m.loadFile(path); err != nil {
			return nil, err
		}
	}
	for _, p := range extra {
		m.add(p)
	}
	return m, nil
}

// FromPatterns builds a matcher from explicit patterns only (no defaults, no files).
func FromPatterns(patterns []string) *Matcher {
	m := &Matcher{}
	for _, p := range patterns {
		m.add(p)
	}
	return m
}

func (m *Matcher) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.add(line)
	}
	return sc.Err()
}

func (m *Matcher) add(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return
	}
	p := pattern{raw: raw}
	if strings.HasPrefix(raw, "!") {
		p.neg = true
		raw = raw[1:]
	}
	raw = strings.TrimPrefix(raw, "./")
	if strings.HasSuffix(raw, "/") {
		p.dirOnly = true
		raw = strings.TrimSuffix(raw, "/")
	}
	if strings.HasPrefix(raw, "/") {
		p.anchored = true
		raw = strings.TrimPrefix(raw, "/")
	}
	p.segments = strings.Split(raw, "/")
	m.patterns = append(m.patterns, p)
}

// Ignore reports whether rel (slash-separated, relative to the scan root)
// should be skipped. isDir is true when rel names a directory.
func (m *Matcher) Ignore(rel string, isDir bool) bool {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "." || rel == "" {
		return false
	}
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			// A dir-only pattern still matches a file if a parent directory matches.
			if !parentDirMatch(p, rel) {
				continue
			}
		} else if !matchPattern(p, rel, isDir) {
			continue
		}
		ignored = !p.neg
	}
	return ignored
}

func parentDirMatch(p pattern, rel string) bool {
	parts := strings.Split(rel, "/")
	acc := ""
	for i := 0; i < len(parts)-1; i++ {
		if acc == "" {
			acc = parts[i]
		} else {
			acc += "/" + parts[i]
		}
		if matchPattern(p, acc, true) {
			return true
		}
	}
	return false
}

func matchPattern(p pattern, rel string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	relSegs := strings.Split(rel, "/")
	if hasDoubleStar(p.segments) {
		return matchGlob(strings.Join(p.segments, "/"), rel) ||
			(!p.anchored && matchAnySuffix(p, relSegs))
	}
	if p.anchored || len(p.segments) > 1 {
		return matchSegs(p.segments, relSegs, true)
	}
	// Unanchored single-segment pattern matches any path segment.
	pat := p.segments[0]
	for _, s := range relSegs {
		if globOK(pat, s) {
			return true
		}
	}
	return globOK(pat, rel)
}

func hasDoubleStar(segs []string) bool {
	for _, s := range segs {
		if s == "**" {
			return true
		}
	}
	return false
}

func matchAnySuffix(p pattern, relSegs []string) bool {
	// Try matching the pattern against every suffix of rel.
	for i := range relSegs {
		if matchGlob(strings.Join(p.segments, "/"), strings.Join(relSegs[i:], "/")) {
			return true
		}
	}
	return false
}

func matchSegs(pat, rel []string, full bool) bool {
	return matchSegsDepth(pat, rel, full, 0)
}

func matchSegsDepth(pat, rel []string, full bool, depth int) bool {
	if depth > 64 {
		return false
	}
	if len(pat) == 0 {
		return !full || len(rel) == 0
	}
	if pat[0] == "**" {
		// Eat zero or more segments.
		if matchSegsDepth(pat[1:], rel, full, depth+1) {
			return true
		}
		if len(rel) == 0 {
			return false
		}
		return matchSegsDepth(pat, rel[1:], full, depth+1)
	}
	if len(rel) == 0 {
		return false
	}
	if !globOK(pat[0], rel[0]) {
		return false
	}
	return matchSegsDepth(pat[1:], rel[1:], full, depth+1)
}

func matchGlob(pat, name string) bool {
	// Fast path: filepath.Match does not understand **.
	if !strings.Contains(pat, "**") {
		ok, err := filepath.Match(pat, name)
		return err == nil && ok
	}
	return matchSegs(strings.Split(pat, "/"), strings.Split(name, "/"), true)
}

func globOK(pat, name string) bool {
	if pat == "**" {
		return true
	}
	ok, err := filepath.Match(pat, name)
	return err == nil && ok
}
