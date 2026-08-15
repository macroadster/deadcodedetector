// Package engine runs the selected language detectors and collects findings.
package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eric/deadcodedetector/internal/config"
	cssdet "github.com/eric/deadcodedetector/internal/css"
	"github.com/eric/deadcodedetector/internal/finding"
	godet "github.com/eric/deadcodedetector/internal/golang"
	"github.com/eric/deadcodedetector/internal/ignore"
	javadet "github.com/eric/deadcodedetector/internal/java"
	jsdet "github.com/eric/deadcodedetector/internal/javascript"
	pydet "github.com/eric/deadcodedetector/internal/python"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Run discovers files under cfg.Root and executes the requested detectors.
func Run(cfg config.Config) ([]finding.Finding, error) {
	root := cfg.Root
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	m, err := ignore.New(abs, cfg.Ignore)
	if err != nil {
		return nil, err
	}
	files, err := walk.Discover(abs, m)
	if err != nil {
		return nil, err
	}
	detected := walk.DetectedLangs(files)

	var fs []finding.Finding
	if cfg.Wants("go", detected) {
		gofs, err := godet.Detect(godet.Options{
			Root:      abs,
			Patterns:  cfg.Patterns,
			Tests:     cfg.Tests,
			Exported:  cfg.Exported,
			Reachable: cfg.Reachable,
			Timeout:   cfg.Timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("go: %w", err)
		}
		fs = append(fs, gofs...)
	}
	if cfg.Wants("js", detected) {
		jsfs, err := jsdet.Detect(abs, files, cfg.Entries)
		if err != nil {
			return nil, fmt.Errorf("javascript: %w", err)
		}
		fs = append(fs, jsfs...)
	}
	if cfg.Wants("css", detected) {
		cssfs, err := cssdet.Detect(abs, files)
		if err != nil {
			return nil, fmt.Errorf("css: %w", err)
		}
		fs = append(fs, cssfs...)
	}
	if cfg.Wants("py", detected) {
		pyfs, err := pydet.Detect(abs, files, cfg.Entries)
		if err != nil {
			return nil, fmt.Errorf("python: %w", err)
		}
		fs = append(fs, pyfs...)
	}
	if cfg.Wants("java", detected) {
		jfs, err := javadet.Detect(abs, files, cfg.Entries)
		if err != nil {
			return nil, fmt.Errorf("java: %w", err)
		}
		fs = append(fs, jfs...)
	}
	finding.Sort(fs)
	return fs, nil
}
