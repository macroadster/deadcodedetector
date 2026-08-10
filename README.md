# dcd — dead code detector

A single CLI that finds unused and unreachable code in **Go**, **JavaScript/TypeScript**, **CSS**, and **Python**.

```
dcd [flags] [path]
```

`path` defaults to the current directory. Languages are auto-detected from the files that are present.

## Install

```bash
go install github.com/eric/deadcodedetector/cmd/dcd@latest
```

Or from this repo:

```bash
go build -o dcd ./cmd/dcd
./dcd .
```

## What it reports

| Language | Findings |
|---|---|
| **Go** | Unused package-level functions, methods, types, consts, and vars. When a `main` (or test main) exists, Rapid Type Analysis also reports functions that are referenced only from dead code — the official [`deadcode`](https://go.dev/blog/deadcode) algorithm. |
| **JavaScript / TypeScript** | Unreachable files, unused exports, unused imports, and unused top-level functions / classes / variables. Follows `import` / `export`, `require` / `module.exports`, `import()`, JSX tags, `package.json` entry fields, `tsconfig` path aliases, and HTML `<script src>`. |
| **CSS** | Selectors whose classes, IDs, or attributes never appear in HTML / JS / Go templates, plus unused `@keyframes`. |
| **Python** | Unreachable modules, unused imports, and unused top-level functions / classes / variables. Follows `import` / `from … import` (including nested imports), package-relative imports, `__main__` guards, and pytest-style test discovery. |

The tool prefers **false negatives over false positives**. If a use cannot be proven, the symbol is kept.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-lang go,js,css,py` | auto-detect | Restrict which analyzers run |
| `-format text\|json\|sarif` | `text` | Output format |
| `-tests` | `true` | Treat Go tests as entry points |
| `-exported auto\|true\|false` | `auto` | Report unused exported Go symbols (`auto` = yes when a `main` exists) |
| `-reachable` | `true` | Run Go RTA when a main package exists |
| `-timeout dur` | `45s` | Max time for Go RTA (`0` = 45s; negative = no limit). Heavy import graphs (btcd, libp2p, IPFS, …) skip RTA instead of hanging. |
| `-entry path` | | Extra JavaScript/Python entry file (repeatable) |
| `-ignore glob` | | Extra gitignore-style skip pattern (repeatable) |
| `-fail-on-findings` | `false` | Exit `1` if anything is found |

## Examples

```bash
# Whole repo, every language present
dcd .

# Go only, fail CI on findings
dcd -lang go -fail-on-findings ./...

# Library: do not treat exported API as dead
dcd -lang go -exported=false .

# JS with an explicit entry
dcd -lang js -entry src/main.tsx .

# Python only
dcd -lang py .

# Machine-readable
dcd -format json . > dead.json
dcd -format sarif . > dcd.sarif
```

## Ignore rules

Default skips include `node_modules/`, `vendor/`, `dist/`, `build/`, `.git/`, `testdata/`, minified bundles, and generated `*.pb.go` / `*_gen.go` files. `.gitignore` and `.dcdignore` in the scan root are honoured.

Suppress one symbol:

```go
// dcd:ignore
func legacyHook() {}
```

```js
// dcd:ignore
export function legacyHook() {}
```

```python
# dcd:ignore
def legacy_hook():
    pass
```

```css
/* dcd:ignore */
.legacy-modal { display: none; }
```

`//nolint:dcd`, `//deadcode:ignore`, `# noqa: F401`, and `//dcd:ignore-file` / `# dcd:ignore-file` also work.

## How each analyzer works

**Go.** Loads packages with `go/packages`, counts `types.Info` uses, and keeps methods that are required to implement an interface in the module. With `-reachable` and a `main`/`go test` entry, it builds SSA and runs [`rta.Analyze`](https://pkg.go.dev/golang.org/x/tools/go/callgraph/rta). Generated files (`Code generated … DO NOT EDIT.`) are skipped.

**JavaScript.** Tokenizes JS/TS/JSX, extracts imports/exports/declarations, builds a module graph from the entries above, and mark-and-sweeps. Type-only `import type` / `export type` / `interface` are ignored. Dynamic `import(expr)` is treated conservatively.

**CSS.** Parses stylesheets, then looks for class / id / attribute / string / `styles.foo` occurrences in HTML, JS, and Go files. Standard tags (`div`, `body`, `:root`, …) are never reported. A selector is unused only if one of its hooks is missing entirely.

**Python.** Tokenizes Python, extracts imports / top-level defs / uses (including f-string interpolations), builds a module graph from `__main__` guards, test files, and conventional entry names, then mark-and-sweeps. Package `__init__` re-exports and names listed in `__all__` are kept. Methods inside classes are not reported (prefer false negatives).

## Limitations

- Reflection, `//go:linkname`, and cgo-only callees can hide Go uses (same class of unsoundness as `golang.org/x/tools/cmd/deadcode`).
- Go Rapid Type Analysis is skipped (stderr warning) on huge import graphs (btcd, libp2p, IPFS, Kubernetes, cloud SDKs) or when `-timeout` fires, so a scan cannot hang. Unused-reference analysis still runs.
- JS computed member access (`obj[name]`, `import(variable)`) is not resolved.
- CSS does not expand Sass/Less; only `.css` is parsed. Dynamically concatenated class names may look unused.
- TypeScript types are skipped heuristically, not by a full TS compiler.
- Python `importlib`, `getattr`, and string-based dynamic imports are not fully resolved; class methods are not analyzed.

## Development

```bash
go test ./...
go build -o dcd ./cmd/dcd
```
