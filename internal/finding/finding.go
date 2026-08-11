// Package finding defines the common result types produced by every language detector.
package finding

import (
	"fmt"
	"sort"
)

// Language is a supported source language.
type Language string

const (
	Go         Language = "go"
	JavaScript Language = "javascript"
	CSS        Language = "css"
	Python     Language = "python"
	Java       Language = "java"
)

// Kind classifies a dead-code finding.
type Kind string

const (
	UnusedFunction      Kind = "unused_function"
	UnusedMethod        Kind = "unused_method"
	UnusedType          Kind = "unused_type"
	UnusedVar           Kind = "unused_var"
	UnusedConst         Kind = "unused_const"
	UnreachableFunction Kind = "unreachable_function"
	UnusedExport        Kind = "unused_export"
	UnusedImport        Kind = "unused_import"
	UnusedFile          Kind = "unused_file"
	UnusedSelector      Kind = "unused_selector"
	UnusedKeyframes     Kind = "unused_keyframes"
)

// Finding is one dead-code report.
type Finding struct {
	Language Language `json:"language"`
	Kind     Kind     `json:"kind"`
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Name     string   `json:"name"`
	Message  string   `json:"message"`
}

// String formats a compiler-style diagnostic.
func (f Finding) String() string {
	col := f.Column
	if col <= 0 {
		col = 1
	}
	line := f.Line
	if line <= 0 {
		line = 1
	}
	return fmt.Sprintf("%s:%d:%d: %s", f.Path, line, col, f.Message)
}

// Sort orders findings by path, line, column, then name.
func Sort(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
}

// Summary counts findings by language.
func Summary(fs []Finding) map[Language]int {
	out := map[Language]int{}
	for _, f := range fs {
		out[f.Language]++
	}
	return out
}
