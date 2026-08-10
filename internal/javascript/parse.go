package javascript

import (
	"strings"
)

// Extracted is the module-level fact base for one JS/TS file.
type Extracted struct {
	Imports []Import
	Exports []Export
	Decls   []Decl
	Uses    []Use
	Strings []string // string/template literals (for CSS cross-ref)
	// DynamicSpecs are import() / require() specifiers when they are strings.
	DynamicSpecs []string
	// HasDynamicUnknown is true when import()/require() uses a non-literal.
	HasDynamicUnknown bool
	// IgnoreAll is set by a file-level dcd:ignore comment.
	IgnoreAll bool
}

// Import is a static import or require().
type Import struct {
	Specifier  string
	Default    string // local name of default import
	Namespace  string // local name of * as ns
	Named      []Named
	TypeOnly   bool
	SideEffect bool
	Line, Col  int
	// CommonJS require assigned to a local: const x = require('m')
	CJS bool
	// resolved is the absolute file path of Specifier, if local.
	resolved string
}

// Named maps a remote export name to a local binding.
type Named struct {
	Remote   string
	Local    string
	TypeOnly bool
}

// Export is a name this module makes visible to importers.
type Export struct {
	Name      string // exported name ("default" for default)
	Local     string // local binding, if any
	Line, Col int
	Kind      string // function, class, var, reexport, default
	From      string // re-export source specifier
	TypeOnly  bool
	Ignored   bool
}

// Decl is a top-level binding.
type Decl struct {
	Name      string
	Line, Col int
	Kind      string // function, class, var, import
	Exported  bool
	Ignored   bool
}

// Use is a free identifier (or JSX tag) reference.
type Use struct {
	Name      string
	Line, Col int
	// Member is set when this is ns.Member (namespace import use).
	Member string
}

func extract(src []byte) Extracted {
	ex := Extracted{}
	if bytesContainsIgnore(src) && fileIgnore(src) {
		ex.IgnoreAll = true
	}
	toks := tokenize(src)
	p := &parser{toks: toks, src: src}
	p.parse(&ex)
	return ex
}

func bytesContainsIgnore(src []byte) bool {
	s := strings.ToLower(string(src))
	return strings.Contains(s, "dcd:ignore") || strings.Contains(s, "deadcode:ignore")
}

func fileIgnore(src []byte) bool {
	// First non-empty comment is a file-level directive.
	s := string(src)
	low := strings.ToLower(s)
	// If the first 1KB has a file-ignore marker not attached to a symbol, treat as whole file.
	head := low
	if len(head) > 1024 {
		head = head[:1024]
	}
	return strings.Contains(head, "dcd:ignore-file") || strings.Contains(head, "deadcode:ignore-file")
}

type parser struct {
	toks []token
	i    int
	src  []byte
}

func (p *parser) peek() token {
	if p.i >= len(p.toks) {
		return token{kind: tEOF}
	}
	return p.toks[p.i]
}

func (p *parser) peekN(n int) token {
	if p.i+n >= len(p.toks) {
		return token{kind: tEOF}
	}
	return p.toks[p.i+n]
}

func (p *parser) next() token {
	t := p.peek()
	if p.i < len(p.toks) {
		p.i++
	}
	return t
}

func (p *parser) acceptPunct(s string) bool {
	if p.peek().kind == tPunct && p.peek().lit == s {
		p.i++
		return true
	}
	return false
}

func (p *parser) acceptIdent(s string) bool {
	if p.peek().kind == tIdent && p.peek().lit == s {
		p.i++
		return true
	}
	return false
}

func (p *parser) parse(ex *Extracted) {
	// Hard cap: every production must consume a token or we force one.
	// Without this, a stray ",", ")", or "}" at statement level loops
	// forever — parseExprish returns without advancing and parse retries.
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		start := p.i
		if p.skipTypeOnlyStatement(ex) {
			p.finishStep(start)
			continue
		}
		t := p.peek()
		if t.kind == tIdent {
			switch t.lit {
			case "import":
				// import() is dynamic; import ... is static.
				if p.peekN(1).kind == tPunct && p.peekN(1).lit == "(" {
					p.parseDynamicImport(ex)
				} else {
					p.parseImport(ex)
				}
				p.finishStep(start)
				continue
			case "export":
				p.parseExport(ex)
				p.finishStep(start)
				continue
			case "require":
				p.parseRequireCall(ex, "", 0, 0)
				p.finishStep(start)
				continue
			}
		}
		// Top-level declaration?
		if p.looksLikeDecl() {
			p.parseDecl(ex, false, false)
			p.finishStep(start)
			continue
		}
		p.parseExprish(ex, 0)
		p.finishStep(start)
	}
}

// finishStep consumes one token when a production made no progress.
func (p *parser) finishStep(start int) {
	if p.i <= start && p.peek().kind != tEOF {
		p.next()
	}
}

func (p *parser) looksLikeDecl() bool {
	t := p.peek()
	if t.kind != tIdent {
		return false
	}
	switch t.lit {
	case "async", "function", "class", "const", "let", "var", "enum":
		return true
	}
	return false
}

func (p *parser) skipTypeOnlyStatement(ex *Extracted) bool {
	t := p.peek()
	if t.kind != tIdent {
		return false
	}
	switch t.lit {
	case "interface", "type":
		// type Foo = ... or interface Foo
		// but `type` can appear as a member / import type already handled.
		// Only skip at statement start when next is Ident (or { for type {).
		n := p.peekN(1)
		if t.lit == "type" && n.kind != tIdent {
			return false
		}
		p.next()
		if t.lit == "type" {
			// type Alias<T> = ...
			if p.peek().kind == tIdent {
				p.next()
			}
			p.skipGeneric()
			if p.acceptPunct("=") {
				p.skipTypeExpr()
			}
			p.acceptPunct(";")
		} else {
			// interface Name { ... }  or interface Name<T> { ... }
			if p.peek().kind == tIdent {
				p.next()
			}
			p.skipGeneric()
			if p.peek().kind == tIdent && p.peek().lit == "extends" {
				p.next()
				p.skipUntil(func(t token) bool { return t.kind == tPunct && t.lit == "{" })
			}
			p.skipBalanced("{", "}")
			p.acceptPunct(";")
		}
		_ = ex
		return true
	case "declare":
		p.next()
		if p.peek().kind == tIdent {
			switch p.peek().lit {
			case "global", "module", "namespace":
				p.next()
				if p.peek().kind == tString || p.peek().kind == tIdent {
					p.next()
				}
				p.skipGeneric()
				p.skipBalanced("{", "}")
				p.acceptPunct(";")
				return true
			}
		}
		p.skipUntilStmtEnd()
		return true
	}
	return false
}

func (p *parser) skipGeneric() {
	if p.peek().kind == tPunct && p.peek().lit == "<" {
		p.skipBalanced("<", ">")
	}
}

// skipTypeExpr consumes a TypeScript type and stops before the next value
// statement. Previously skipUntilStmtEnd kept going after the alias's
// closing `}`, so `type Props = { ... }` swallowed the following
// `export function` / `useState(...)`.
func (p *parser) skipTypeExpr() {
	depthParen, depthBrace, depthBrack, depthAngle := 0, 0, 0, 0
	started := false
	steps := 0
	limit := len(p.toks) + 2
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		t := p.peek()
		if depthParen == 0 && depthBrace == 0 && depthBrack == 0 && depthAngle == 0 && started {
			if t.kind == tPunct {
				switch t.lit {
				case ";", ",", ")", "]", "}":
					return
				}
			}
			if t.kind == tIdent && isValueStmtStart(t.lit) {
				// `type X = import("m").Y` is still a type.
				if t.lit == "import" && p.peekN(1).kind == tPunct && p.peekN(1).lit == "(" {
					// continue
				} else {
					return
				}
			}
		}
		if t.kind == tPunct {
			switch t.lit {
			case "(":
				depthParen++
			case ")":
				if depthParen == 0 {
					return
				}
				depthParen--
			case "{":
				depthBrace++
			case "}":
				if depthBrace == 0 {
					return
				}
				depthBrace--
			case "[":
				depthBrack++
			case "]":
				if depthBrack == 0 {
					return
				}
				depthBrack--
			case "<":
				depthAngle++
			case ">":
				if depthAngle > 0 {
					depthAngle--
				}
			}
		}
		p.next()
		started = true
	}
}

func isValueStmtStart(s string) bool {
	switch s {
	case "export", "import", "function", "class", "const", "let", "var",
		"async", "interface", "type", "declare", "enum", "return",
		"if", "for", "while", "switch", "try", "throw", "break",
		"continue", "do", "with":
		return true
	}
	return false
}

func (p *parser) skipUntilStmtEnd() {
	depth := 0
	started := false
	steps := 0
	limit := len(p.toks) + 2
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		t := p.peek()
		if depth == 0 && started {
			if t.kind == tPunct && t.lit == ";" {
				p.next()
				return
			}
			if t.kind == tIdent && isValueStmtStart(t.lit) {
				return
			}
		}
		if t.kind == tPunct {
			switch t.lit {
			case "{", "(", "[", "<":
				if t.lit == "<" && depth == 0 && started {
					// Comparison or generic in a type; treat as generic nest
					// only when it looks like one, otherwise ignore.
					if !looksLikeGeneric(p) {
						p.next()
						started = true
						continue
					}
				}
				depth++
				p.next()
				started = true
				continue
			case "}", ")", "]", ">":
				if depth > 0 {
					depth--
					p.next()
					if depth == 0 {
						nt := p.peek()
						if nt.kind == tPunct && (nt.lit == "|" || nt.lit == "&" || nt.lit == "[" || nt.lit == "?" || nt.lit == "." || nt.lit == "<") {
							started = true
							continue
						}
						p.acceptPunct(";")
						return
					}
					continue
				}
				if t.lit == "}" || t.lit == ";" {
					p.next()
					return
				}
			case ";":
				if depth == 0 {
					p.next()
					return
				}
			}
		}
		p.next()
		started = true
	}
}

func (p *parser) skipUntil(pred func(token) bool) {
	for p.peek().kind != tEOF && !pred(p.peek()) {
		p.next()
	}
}

func (p *parser) skipBalanced(open, close string) {
	if !p.acceptPunct(open) {
		return
	}
	depth := 1
	steps := 0
	limit := len(p.toks) + 2
	for p.peek().kind != tEOF && depth > 0 {
		steps++
		if steps > limit {
			return
		}
		t := p.next()
		if t.kind == tPunct {
			if t.lit == open {
				depth++
			} else if t.lit == close {
				depth--
			}
		}
	}
}

func (p *parser) parseImport(ex *Extracted) {
	kw := p.next() // import
	imp := Import{Line: kw.line, Col: kw.col}
	// import type ...
	if p.peek().kind == tIdent && p.peek().lit == "type" && p.peekN(1).kind != tIdent {
		// import type { ... } or import type * or import type Def from
		p.next()
		imp.TypeOnly = true
	} else if p.peek().kind == tIdent && p.peek().lit == "type" && (p.peekN(1).kind == tIdent || (p.peekN(1).kind == tPunct && p.peekN(1).lit == "{")) {
		p.next()
		imp.TypeOnly = true
	}

	// import "mod"
	if p.peek().kind == tString {
		imp.Specifier = p.next().lit
		imp.SideEffect = true
		ex.Imports = append(ex.Imports, imp)
		p.acceptPunct(";")
		return
	}

	// default and/or namespace and/or named
	if p.peek().kind == tIdent && p.peek().lit != "from" {
		name := p.next()
		// import type Foo - already consumed type
		imp.Default = name.lit
		p.acceptPunct(",")
	}
	if p.acceptPunct("*") {
		p.acceptIdent("as")
		if p.peek().kind == tIdent {
			imp.Namespace = p.next().lit
		}
	} else if p.peek().kind == tPunct && p.peek().lit == "{" {
		imp.Named = p.parseNamedSpecifiers()
	}
	p.acceptIdent("from")
	if p.peek().kind == tString {
		imp.Specifier = p.next().lit
	}
	p.acceptPunct(";")
	if imp.Default == "" && imp.Namespace == "" && len(imp.Named) == 0 {
		imp.SideEffect = true
	}
	ex.Imports = append(ex.Imports, imp)
	// Local decls for imported names
	if imp.Default != "" {
		ex.Decls = append(ex.Decls, Decl{Name: imp.Default, Line: imp.Line, Col: imp.Col, Kind: "import"})
	}
	if imp.Namespace != "" {
		ex.Decls = append(ex.Decls, Decl{Name: imp.Namespace, Line: imp.Line, Col: imp.Col, Kind: "import"})
	}
	for _, n := range imp.Named {
		if n.Local != "" {
			ex.Decls = append(ex.Decls, Decl{Name: n.Local, Line: imp.Line, Col: imp.Col, Kind: "import"})
		}
	}
}

func (p *parser) parseNamedSpecifiers() []Named {
	var out []Named
	if !p.acceptPunct("{") {
		return out
	}
	for p.peek().kind != tEOF && !(p.peek().kind == tPunct && p.peek().lit == "}") {
		typeOnly := p.acceptIdent("type")
		var remote, local string
		if p.peek().kind == tIdent || p.peek().kind == tString {
			remote = p.next().lit
			local = remote
		} else {
			p.next()
			continue
		}
		if p.acceptIdent("as") {
			if p.peek().kind == tIdent {
				local = p.next().lit
			}
		}
		out = append(out, Named{Remote: remote, Local: local, TypeOnly: typeOnly})
		p.acceptPunct(",")
	}
	p.acceptPunct("}")
	return out
}

func (p *parser) parseExport(ex *Extracted) {
	kw := p.next() // export
	ignored := p.prevIgnored()
	typeOnly := false
	if p.peek().kind == tIdent && p.peek().lit == "type" {
		// export type { ... } or export type Foo =
		n1 := p.peekN(1)
		if n1.kind == tPunct && n1.lit == "{" {
			p.next()
			typeOnly = true
		} else if n1.kind == tIdent {
			p.next() // type
			if p.peek().kind == tIdent {
				p.next() // Alias
			}
			p.skipGeneric()
			if p.acceptPunct("=") {
				p.skipTypeExpr()
			}
			p.acceptPunct(";")
			return
		}
	}
	if p.peek().kind == tIdent && p.peek().lit == "default" {
		p.next()
		p.parseDefaultExport(ex, kw.line, kw.col, ignored)
		return
	}
	if p.peek().kind == tIdent && (p.peek().lit == "interface" || p.peek().lit == "declare") {
		p.skipTypeOnlyStatement(ex)
		return
	}
	if p.acceptPunct("*") {
		asName := ""
		if p.acceptIdent("as") && p.peek().kind == tIdent {
			asName = p.next().lit
		}
		p.acceptIdent("from")
		from := ""
		if p.peek().kind == tString {
			from = p.next().lit
		}
		name := "*"
		if asName != "" {
			name = asName
		}
		ex.Exports = append(ex.Exports, Export{
			Name: name, Local: asName, Line: kw.line, Col: kw.col,
			Kind: "reexport", From: from, TypeOnly: typeOnly, Ignored: ignored,
		})
		p.acceptPunct(";")
		return
	}
	if p.peek().kind == tPunct && p.peek().lit == "{" {
		named := p.parseNamedSpecifiers()
		from := ""
		if p.acceptIdent("from") && p.peek().kind == tString {
			from = p.next().lit
		}
		for _, n := range named {
			ex.Exports = append(ex.Exports, Export{
				Name: n.Remote, Local: n.Local, Line: kw.line, Col: kw.col,
				Kind: "reexport", From: from, TypeOnly: typeOnly || n.TypeOnly, Ignored: ignored,
			})
			if from == "" && n.Local != "" {
				// export { local as remote } — the "Local" in Named is the exported alias
				// and Remote is the local binding when we parsed `local as alias`.
				// parseNamedSpecifiers: remote=first, local=after as.
				// For `export { foo as bar }`, Remote=foo (local binding), Local=bar (exported name).
				ex.Exports[len(ex.Exports)-1].Name = n.Local
				ex.Exports[len(ex.Exports)-1].Local = n.Remote
				ex.Uses = append(ex.Uses, Use{Name: n.Remote, Line: kw.line, Col: kw.col})
			}
			if from != "" {
				// Re-export does not create a local use.
			}
		}
		p.acceptPunct(";")
		return
	}
	// export function/class/const/async
	p.parseDecl(ex, true, ignored)
}

func (p *parser) parseDefaultExport(ex *Extracted, line, col int, ignored bool) {
	name := "default"
	local := ""
	kind := "default"
	t := p.peek()
	if t.kind == tIdent && (t.lit == "async" || t.lit == "function" || t.lit == "class") {
		// May have a name.
		saved := p.i
		if t.lit == "async" {
			p.next()
		}
		if p.peek().kind == tIdent && (p.peek().lit == "function" || p.peek().lit == "class") {
			p.next()
			p.acceptPunct("*")
			if p.peek().kind == tIdent && !isKeyword(p.peek().lit) {
				local = p.peek().lit
				dline, dcol := p.peek().line, p.peek().col
				p.next()
				ex.Decls = append(ex.Decls, Decl{Name: local, Line: dline, Col: dcol, Kind: "function", Exported: true, Ignored: ignored})
			}
		} else {
			p.i = saved
		}
		p.consumeSignatureAndBody(ex)
	} else {
		p.parseExprish(ex, 0)
	}
	ex.Exports = append(ex.Exports, Export{
		Name: name, Local: local, Line: line, Col: col, Kind: kind, Ignored: ignored,
	})
}

func (p *parser) parseDecl(ex *Extracted, exported, ignored bool) {
	t := p.peek()
	if t.kind != tIdent {
		p.next()
		return
	}
	switch t.lit {
	case "async":
		p.next()
		p.parseDecl(ex, exported, ignored)
	case "function":
		p.next()
		p.acceptPunct("*")
		p.skipGeneric()
		if p.peek().kind == tIdent {
			id := p.next()
			ex.Decls = append(ex.Decls, Decl{Name: id.lit, Line: id.line, Col: id.col, Kind: "function", Exported: exported, Ignored: ignored})
			if exported {
				ex.Exports = append(ex.Exports, Export{Name: id.lit, Local: id.lit, Line: id.line, Col: id.col, Kind: "function", Ignored: ignored})
			}
		}
		p.consumeSignatureAndBody(ex)
	case "class":
		p.next()
		p.skipGeneric()
		if p.peek().kind == tIdent && !isKeyword(p.peek().lit) {
			id := p.next()
			ex.Decls = append(ex.Decls, Decl{Name: id.lit, Line: id.line, Col: id.col, Kind: "class", Exported: exported, Ignored: ignored})
			if exported {
				ex.Exports = append(ex.Exports, Export{Name: id.lit, Local: id.lit, Line: id.line, Col: id.col, Kind: "class", Ignored: ignored})
			}
		}
		if p.acceptIdent("extends") {
			p.parseExprish(ex, 1)
		}
		p.skipGeneric()
		p.collectUntilBalanced(ex, "{", "}")
	case "const", "let", "var", "enum":
		kind := "var"
		if t.lit == "enum" {
			kind = "enum"
		}
		p.next()
		if kind == "enum" {
			if p.peek().kind == tIdent {
				id := p.next()
				ex.Decls = append(ex.Decls, Decl{Name: id.lit, Line: id.line, Col: id.col, Kind: kind, Exported: exported, Ignored: ignored})
				if exported {
					ex.Exports = append(ex.Exports, Export{Name: id.lit, Local: id.lit, Line: id.line, Col: id.col, Kind: kind, Ignored: ignored})
				}
			}
			p.skipBalanced("{", "}")
			return
		}
		p.parseBindingList(ex, exported, ignored, kind)
	default:
		p.parseExprish(ex, 0)
	}
}

func (p *parser) parseBindingList(ex *Extracted, exported, ignored bool, kind string) {
	steps := 0
	limit := len(p.toks) + 2
	for {
		steps++
		if steps > limit {
			break
		}
		start := p.i
		p.parseBinding(ex, exported, ignored, kind)
		// initializer
		if p.acceptPunct("=") {
			p.parseExprish(ex, 1)
		}
		if p.i <= start {
			// e.g. `const` with no binding — do not spin.
			break
		}
		if !p.acceptPunct(",") {
			break
		}
	}
	p.acceptPunct(";")
}

func (p *parser) parseBinding(ex *Extracted, exported, ignored bool, kind string) {
	t := p.peek()
	if t.kind == tIdent {
		id := p.next()
		// Type annotation
		if p.peek().kind == tPunct && p.peek().lit == ":" {
			p.skipTypeAnnot()
		}
		ex.Decls = append(ex.Decls, Decl{Name: id.lit, Line: id.line, Col: id.col, Kind: kind, Exported: exported, Ignored: ignored})
		if exported {
			ex.Exports = append(ex.Exports, Export{Name: id.lit, Local: id.lit, Line: id.line, Col: id.col, Kind: kind, Ignored: ignored})
		}
		return
	}
	if t.kind == tPunct && (t.lit == "{" || t.lit == "[") {
		close := "}"
		if t.lit == "[" {
			close = "]"
		}
		p.next()
		for p.peek().kind != tEOF && !(p.peek().kind == tPunct && p.peek().lit == close) {
			if p.peek().kind == tIdent {
				id := p.next()
				if p.acceptIdent("as") || p.acceptPunct(":") {
					// { foo: bar } destructure — bar is the local
					if p.peek().kind == tIdent {
						id = p.next()
					}
				}
				p.skipTypeAnnot()
				ex.Decls = append(ex.Decls, Decl{Name: id.lit, Line: id.line, Col: id.col, Kind: kind, Exported: exported, Ignored: ignored})
				if exported {
					ex.Exports = append(ex.Exports, Export{Name: id.lit, Local: id.lit, Line: id.line, Col: id.col, Kind: kind, Ignored: ignored})
				}
			} else {
				p.next()
			}
			p.acceptPunct(",")
		}
		p.acceptPunct(close)
		p.skipTypeAnnot()
	}
}

func (p *parser) skipTypeAnnot() {
	if !p.acceptPunct(":") {
		return
	}
	// Skip a TypeScript type. Stop at , = ; ) ] } at depth 0.
	// `{` after a complete type is a function body (`(): string {`), not
	// an object type. Object types are `{` immediately after `:` / `|` / `&`.
	depthParen, depthBrack, depthAngle := 0, 0, 0
	depthBrace := 0
	started := false
	var prev token
	steps := 0
	limit := len(p.toks) + 2
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		t := p.peek()
		atTop := depthParen == 0 && depthBrack == 0 && depthBrace == 0 && depthAngle == 0
		if atTop && started {
			if t.kind == tIdent && isValueStmtStart(t.lit) {
				if !(t.lit == "import" && p.peekN(1).kind == tPunct && p.peekN(1).lit == "(") {
					return
				}
			}
			if t.kind == tPunct {
				switch t.lit {
				case ",", ";", "=", ")", "]", "}":
					return
				case "=>":
					// `(): (x: T) => U` continues; `(): U =>` is an arrow body.
					if !(prev.kind == tPunct && prev.lit == ")") {
						return
					}
				case "{":
					// Continue only when the type is still being joined.
					if !typeContinuesWithBrace(prev) {
						return
					}
				}
			}
		}
		if t.kind == tPunct {
			switch t.lit {
			case "<":
				depthAngle++
			case ">":
				if depthAngle > 0 {
					depthAngle--
				}
			case "(":
				depthParen++
			case ")":
				if depthParen == 0 {
					return
				}
				depthParen--
			case "[":
				depthBrack++
			case "]":
				if depthBrack == 0 {
					return
				}
				depthBrack--
			case "{":
				depthBrace++
			case "}":
				if depthBrace == 0 {
					return
				}
				depthBrace--
			}
		}
		prev = t
		started = true
		p.next()
	}
}

func typeContinuesWithBrace(prev token) bool {
	if prev.kind != tPunct {
		return false
	}
	switch prev.lit {
	case "|", "&", ":", "=>", "=", ",", "<", "(":
		return true
	}
	return false
}

func (p *parser) consumeSignatureAndBody(ex *Extracted) {
	p.skipGeneric()
	if p.acceptIdent("extends") {
		p.parseExprish(ex, 1)
	}
	p.skipGeneric()
	if p.peek().kind == tPunct && p.peek().lit == "(" {
		p.collectUntilBalanced(ex, "(", ")")
	}
	p.skipTypeAnnot()
	if p.peek().kind == tPunct && p.peek().lit == "{" {
		p.collectUntilBalanced(ex, "{", "}")
	} else if p.acceptPunct("=>") {
		if p.peek().kind == tPunct && p.peek().lit == "{" {
			p.collectUntilBalanced(ex, "{", "}")
		} else {
			p.parseExprish(ex, 1)
		}
	}
	p.acceptPunct(";")
}

func (p *parser) collectUntilBalanced(ex *Extracted, open, close string) {
	if !p.acceptPunct(open) {
		return
	}
	depth := 1
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF && depth > 0 {
		steps++
		if steps > limit {
			return
		}
		start := p.i
		t := p.peek()
		if t.kind == tPunct {
			if t.lit == open {
				depth++
				p.next()
				continue
			}
			if t.lit == close {
				depth--
				p.next()
				continue
			}
		}
		p.collectAtom(ex)
		p.finishStep(start)
	}
}

func (p *parser) collectAtom(ex *Extracted) {
	t := p.peek()
	switch t.kind {
	case tString, tTemplate:
		ex.Strings = append(ex.Strings, t.lit)
		if t.kind == tTemplate {
			addTemplateIdentUses(ex, t)
		}
		p.next()
	case tIdent:
		if t.lit == "require" && p.peekN(1).kind == tPunct && p.peekN(1).lit == "(" {
			p.parseRequireCall(ex, "", t.line, t.col)
			return
		}
		if t.lit == "import" && p.peekN(1).kind == tPunct && p.peekN(1).lit == "(" {
			p.parseDynamicImport(ex)
			return
		}
		p.consumeIdentUse(ex)
	case tPunct:
		if t.lit == "<" && p.maybeJSX(ex) {
			return
		}
		p.next()
	default:
		if t.kind != tEOF {
			p.next()
		}
	}
}

func (p *parser) parseDynamicImport(ex *Extracted) {
	p.next() // import
	p.acceptPunct("(")
	if p.peek().kind == tString {
		ex.DynamicSpecs = append(ex.DynamicSpecs, p.next().lit)
	} else {
		ex.HasDynamicUnknown = true
		p.parseExprish(ex, 1)
	}
	// closing ) consumed by expr or here
	p.acceptPunct(")")
}

func (p *parser) parseRequireCall(ex *Extracted, assignTo string, line, col int) {
	if p.peek().kind == tIdent && p.peek().lit == "require" {
		p.next()
	}
	if !p.acceptPunct("(") {
		return
	}
	spec := ""
	if p.peek().kind == tString {
		spec = p.next().lit
	} else {
		ex.HasDynamicUnknown = true
		p.parseExprish(ex, 1)
	}
	p.acceptPunct(")")
	if spec == "" {
		return
	}
	imp := Import{Specifier: spec, Line: line, Col: col, CJS: true}
	if line == 0 {
		imp.Line = p.peek().line
	}
	if assignTo != "" {
		imp.Namespace = assignTo // treat CJS require as namespace
	}
	ex.Imports = append(ex.Imports, imp)
	ex.DynamicSpecs = append(ex.DynamicSpecs, spec)
}

func (p *parser) parseExprish(ex *Extracted, stopDepth int) {
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		start := p.i
		t := p.peek()
		if t.kind == tPunct {
			switch t.lit {
			case "{", "(", "[":
				close := map[string]string{"{": "}", "(": ")", "[": "]"}[t.lit]
				p.collectUntilBalanced(ex, t.lit, close)
				if stopDepth == 1 {
					return
				}
				p.finishStep(start)
				continue
			case ";":
				p.next()
				return
			case ",":
				// Nested expression (stopDepth==1) or JSX `{a, b}` caller
				// needs the comma left in place. At true top-level, consume
				// so `require('a'), require('b')` does not hang.
				if stopDepth == 1 {
					return
				}
				p.next()
				continue
			case "}", ")", "]":
				// Leave the closer for the caller (JSX `{expr}`, grouping).
				// parse() finishStep consumes a stray closer at top level.
				return
			}
		}
		if t.kind == tIdent && stopDepth == 0 {
			if t.lit == "export" {
				return
			}
			if t.lit == "import" && !(p.peekN(1).kind == tPunct && p.peekN(1).lit == "(") {
				return
			}
			if p.looksLikeDecl() {
				return
			}
		}
		p.collectAtom(ex)
		p.finishStep(start)
		if stopDepth == 1 {
			nt := p.peek()
			if nt.kind == tPunct && (nt.lit == "," || nt.lit == ";" || nt.lit == "{" || nt.lit == ")" || nt.lit == "}") {
				return
			}
		}
	}
}

func addTemplateIdentUses(ex *Extracted, t token) {
	lit := t.lit
	i := 0
	for i < len(lit) {
		if !isIdentStart(lit[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(lit) && isIdentContinue(lit[j]) {
			j++
		}
		name := lit[i:j]
		i = j
		if isKeyword(name) {
			continue
		}
		ex.Uses = append(ex.Uses, Use{Name: name, Line: t.line, Col: t.col})
	}
}

func (p *parser) consumeIdentUse(ex *Extracted) {
	id := p.next()
	if isKeyword(id.lit) && id.lit != "of" && id.lit != "as" && id.lit != "from" && id.lit != "type" {
		// Some keywords are not uses. `of`/`as`/`from` can be idents in non-keyword position
		// but we already consumed keyword forms in statement parsers.
		// Still, `this`, `super`, etc. are not bindings.
		switch id.lit {
		case "this", "super", "true", "false", "null", "undefined", "new", "typeof",
			"void", "delete", "instanceof", "in", "return", "throw", "else", "if",
			"while", "for", "switch", "case", "break", "continue", "try", "catch",
			"finally", "default", "with", "yield", "await", "class", "function",
			"var", "let", "const", "export", "import", "extends", "static":
			return
		}
	}
	// Skip label: ident:
	if p.peek().kind == tPunct && p.peek().lit == ":" {
		// Could be object key or label or ternary is elsewhere. Conservative: if
		// previous was `{` or `,` it's a key. We don't track prev easily; treat
		// ident: as a key (not a use) when next is not `:` of type assertion.
		// Type assertion `x as Type` handled by keyword.
		// Object key `foo:` is not a use of foo.
		p.next() // :
		return
	}
	member := ""
	// ns.foo or ns?.foo
	if (p.peek().kind == tPunct && (p.peek().lit == "." || p.peek().lit == "?.")) || false {
		p.next()
		if p.peek().kind == tIdent {
			member = p.peek().lit
			p.next()
		}
	}
	// Skip TS non-null !
	p.acceptPunct("!")
	// Skip generic foo<T>(
	if p.peek().kind == tPunct && p.peek().lit == "<" {
		// Could be JSX or generic. Generic if followed by ident and > or ,
		if looksLikeGeneric(p) {
			p.skipBalanced("<", ">")
		}
	}
	ex.Uses = append(ex.Uses, Use{Name: id.lit, Line: id.line, Col: id.col, Member: member})
}

func looksLikeGeneric(p *parser) bool {
	// <Ident ... >
	if p.peekN(1).kind != tIdent {
		return false
	}
	// Scan ahead a bit for closing >
	for i := 1; i < 12 && p.i+i < len(p.toks); i++ {
		t := p.toks[p.i+i]
		if t.kind == tPunct && t.lit == ">" {
			return true
		}
		if t.kind == tPunct && (t.lit == ";" || t.lit == "{" || t.lit == "=>") {
			return false
		}
	}
	return false
}

func (p *parser) maybeJSX(ex *Extracted) bool {
	// JSX: <Ident ...>  not comparison, not generic
	if looksLikeGeneric(p) {
		return false
	}
	// Comparison if previous token was ident/number/)/]
	if p.i > 0 {
		prev := p.toks[p.i-1]
		if prev.kind == tIdent || prev.kind == tNumber || (prev.kind == tPunct && (prev.lit == ")" || prev.lit == "]" || prev.lit == "}")) {
			return false
		}
	}
	if p.peekN(1).kind != tIdent && !(p.peekN(1).kind == tPunct && p.peekN(1).lit == ">") {
		return false
	}
	p.next() // <
	if p.peek().kind == tIdent {
		id := p.next()
		// lowercase = intrinsic DOM tag, not a binding
		if id.lit != "" && (id.lit[0] < 'a' || id.lit[0] > 'z') {
			ex.Uses = append(ex.Uses, Use{Name: id.lit, Line: id.line, Col: id.col})
		}
		// Foo.Bar
		if p.acceptPunct(".") && p.peek().kind == tIdent {
			p.next()
		}
	}
	// attributes
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return true
		}
		start := p.i
		t := p.peek()
		if t.kind == tPunct && (t.lit == ">" || t.lit == "/") {
			if t.lit == "/" {
				p.next()
			}
			p.acceptPunct(">")
			return true
		}
		if t.kind == tString || t.kind == tTemplate {
			ex.Strings = append(ex.Strings, t.lit)
			p.next()
			continue
		}
		if t.kind == tIdent {
			// attr name
			p.next()
			if p.acceptPunct("=") {
				if p.peek().kind == tPunct && p.peek().lit == "{" {
					p.next() // {
					p.parseExprish(ex, 0)
					p.acceptPunct("}")
					continue
				}
			}
			p.finishStep(start)
			continue
		}
		p.finishStep(start)
	}
	return true
}

func (p *parser) prevIgnored() bool {
	// Look at source around the current token for a same-line or previous-line ignore.
	if p.i <= 0 || p.i-1 >= len(p.toks) {
		return false
	}
	t := p.toks[p.i-1]
	return lineHasIgnore(p.src, t.line)
}

func lineHasIgnore(src []byte, line int) bool {
	lines := strings.Split(string(src), "\n")
	check := func(i int) bool {
		if i < 0 || i >= len(lines) {
			return false
		}
		low := strings.ToLower(lines[i])
		return strings.Contains(low, "dcd:ignore") || strings.Contains(low, "deadcode:ignore") ||
			strings.Contains(low, "nolint:dcd")
	}
	return check(line-1) || check(line-2)
}
