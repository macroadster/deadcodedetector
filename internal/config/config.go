// Package config holds shared detector options.
package config

import (
	"fmt"
	"strings"
)

// Config is the user-facing analysis configuration.
type Config struct {
	// Root is the directory to scan. Empty means the current directory.
	Root string

	// Patterns are Go package patterns (default ["./..."]).
	Patterns []string

	// Langs restricts analysis to these languages ("go", "js", "css").
	// Empty means auto-detect from files present.
	Langs []string

	// Tests includes Go test files / generated test mains as entry points.
	Tests bool

	// Exported controls reporting of unused exported Go symbols.
	// nil means auto: report them when a main package exists.
	Exported *bool

	// Reachable enables Rapid Type Analysis for Go when a main (or test main)
	// is available. Functions that are referenced only from dead code are then
	// reported as unreachable.
	Reachable bool

	// Entries are extra JavaScript entry-point files (relative to Root or absolute).
	Entries []string

	// Ignore is a list of gitignore-style patterns relative to Root.
	Ignore []string

	// Format is "text", "json", or "sarif".
	Format string

	// FailOnFindings makes the process exit 1 when any finding is produced.
	FailOnFindings bool
}

// Default returns a Config with sensible defaults.
func Default() Config {
	return Config{
		Root:      ".",
		Patterns:  []string{"./..."},
		Tests:     true,
		Reachable: true,
		Format:    "text",
	}
}

// Wants reports whether the given language should run.
func (c Config) Wants(lang string, detected map[string]bool) bool {
	if len(c.Langs) == 0 {
		return detected[lang]
	}
	for _, l := range c.Langs {
		if normalizeLang(l) == lang {
			return true
		}
	}
	return false
}

// ParseLangs splits a comma-separated -lang value.
func ParseLangs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n := normalizeLang(p)
		if n == "" {
			return nil, fmt.Errorf("unknown language %q (want go, js, css)", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// ParseExported parses the -exported flag: auto, true, false.
func ParseExported(s string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return nil, nil
	case "true", "1", "yes":
		v := true
		return &v, nil
	case "false", "0", "no":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid -exported %q (want auto, true, false)", s)
	}
}

func normalizeLang(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "go", "golang":
		return "go"
	case "js", "javascript", "ts", "typescript":
		return "js"
	case "css":
		return "css"
	default:
		return ""
	}
}
