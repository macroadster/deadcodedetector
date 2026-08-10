package javascript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

var jsExtensions = []string{
	".js", ".jsx", ".mjs", ".cjs",
	".ts", ".tsx", ".mts", ".cts",
}

// Resolver maps import specifiers to files on disk.
type Resolver struct {
	root    string
	files   map[string]string // slash-rel or abs lowercased path -> abs
	pkgMain []string
	tsPaths []tsPath
	baseURL string
}

type tsPath struct {
	prefix string // without *
	suffix string // after *
	target string // replacement pattern
	star   bool   // pattern contained *
}

func newResolver(root string, absFiles []string) *Resolver {
	r := &Resolver{
		root:  root,
		files: map[string]string{},
	}
	for _, abs := range absFiles {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		slash := filepath.ToSlash(rel)
		r.files[slash] = abs
		r.files[strings.ToLower(slash)] = abs
	}
	r.loadPackageJSON()
	r.loadTSConfig()
	return r
}

func (r *Resolver) loadPackageJSON() {
	b, err := os.ReadFile(filepath.Join(r.root, "package.json"))
	if err != nil {
		return
	}
	var pkg struct {
		Main    string          `json:"main"`
		Module  string          `json:"module"`
		Browser json.RawMessage `json:"browser"`
		Bin     json.RawMessage `json:"bin"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return
	}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		r.pkgMain = append(r.pkgMain, filepath.ToSlash(s))
	}
	add(pkg.Module)
	add(pkg.Main)
	var browser string
	if json.Unmarshal(pkg.Browser, &browser) == nil {
		add(browser)
	}
	var binStr string
	if json.Unmarshal(pkg.Bin, &binStr) == nil {
		add(binStr)
	}
	var binMap map[string]string
	if json.Unmarshal(pkg.Bin, &binMap) == nil {
		for _, v := range binMap {
			add(v)
		}
	}
	r.collectExports(pkg.Exports, add)
}

func (r *Resolver) collectExports(raw json.RawMessage, add func(string)) {
	r.collectExportsDepth(raw, add, 0)
}

func (r *Resolver) collectExportsDepth(raw json.RawMessage, add func(string), depth int) {
	if len(raw) == 0 || depth > 32 {
		return
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		add(s)
		return
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	for k, v := range m {
		if k == "types" || k == "typings" {
			continue
		}
		var inner string
		if json.Unmarshal(v, &inner) == nil {
			add(inner)
			continue
		}
		r.collectExportsDepth(v, add, depth+1)
	}
}

func (r *Resolver) loadTSConfig() {
	for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
		b, err := os.ReadFile(filepath.Join(r.root, name))
		if err != nil {
			continue
		}
		// tsconfig allows comments / trailing commas; strip crudely.
		b = stripJSONC(b)
		var cfg struct {
			CompilerOptions struct {
				BaseURL string              `json:"baseUrl"`
				Paths   map[string][]string `json:"paths"`
			} `json:"compilerOptions"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			continue
		}
		r.baseURL = cfg.CompilerOptions.BaseURL
		for pat, targets := range cfg.CompilerOptions.Paths {
			prefix, suffix, star := splitStar(pat)
			for _, t := range targets {
				r.tsPaths = append(r.tsPaths, tsPath{prefix: prefix, suffix: suffix, target: t, star: star})
			}
		}
		return
	}
}

func splitStar(s string) (prefix, suffix string, star bool) {
	i := strings.IndexByte(s, '*')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func stripJSONC(b []byte) []byte {
	// Remove // and /* */ comments outside of strings. Good enough for tsconfig.
	var out []byte
	i := 0
	inStr := byte(0)
	for i < len(b) {
		c := b[i]
		if inStr != 0 {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				out = append(out, b[i+1])
				i += 2
				continue
			}
			if c == inStr {
				inStr = 0
			}
			i++
			continue
		}
		if c == '"' || c == '\'' {
			inStr = c
			out = append(out, c)
			i++
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

// PackageEntries returns entry candidates from package.json.
func (r *Resolver) PackageEntries() []string {
	var out []string
	for _, e := range r.pkgMain {
		if abs := r.resolveFrom(r.root, e); abs != "" {
			out = append(out, abs)
		}
	}
	return out
}

// Resolve maps a specifier from file `fromAbs` to an absolute file path.
// Empty string means external / unresolved.
func (r *Resolver) Resolve(fromAbs, spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if spec[0] == '.' || spec[0] == '/' {
		base := filepath.Dir(fromAbs)
		if spec[0] == '/' {
			base = r.root
			spec = spec[1:]
		}
		return r.resolveFrom(base, spec)
	}
	// tsconfig paths / baseUrl
	if abs := r.resolveAlias(spec); abs != "" {
		return abs
	}
	// bare specifier: node_modules / builtin — treat as external
	return ""
}

func (r *Resolver) resolveAlias(spec string) string {
	for _, p := range r.tsPaths {
		if !p.star {
			if p.prefix == spec {
				return r.resolveFrom(r.root, p.target)
			}
			continue
		}
		if !strings.HasPrefix(spec, p.prefix) || !strings.HasSuffix(spec, p.suffix) {
			continue
		}
		midEnd := len(spec) - len(p.suffix)
		if midEnd < len(p.prefix) {
			continue
		}
		mid := spec[len(p.prefix):midEnd]
		target := strings.Replace(p.target, "*", mid, 1)
		if abs := r.resolveFrom(filepath.Join(r.root, r.baseURL), target); abs != "" {
			return abs
		}
		if abs := r.resolveFrom(r.root, target); abs != "" {
			return abs
		}
	}
	if r.baseURL != "" {
		if abs := r.resolveFrom(filepath.Join(r.root, r.baseURL), spec); abs != "" {
			return abs
		}
	}
	return ""
}

func (r *Resolver) resolveFrom(base, spec string) string {
	spec = filepath.Clean(spec)
	cand := filepath.Join(base, spec)
	if abs := r.existing(cand); abs != "" {
		return abs
	}
	for _, ext := range jsExtensions {
		if abs := r.existing(cand + ext); abs != "" {
			return abs
		}
	}
	for _, ext := range jsExtensions {
		if abs := r.existing(filepath.Join(cand, "index"+ext)); abs != "" {
			return abs
		}
	}
	// CSS/JSON side-effect imports
	for _, ext := range []string{".css", ".json", ".svg", ".css"} {
		if abs := r.existing(cand + ext); abs != "" {
			return abs
		}
		if strings.HasSuffix(strings.ToLower(cand), ext) {
			if abs := r.existing(cand); abs != "" {
				return abs
			}
		}
	}
	return ""
}

func (r *Resolver) existing(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(r.root, abs)
	if err != nil {
		return ""
	}
	slash := filepath.ToSlash(rel)
	if a, ok := r.files[slash]; ok {
		return a
	}
	if a, ok := r.files[strings.ToLower(slash)]; ok {
		return a
	}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		return abs
	}
	return ""
}
