// Command dcd reports dead code in Go, JavaScript/TypeScript, CSS, Python, Java, and C.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/eric/deadcodedetector/internal/config"
	"github.com/eric/deadcodedetector/internal/engine"
	"github.com/eric/deadcodedetector/internal/report"
)

const usage = `dcd — dead code detector for Go, JavaScript/TypeScript, CSS, Python, Java, and C

Usage:
  dcd [flags] [path]

Path defaults to the current directory. Languages are auto-detected from
the files present unless -lang is set.

Flags:
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("dcd", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}

	lang := fs.String("lang", "", "comma-separated languages: go,js,css,py,java,c (default: auto-detect)")
	format := fs.String("format", "text", "output format: text, json, sarif")
	tests := fs.Bool("tests", true, "treat Go tests as entry points")
	exported := fs.String("exported", "auto", "report unused exported Go symbols: auto, true, false")
	reachable := fs.Bool("reachable", true, "run Go reachability analysis when a main package exists")
	timeout := fs.Duration("timeout", 0, "max time for Go reachability (0 = 45s; negative = no limit)")
	fail := fs.Bool("fail-on-findings", false, "exit 1 if any dead code is found")
	var entries, ignores multiFlag
	fs.Var(&entries, "entry", "JavaScript/Python/Java/C entry file (repeatable)")
	fs.Var(&ignores, "ignore", "gitignore-style pattern to skip (repeatable)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	cfg := config.Default()
	cfg.Format = *format
	cfg.Tests = *tests
	cfg.Reachable = *reachable
	cfg.Timeout = *timeout
	cfg.FailOnFindings = *fail
	cfg.Entries = entries
	cfg.Ignore = ignores

	langs, err := config.ParseLangs(*lang)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg.Langs = langs

	exp, err := config.ParseExported(*exported)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg.Exported = exp

	switch rest := fs.Args(); len(rest) {
	case 0:
		cfg.Root = "."
	case 1:
		cfg.Root = rest[0]
	default:
		fmt.Fprintln(os.Stderr, "dcd: extra arguments; pass a single path")
		return 2
	}

	findings, err := engine.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dcd: %v\n", err)
		return 1
	}
	if err := report.Write(os.Stdout, cfg.Format, findings); err != nil {
		fmt.Fprintf(os.Stderr, "dcd: %v\n", err)
		return 1
	}
	if cfg.FailOnFindings && len(findings) > 0 {
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
