package c

import (
	"path/filepath"
	"strings"
)

// Resolver maps #include paths onto project-local headers.
type Resolver struct {
	root    string
	byRel   map[string]string // slash-rel / slash-base → abs
	byBase  map[string][]string
	headers []string
}

func newResolver(root string, headers []string) *Resolver {
	r := &Resolver{
		root:    root,
		byRel:   map[string]string{},
		byBase:  map[string][]string{},
		headers: headers,
	}
	for _, abs := range headers {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			rel = abs
		}
		rel = filepath.ToSlash(rel)
		base := filepath.Base(abs)
		r.byRel[rel] = abs
		r.byRel[base] = abs
		r.byBase[base] = append(r.byBase[base], abs)
		// include/foo.h also matches "foo.h" and <foo.h>
		for _, prefix := range []string{"include/", "inc/", "src/", "lib/", "headers/"} {
			if rest, ok := strings.CutPrefix(rel, prefix); ok {
				r.byRel[rest] = abs
			}
		}
	}
	return r
}

// Resolve finds a project header for #include "path" or <path>.
func (r *Resolver) Resolve(fromAbs, incPath string, system bool) string {
	if incPath == "" || r == nil {
		return ""
	}
	incPath = filepath.ToSlash(incPath)
	incPath = strings.TrimPrefix(incPath, "./")

	// Quoted includes: same directory first.
	if !system && fromAbs != "" {
		cand := filepath.Clean(filepath.Join(filepath.Dir(fromAbs), filepath.FromSlash(incPath)))
		for _, h := range r.headers {
			if sameFile(h, cand) {
				return h
			}
		}
	}
	if abs, ok := r.byRel[incPath]; ok {
		return abs
	}
	base := filepath.Base(incPath)
	if hits := r.byBase[base]; len(hits) == 1 {
		return hits[0]
	}
	// Unique suffix match: "dir/foo.h" vs ".../dir/foo.h"
	var suffix []string
	for _, h := range r.headers {
		rel, err := filepath.Rel(r.root, h)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == incPath || strings.HasSuffix(rel, "/"+incPath) {
			suffix = append(suffix, h)
		}
	}
	if len(suffix) == 1 {
		return suffix[0]
	}
	return ""
}

func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return aa == bb
}
