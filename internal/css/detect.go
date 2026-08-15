// Package css detects unused selectors and keyframes.
package css

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"

	"github.com/eric/deadcodedetector/internal/finding"
	"github.com/eric/deadcodedetector/internal/javascript"
	"github.com/eric/deadcodedetector/internal/walk"
)

// Detect reports CSS selectors whose classes/ids/attrs never appear in the project.
func Detect(root string, files []walk.File) ([]finding.Finding, error) {
	var cssFiles, usageFiles []walk.File
	for _, f := range files {
		switch f.Lang {
		case "css":
			cssFiles = append(cssFiles, f)
		case "html", "js", "go":
			usageFiles = append(usageFiles, f)
		}
	}
	if len(cssFiles) == 0 {
		return nil, nil
	}

	idx := buildUsage(usageFiles)
	var fs []finding.Finding
	for _, f := range cssFiles {
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			return nil, err
		}
		if generatedCSS(src) {
			continue
		}
		rules, keys := parseStylesheet(src)
		usedKeyframes := map[string]bool{}
		for _, r := range rules {
			if r.ignored || alwaysLive(r) {
				continue
			}
			if !selectorUsed(r, idx) {
				fs = append(fs, finding.Finding{
					Language: finding.CSS,
					Kind:     finding.UnusedSelector,
					Path:     f.Rel,
					Line:     r.line,
					Column:   r.col,
					Name:     r.raw,
					Message:  fmt.Sprintf("unused selector %s", r.raw),
				})
				continue
			}
			for _, n := range r.animations {
				usedKeyframes[strings.ToLower(n)] = true
			}
		}
		for _, k := range keys {
			if k.ignored {
				continue
			}
			if usedKeyframes[strings.ToLower(k.name)] || idx.hasWord(k.name) {
				continue
			}
			fs = append(fs, finding.Finding{
				Language: finding.CSS,
				Kind:     finding.UnusedKeyframes,
				Path:     f.Rel,
				Line:     k.line,
				Column:   k.col,
				Name:     k.name,
				Message:  fmt.Sprintf("unused @keyframes %s", k.name),
			})
		}
	}
	_ = root
	_ = filepath.Separator
	return fs, nil
}

func generatedCSS(src []byte) bool {
	head := src
	if len(head) > 256 {
		head = head[:256]
	}
	s := string(head)
	return strings.Contains(s, "Code generated") || strings.Contains(s, "@generated")
}

type rule struct {
	raw        string
	line, col  int
	classes    []string
	ids        []string
	attrs      []string
	tags       []string
	animations []string
	ignored    bool
}

type keyframe struct {
	name      string
	line, col int
	ignored   bool
}

func parseStylesheet(src []byte) ([]rule, []keyframe) {
	input := parse.NewInputBytes(src)
	p := css.NewParser(input, false)
	var rules []rule
	var keys []keyframe
	inKeyframes := false
	ignoreNext := false

	lineAt := func(off int) (int, int) {
		if off < 0 {
			off = 0
		}
		if off > len(src) {
			off = len(src)
		}
		line, col := 1, 1
		for i := 0; i < off; i++ {
			if src[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		}
		return line, col
	}

	steps := 0
	limit := len(src) + 8
	for {
		steps++
		if steps > limit {
			return rules, keys
		}
		gt, _, data := p.Next()
		off := p.Offset()
		switch gt {
		case css.ErrorGrammar:
			return rules, keys
		case css.CommentGrammar:
			if commentIgnores(string(data)) {
				ignoreNext = true
			}
		case css.BeginAtRuleGrammar, css.AtRuleGrammar:
			name := strings.ToLower(strings.TrimPrefix(string(data), "@"))
			if strings.HasPrefix(name, "keyframes") || name == "keyframes" {
				// @keyframes name
				kfName := ""
				for _, v := range p.Values() {
					if v.TokenType == css.IdentToken {
						kfName = string(v.Data)
						break
					}
				}
				if kfName == "" {
					// data might be @keyframes, next value is the name — already in Values
					vals := p.Values()
					if len(vals) > 0 {
						kfName = strings.TrimSpace(string(vals[0].Data))
					}
				}
				// The at-keyword may be "@keyframes" with name in values.
				if strings.Contains(name, "keyframes") {
					if kfName == "" {
						parts := strings.Fields(string(data))
						if len(parts) > 1 {
							kfName = parts[1]
						}
					}
					if kfName != "" {
						ln, col := lineAt(tokenStart(src, off, data))
						keys = append(keys, keyframe{name: kfName, line: ln, col: col, ignored: ignoreNext})
					}
					inKeyframes = gt == css.BeginAtRuleGrammar
					ignoreNext = false
				}
			}
		case css.EndAtRuleGrammar:
			inKeyframes = false
		case css.BeginRulesetGrammar, css.QualifiedRuleGrammar:
			if inKeyframes {
				ignoreNext = false
				continue
			}
			toks := []css.Token{{TokenType: css.IdentToken, Data: data}}
			toks = append(toks, p.Values()...)
			raw := joinTokens(toks)
			end := off
			if end < 0 {
				end = 0
			}
			if end > len(src) {
				end = len(src)
			}
			start := tokenStart(src, end, data)
			if i := bytes.LastIndex(src[:end], []byte(strings.TrimSpace(raw))); i >= 0 {
				start = i
			}
			ln, col := lineAt(start)
			r := parseSelector(raw, ln, col)
			r.ignored = ignoreNext
			ignoreNext = false
			rules = append(rules, r)
		case css.DeclarationGrammar, css.CustomPropertyGrammar:
			prop := strings.ToLower(string(data))
			if strings.Contains(prop, "animation") {
				if len(rules) > 0 {
					for _, v := range p.Values() {
						if v.TokenType == css.IdentToken {
							ident := string(v.Data)
							if !isAnimKeyword(ident) {
								rules[len(rules)-1].animations = append(rules[len(rules)-1].animations, ident)
							}
						}
					}
				}
			}
		}
	}
}

func tokenStart(src []byte, off int, tok []byte) int {
	if off > len(src) {
		off = len(src)
	}
	if len(tok) == 0 || off == 0 {
		return off
	}
	idx := bytes.LastIndex(src[:off], tok)
	if idx < 0 {
		if off >= len(tok) {
			return off - len(tok)
		}
		return 0
	}
	return idx
}

func joinTokens(toks []css.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.Write(t.Data)
	}
	return strings.TrimSpace(b.String())
}

func isAnimKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "none", "infinite", "forwards", "backwards", "both", "alternate",
		"alternate-reverse", "linear", "ease", "ease-in", "ease-out",
		"ease-in-out", "normal", "reverse", "paused", "running",
		"inherit", "initial", "unset", "step-start", "step-end":
		return true
	}
	return false
}

func commentIgnores(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "dcd:ignore") || strings.Contains(low, "deadcode:ignore")
}

func parseSelector(raw string, line, col int) rule {
	r := rule{raw: raw, line: line, col: col}
	// Split on commas — a ruleset is unused only if EVERY comma-separated
	// selector is unused; we store one rule per full raw string and treat it
	// as used if any alternative is used.
	s := raw
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '.':
			name, n := readIdent(s[i+1:])
			if name != "" {
				r.classes = append(r.classes, name)
			}
			i += 1 + n
		case '#':
			name, n := readIdent(s[i+1:])
			if name != "" {
				r.ids = append(r.ids, name)
			}
			i += 1 + n
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				i++
				continue
			}
			body := s[i+1 : i+end]
			attr := body
			for _, sep := range []string{"~=", "|=", "^=", "$=", "*=", "="} {
				if j := strings.Index(body, sep); j >= 0 {
					attr = body[:j]
					break
				}
			}
			attr = strings.TrimSpace(attr)
			if attr != "" {
				r.attrs = append(r.attrs, attr)
			}
			i += end + 1
		case ':':
			// skip pseudo-class / pseudo-element
			i++
			if i < len(s) && s[i] == ':' {
				i++
			}
			_, n := readIdent(s[i:])
			i += n
			// skip ( ... )
			if i < len(s) && s[i] == '(' {
				depth := 1
				i++
				for i < len(s) && depth > 0 {
					if s[i] == '(' {
						depth++
					} else if s[i] == ')' {
						depth--
					}
					i++
				}
			}
		case '*', '>', '+', '~', ',', '(', ')', '{', '}':
			i++
		default:
			if isIdentStartByte(c) {
				name, n := readIdent(s[i:])
				if name != "" && !isPseudoLike(name) {
					r.tags = append(r.tags, name)
				}
				i += n
			} else {
				i++
			}
		}
	}
	return r
}

func isPseudoLike(s string) bool {
	switch strings.ToLower(s) {
	case "not", "is", "where", "has", "global", "local", "root", "host", "slotted",
		"nth-child", "nth-of-type", "nth-last-child", "nth-last-of-type",
		"first-child", "last-child", "only-child", "first-of-type", "last-of-type",
		"empty", "checked", "disabled", "enabled", "focus", "hover", "active",
		"visited", "link", "target", "before", "after", "lang":
		return true
	}
	return false
}

func readIdent(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	i := 0
	if s[0] == '\\' && len(s) > 1 {
		i = 2
	}
	for i < len(s) {
		c := s[i]
		if isIdentContinueByte(c) {
			i++
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			i += size
			continue
		}
		break
	}
	if i == 0 {
		return "", 0
	}
	return s[:i], i
}

func isIdentStartByte(c byte) bool {
	return c == '_' || c == '-' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c >= 0x80
}

func isIdentContinueByte(c byte) bool {
	return isIdentStartByte(c) || (c >= '0' && c <= '9')
}

var alwaysLiveTags = map[string]bool{
	"html": true, "body": true, "head": true, ":root": true, "root": true,
	"*": true,
}

func alwaysLive(r rule) bool {
	raw := strings.TrimSpace(r.raw)
	if raw == "*" || raw == ":root" || raw == "html" || raw == "body" {
		return true
	}
	// Pure pseudo-element / :root
	if len(r.classes) == 0 && len(r.ids) == 0 && len(r.attrs) == 0 {
		if len(r.tags) == 0 {
			return true
		}
		allLive := true
		for _, t := range r.tags {
			if !alwaysLiveTags[strings.ToLower(t)] {
				allLive = false
				break
			}
		}
		if allLive {
			return true
		}
		// Universal HTML tags (div, span, ...) are almost certainly used.
		// Only report "custom" tags (containing a hyphen) or uncommon ones
		// when they are the sole hook. Standard HTML tags are live.
		if onlyStandardHTMLTags(r.tags) && len(r.classes)+len(r.ids)+len(r.attrs) == 0 {
			return true
		}
	}
	return false
}

func onlyStandardHTMLTags(tags []string) bool {
	for _, t := range tags {
		if !standardHTML[strings.ToLower(t)] {
			return false
		}
	}
	return len(tags) > 0
}

var standardHTML = map[string]bool{
	"a": true, "abbr": true, "address": true, "area": true, "article": true,
	"aside": true, "audio": true, "b": true, "base": true, "bdi": true,
	"bdo": true, "blockquote": true, "body": true, "br": true, "button": true,
	"canvas": true, "caption": true, "cite": true, "code": true, "col": true,
	"colgroup": true, "data": true, "datalist": true, "dd": true, "del": true,
	"details": true, "dfn": true, "dialog": true, "div": true, "dl": true,
	"dt": true, "em": true, "embed": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "head": true, "header": true,
	"hgroup": true, "hr": true, "html": true, "i": true, "iframe": true,
	"img": true, "input": true, "ins": true, "kbd": true, "label": true,
	"legend": true, "li": true, "link": true, "main": true, "map": true,
	"mark": true, "menu": true, "meta": true, "meter": true, "nav": true,
	"noscript": true, "object": true, "ol": true, "optgroup": true,
	"option": true, "output": true, "p": true, "picture": true, "pre": true,
	"progress": true, "q": true, "rp": true, "rt": true, "ruby": true,
	"s": true, "samp": true, "script": true, "search": true, "section": true,
	"select": true, "slot": true, "small": true, "source": true, "span": true,
	"strong": true, "style": true, "sub": true, "summary": true, "sup": true,
	"svg": true, "table": true, "tbody": true, "td": true, "template": true,
	"textarea": true, "tfoot": true, "th": true, "thead": true, "time": true,
	"title": true, "tr": true, "track": true, "u": true, "ul": true,
	"var": true, "video": true, "wbr": true, "path": true, "g": true,
	"circle": true, "rect": true, "line": true, "polyline": true,
	"polygon": true, "text": true, "use": true, "defs": true, "clippath": true,
}

func selectorUsed(r rule, idx *usage) bool {
	// A comma-separated selector list is used if ANY alternative is used.
	parts := splitSelectors(r.raw)
	if len(parts) > 1 {
		for _, part := range parts {
			sub := parseSelector(part, r.line, r.col)
			if selectorUsedAtomic(sub, idx) {
				return true
			}
		}
		return false
	}
	return selectorUsedAtomic(r, idx)
}

func splitSelectors(raw string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(raw[start:]))
	return parts
}

func selectorUsedAtomic(r rule, idx *usage) bool {
	// Used if every class, id, and attr hook appears somewhere.
	// Tags alone are handled by alwaysLive; remaining custom tags must appear.
	for _, c := range r.classes {
		if !idx.hasClass(c) {
			return false
		}
	}
	for _, id := range r.ids {
		if !idx.hasID(id) {
			return false
		}
	}
	for _, a := range r.attrs {
		if !idx.hasAttr(a) {
			return false
		}
	}
	for _, t := range r.tags {
		if standardHTML[strings.ToLower(t)] || alwaysLiveTags[strings.ToLower(t)] {
			continue
		}
		if !idx.hasTag(t) {
			return false
		}
	}
	// If the selector has no hooks at all, keep it.
	if len(r.classes)+len(r.ids)+len(r.attrs) == 0 {
		return true
	}
	return true
}

type usage struct {
	classes map[string]bool
	ids     map[string]bool
	attrs   map[string]bool
	tags    map[string]bool
	words   map[string]bool
}

func (u *usage) hasClass(s string) bool {
	return u.classes[s] || u.words[s]
}
func (u *usage) hasID(s string) bool { return u.ids[s] || u.words[s] }
func (u *usage) hasAttr(s string) bool {
	return u.attrs[s] || u.words[s] || u.attrs[strings.ToLower(s)]
}
func (u *usage) hasTag(s string) bool  { return u.tags[strings.ToLower(s)] || u.words[s] }
func (u *usage) hasWord(s string) bool { return u.words[s] }

func buildUsage(files []walk.File) *usage {
	u := &usage{
		classes: map[string]bool{},
		ids:     map[string]bool{},
		attrs:   map[string]bool{},
		tags:    map[string]bool{},
		words:   map[string]bool{},
	}
	for _, f := range files {
		src, err := os.ReadFile(f.Abs)
		if err != nil {
			continue
		}
		harvestUsage(src, f.Lang, u)
	}
	return u
}

var (
	reClassAttr = regexp.MustCompile(`(?i)(?:class|classname|classlist)\s*[=:]\s*['"]([^'"]+)['"]`)
	reIDAttr    = regexp.MustCompile(`(?i)\bid\s*[=:]\s*['"]([^'"]+)['"]`)
	reHTMLTag   = regexp.MustCompile(`</?([A-Za-z][A-Za-z0-9:-]*)`)
	reAttrName  = regexp.MustCompile(`\s([A-Za-z_:][\w:.-]*)\s*=`)
	reString    = regexp.MustCompile(`(?s)(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'|` + "`" + `((?:\\.|[^\\` + "`" + `])*)` + "`" + `)`)
	reJSXClass  = regexp.MustCompile(`(?i)class(?:Name)?\s*=\s*\{\s*['"]([^'"]+)['"]`)
)

func harvestUsage(src []byte, lang string, u *usage) {
	s := string(src)
	addClassBag := func(bag string) {
		for _, w := range cssTokens(bag) {
			u.classes[w] = true
			u.words[w] = true
		}
	}
	for _, m := range reClassAttr.FindAllStringSubmatch(s, -1) {
		addClassBag(m[1])
	}
	for _, m := range reJSXClass.FindAllStringSubmatch(s, -1) {
		addClassBag(m[1])
	}
	for _, m := range reIDAttr.FindAllStringSubmatch(s, -1) {
		id := strings.TrimSpace(m[1])
		if id != "" {
			u.ids[id] = true
			u.words[id] = true
		}
	}
	if lang == "html" || lang == "js" {
		for _, m := range reHTMLTag.FindAllStringSubmatch(s, -1) {
			u.tags[strings.ToLower(m[1])] = true
		}
		for _, m := range reAttrName.FindAllStringSubmatch(s, -1) {
			u.attrs[m[1]] = true
			u.attrs[strings.ToLower(m[1])] = true
		}
	}
	// Quoted strings and template literals. Split on interpolation /
	// punctuation so `sl-cell${on ? ' is-on' : ''}` yields sl-cell and is-on.
	// JS/TS uses the real tokenizer so an apostrophe in prose cannot
	// swallow the following template (regex harvest does that).
	var lits []string
	if lang == "js" {
		lits = javascript.StringLiterals(src)
	} else {
		for _, m := range reString.FindAllStringSubmatch(s, -1) {
			if lit := m[1] + m[2] + m[3]; lit != "" {
				lits = append(lits, lit)
			}
		}
	}
	for _, lit := range lits {
		u.words[lit] = true
		for _, w := range cssTokens(lit) {
			u.words[w] = true
		}
		// styles.foo / cls.foo
		if i := strings.LastIndexByte(lit, '.'); i >= 0 && i+1 < len(lit) {
			u.words[lit[i+1:]] = true
		}
	}
	// Identifiers after a dot: styles.foo, classList.add — token scan
	harvestIdents(src, u)
}

// cssTokens pulls CSS-identifier-like pieces out of a string or template.
// `foo bar${x ? ' is-on' : ”}` → foo, bar, is-on (not `bar${x`).
func cssTokens(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		if !isCSSTokenStart(s[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && isCSSTokenCont(s[j]) {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

func isCSSTokenStart(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isCSSTokenCont(c byte) bool {
	return isCSSTokenStart(c) || (c >= '0' && c <= '9')
}

func harvestIdents(src []byte, u *usage) {
	i := 0
	isIdent := func(c byte) bool {
		return c == '_' || c == '$' || c == '-' ||
			(c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
	}
	for i < len(src) {
		c := src[i]
		// skip comments/strings roughly — strings already harvested
		if c == '/' && i+1 < len(src) && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			q := c
			i++
			for i < len(src) && src[i] != q {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i++
			continue
		}
		if c == '.' && i+1 < len(src) && isIdentStartByte(src[i+1]) {
			j := i + 1
			for j < len(src) && isIdent(src[j]) {
				j++
			}
			u.words[string(src[i+1:j])] = true
			i = j
			continue
		}
		i++
	}
}
