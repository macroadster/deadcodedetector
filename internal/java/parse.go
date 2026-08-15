package java

import (
	"strings"
)

// Extracted is the compilation-unit fact base for one Java file.
type Extracted struct {
	Package string
	Imports []Import
	Types   []TypeDecl
	Members []Member
	Uses    []Use
	Strings []string
	// HasMain is true when the file declares public static void main.
	HasMain bool
	// IgnoreAll is set by a file-level dcd:ignore-file comment.
	IgnoreAll bool
	// TypeAnnotations are simple names of annotations on any type in the file.
	TypeAnnotations []string
}

// Import is a single-type or static import.
type Import struct {
	// Qual is the imported name without a trailing .*.
	Qual string
	// Name is the simple binding (last component, or static member).
	Name string
	Star bool
	// Static is true for `import static`.
	Static    bool
	Line, Col int
	// resolved is the absolute path of the local type, if any.
	resolved string
}

// TypeDecl is a class, interface, enum, record, or annotation type.
type TypeDecl struct {
	Name        string
	Kind        string // class, interface, enum, record, annotation
	Nested      bool
	Public      bool
	Line, Col   int
	Ignored     bool
	Annotations []string
	Enclosing   string
}

// Member is a method, field, or constructor.
type Member struct {
	Name        string
	Kind        string // method, field, constructor
	TypeName    string
	Private     bool
	Public      bool
	Static      bool
	Line, Col   int
	Ignored     bool
	Annotations []string
	IsMain      bool
}

// Use is an identifier or dotted reference.
type Use struct {
	Name      string
	Member    string
	Chain     []string
	Line, Col int
}

func extract(src []byte) Extracted {
	ex := Extracted{}
	if fileIgnore(src) {
		ex.IgnoreAll = true
	}
	toks := tokenize(src)
	p := &parser{toks: toks, src: src}
	p.parse(&ex)
	return ex
}

type parser struct {
	toks []token
	i    int
	src  []byte
	// typeStack is the nest of type names being parsed.
	typeStack []string
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

func (p *parser) acceptIdent(s string) bool {
	if p.peek().kind == tIdent && p.peek().lit == s {
		p.i++
		return true
	}
	return false
}

func (p *parser) acceptPunct(s string) bool {
	if p.peek().kind == tPunct && p.peek().lit == s {
		p.i++
		return true
	}
	return false
}

func (p *parser) parse(ex *Extracted) {
	steps := 0
	limit := len(p.toks)*3 + 16
	// package
	if p.acceptIdent("package") {
		ex.Package = p.parseDottedName()
		p.acceptPunct(";")
	}
	// imports
	for p.peek().kind == tIdent && p.peek().lit == "import" {
		p.parseImport(ex)
		if steps++; steps > limit {
			return
		}
	}
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		start := p.i
		if p.parseTypeOrSkip(ex, false) {
			p.finish(start)
			continue
		}
		p.parseExprish(ex, 0)
		p.finish(start)
	}
}

func (p *parser) finish(start int) {
	if p.i <= start && p.peek().kind != tEOF {
		p.next()
	}
}

func (p *parser) parseImport(ex *Extracted) {
	kw := p.next() // import
	imp := Import{Line: kw.line, Col: kw.col}
	if p.acceptIdent("static") {
		imp.Static = true
	}
	var parts []string
	for p.peek().kind == tIdent {
		parts = append(parts, p.peek().lit)
		p.next()
		if !p.acceptPunct(".") {
			break
		}
		if p.peek().kind == tPunct && p.peek().lit == "*" {
			imp.Star = true
			p.next()
			break
		}
	}
	p.acceptPunct(";")
	if len(parts) == 0 {
		return
	}
	imp.Qual = strings.Join(parts, ".")
	imp.Name = parts[len(parts)-1]
	ex.Imports = append(ex.Imports, imp)
}

func (p *parser) parseDottedName() string {
	var parts []string
	for p.peek().kind == tIdent {
		parts = append(parts, p.peek().lit)
		p.next()
		if !p.acceptPunct(".") {
			break
		}
	}
	return strings.Join(parts, ".")
}

type mods struct {
	Public      bool
	Private     bool
	Protected   bool
	Static      bool
	Annotations []string
	Ignored     bool
}

func (p *parser) parseTypeOrSkip(ex *Extracted, nested bool) bool {
	m := p.collectMods(ex)
	// @interface
	if p.peek().kind == tPunct && p.peek().lit == "@" &&
		p.peekN(1).kind == tIdent && p.peekN(1).lit == "interface" {
		p.next() // @
		p.next() // interface
		p.parseTypeRest(ex, "annotation", nested, m)
		return true
	}
	if p.peek().kind == tIdent && isTypeKeyword(p.peek().lit) {
		kind := p.next().lit
		p.parseTypeRest(ex, kind, nested, m)
		return true
	}
	// Put mods' tokens back? We already consumed them. If this is not a type,
	// the caller will parseExprish from here — annotations were recorded as uses.
	return false
}

func (p *parser) collectMods(ex *Extracted) mods {
	var m mods
	steps := 0
	for steps < 64 {
		steps++
		t := p.peek()
		if t.kind == tPunct && t.lit == "@" {
			name, ignored := p.parseAnnotation(ex)
			if name != "" {
				m.Annotations = append(m.Annotations, name)
			}
			if ignored {
				m.Ignored = true
			}
			continue
		}
		if t.kind == tIdent && isModifier(t.lit) {
			switch t.lit {
			case "public":
				m.Public = true
			case "private":
				m.Private = true
			case "protected":
				m.Protected = true
			case "static":
				m.Static = true
			}
			p.next()
			continue
		}
		break
	}
	return m
}

func (p *parser) parseAnnotation(ex *Extracted) (name string, ignored bool) {
	at := p.next() // @
	if p.peek().kind != tIdent {
		return "", lineHasIgnore(p.src, at.line)
	}
	// QualName, keep the simple (last) component.
	var last string
	for p.peek().kind == tIdent {
		last = p.peek().lit
		p.recordUse(ex, p.peek(), "")
		p.next()
		if !p.acceptPunct(".") {
			break
		}
	}
	if p.peek().kind == tPunct && p.peek().lit == "(" {
		p.skipBalanced("(", ")", ex)
	}
	ignored = lineHasIgnore(p.src, at.line)
	if last == "SuppressWarnings" {
		// Check nearby source for "unused".
		ignored = ignored || lineHasIgnore(p.src, at.line)
	}
	return last, ignored
}

func (p *parser) parseTypeRest(ex *Extracted, kind string, nested bool, m mods) {
	nameTok := p.peek()
	if nameTok.kind != tIdent {
		return
	}
	p.next()
	td := TypeDecl{
		Name:        nameTok.lit,
		Kind:        kind,
		Nested:      nested,
		Public:      m.Public,
		Line:        nameTok.line,
		Col:         nameTok.col,
		Ignored:     m.Ignored || lineHasIgnore(p.src, nameTok.line),
		Annotations: m.Annotations,
	}
	if len(p.typeStack) > 0 {
		td.Enclosing = p.typeStack[len(p.typeStack)-1]
	}
	ex.Types = append(ex.Types, td)
	ex.TypeAnnotations = append(ex.TypeAnnotations, m.Annotations...)

	p.skipGenerics(ex)
	if kind == "record" && p.peek().kind == tPunct && p.peek().lit == "(" {
		p.parseRecordHeader(ex, nameTok.lit, m)
	}
	// extends / implements / permits
	for {
		t := p.peek()
		if t.kind == tIdent && (t.lit == "extends" || t.lit == "implements" || t.lit == "permits") {
			p.next()
			p.parseTypeList(ex)
			continue
		}
		break
	}
	if p.peek().kind != tPunct || p.peek().lit != "{" {
		return
	}
	p.typeStack = append(p.typeStack, nameTok.lit)
	p.parseTypeBody(ex, nameTok.lit, kind)
	if len(p.typeStack) > 0 {
		p.typeStack = p.typeStack[:len(p.typeStack)-1]
	}
}

func (p *parser) parseRecordHeader(ex *Extracted, typeName string, m mods) {
	// (Type name, Type name)
	p.next() // (
	for p.peek().kind != tEOF && !(p.peek().kind == tPunct && p.peek().lit == ")") {
		// optional annotations / final
		for (p.peek().kind == tPunct && p.peek().lit == "@") ||
			(p.peek().kind == tIdent && (p.peek().lit == "final" || isModifier(p.peek().lit))) {
			if p.peek().lit == "@" {
				p.parseAnnotation(ex)
			} else {
				p.next()
			}
		}
		p.parseTypeRef(ex)
		if p.peek().kind == tIdent {
			n := p.next()
			ex.Members = append(ex.Members, Member{
				Name:     n.lit,
				Kind:     "field",
				TypeName: typeName,
				Public:   true, // record components are API
				Line:     n.line,
				Col:      n.col,
				Ignored:  true, // never report — canonical accessors
			})
		}
		if !p.acceptPunct(",") {
			break
		}
	}
	p.acceptPunct(")")
	_ = m
}

func (p *parser) parseTypeList(ex *Extracted) {
	for {
		p.parseTypeRef(ex)
		if !p.acceptPunct(",") {
			return
		}
	}
}

func (p *parser) parseTypeBody(ex *Extracted, typeName, kind string) {
	p.next() // {
	if kind == "enum" {
		p.parseEnumConstants(ex, typeName)
	}
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF && !(p.peek().kind == tPunct && p.peek().lit == "}") {
		steps++
		if steps > limit {
			break
		}
		start := p.i
		if p.peek().kind == tPunct && p.peek().lit == ";" {
			p.next()
			continue
		}
		m := p.collectMods(ex)
		// nested type
		if p.peek().kind == tPunct && p.peek().lit == "@" &&
			p.peekN(1).kind == tIdent && p.peekN(1).lit == "interface" {
			p.next()
			p.next()
			p.parseTypeRest(ex, "annotation", true, m)
			p.finish(start)
			continue
		}
		if p.peek().kind == tIdent && isTypeKeyword(p.peek().lit) {
			k := p.next().lit
			p.parseTypeRest(ex, k, true, m)
			p.finish(start)
			continue
		}
		// static / instance initializer
		if p.peek().kind == tPunct && p.peek().lit == "{" {
			p.skipBalanced("{", "}", ex)
			p.finish(start)
			continue
		}
		p.parseMember(ex, typeName, m)
		p.finish(start)
	}
	p.acceptPunct("}")
}

func (p *parser) parseEnumConstants(ex *Extracted, typeName string) {
	steps := 0
	for p.peek().kind != tEOF && steps < 10000 {
		steps++
		if p.peek().kind == tPunct && (p.peek().lit == ";" || p.peek().lit == "}") {
			if p.peek().lit == ";" {
				p.next()
			}
			return
		}
		// annotations on constants
		for p.peek().kind == tPunct && p.peek().lit == "@" {
			p.parseAnnotation(ex)
		}
		if p.peek().kind != tIdent {
			return
		}
		// A member starts with a type name followed by another ident / < / [.
		if p.looksLikeMemberStart() {
			return
		}
		n := p.next()
		ex.Members = append(ex.Members, Member{
			Name:     n.lit,
			Kind:     "field",
			TypeName: typeName,
			Public:   true,
			Static:   true,
			Line:     n.line,
			Col:      n.col,
			Ignored:  true, // enum constants: prefer FN
		})
		if p.peek().kind == tPunct && p.peek().lit == "(" {
			p.skipBalanced("(", ")", ex)
		}
		if p.peek().kind == tPunct && p.peek().lit == "{" {
			p.skipBalanced("{", "}", ex)
		}
		if p.acceptPunct(",") {
			continue
		}
		if p.acceptPunct(";") {
			return
		}
		return
	}
}

func (p *parser) looksLikeMemberStart() bool {
	// ident <...> ident  or ident ident or primitive ident
	if p.peek().kind != tIdent {
		return false
	}
	if isModifier(p.peek().lit) || isTypeKeyword(p.peek().lit) {
		return true
	}
	j := 1
	// skip generics after first ident
	if p.peekN(j).kind == tPunct && p.peekN(j).lit == "<" {
		depth := 1
		j++
		for p.peekN(j).kind != tEOF && depth > 0 && j < 64 {
			if p.peekN(j).kind == tPunct && p.peekN(j).lit == "<" {
				depth++
			}
			if p.peekN(j).kind == tPunct && p.peekN(j).lit == ">" {
				depth--
			}
			j++
		}
	}
	for p.peekN(j).kind == tPunct && p.peekN(j).lit == "[" {
		j++
		if p.peekN(j).kind == tPunct && p.peekN(j).lit == "]" {
			j++
		}
	}
	return p.peekN(j).kind == tIdent
}

func (p *parser) parseMember(ex *Extracted, typeName string, m mods) {
	// Optional method type parameters.
	if p.peek().kind == tPunct && p.peek().lit == "<" {
		p.skipGenerics(ex)
	}
	kind, name, ok := p.classifyMember(ex, typeName)
	if !ok {
		p.parseExprish(ex, 0)
		return
	}
	mem := Member{
		Name:        name.lit,
		Kind:        kind,
		TypeName:    typeName,
		Private:     m.Private,
		Public:      m.Public,
		Static:      m.Static,
		Line:        name.line,
		Col:         name.col,
		Ignored:     m.Ignored || lineHasIgnore(p.src, name.line),
		Annotations: m.Annotations,
	}
	if kind == "method" && name.lit == "main" && m.Static {
		mem.IsMain = true
		ex.HasMain = true
	}
	ex.Members = append(ex.Members, mem)

	if kind == "field" {
		p.parseFieldRest(ex)
		return
	}
	// constructor / method: consume (params) throws? body-or-semi
	if p.peek().kind == tPunct && p.peek().lit == "(" {
		p.skipBalanced("(", ")", ex)
	}
	if p.acceptIdent("throws") {
		p.parseTypeList(ex)
	}
	if p.peek().kind == tPunct && p.peek().lit == "{" {
		p.skipBalanced("{", "}", ex)
		return
	}
	// abstract / interface method, or compact record constructor already handled
	p.acceptPunct(";")
}

func (p *parser) classifyMember(ex *Extracted, typeName string) (kind string, name token, ok bool) {
	// Scan ahead at depth 0 for '(' vs '=' / ';'.
	paren, angle, bracket := 0, 0, 0
	var lastIdent token
	sawIdent := false
	for j := 0; j < 128; j++ {
		t := p.peekN(j)
		if t.kind == tEOF {
			break
		}
		if paren == 0 && angle == 0 && bracket == 0 {
			if t.kind == tPunct {
				switch t.lit {
				case "(":
					if !sawIdent {
						return "", token{}, false
					}
					// Name is the last ident before '('. Consume up to that ident.
					p.consumeUntilToken(ex, lastIdent)
					if lastIdent.lit == typeName {
						return "constructor", lastIdent, true
					}
					return "method", lastIdent, true
				case "=", ";":
					if !sawIdent {
						return "", token{}, false
					}
					p.consumeUntilToken(ex, lastIdent)
					return "field", lastIdent, true
				case "{":
					// compact record constructor: Name {
					if sawIdent && lastIdent.lit == typeName {
						p.consumeUntilToken(ex, lastIdent)
						return "constructor", lastIdent, true
					}
					return "", token{}, false
				}
			}
		}
		if t.kind == tPunct {
			switch t.lit {
			case "(":
				paren++
			case ")":
				if paren > 0 {
					paren--
				}
			case "<":
				angle++
			case ">":
				if angle > 0 {
					angle--
				}
			case "[":
				bracket++
			case "]":
				if bracket > 0 {
					bracket--
				}
			}
		}
		if t.kind == tIdent && paren == 0 && angle == 0 && bracket == 0 {
			lastIdent = t
			sawIdent = true
		}
	}
	return "", token{}, false
}

func (p *parser) consumeUntilToken(ex *Extracted, want token) {
	// Consume type tokens, recording uses, until we are at `want`, then consume it.
	steps := 0
	for p.peek().kind != tEOF && steps < 128 {
		steps++
		t := p.peek()
		if t.kind == want.kind && t.lit == want.lit && t.line == want.line && t.col == want.col {
			p.next()
			return
		}
		if t.kind == tPunct && t.lit == "<" {
			p.skipGenerics(ex)
			continue
		}
		if t.kind == tIdent && ex != nil && !isSkippedUse(t.lit) {
			p.recordUse(ex, t, "")
		}
		p.next()
	}
}

func (p *parser) parseFieldRest(ex *Extracted) {
	// After the first field name has been consumed: optional = init, then , name...
	for {
		if p.acceptPunct("=") {
			p.parseExprishUntil(ex, ",", ";")
		}
		if p.acceptPunct(",") {
			if p.peek().kind == tIdent {
				n := p.next()
				encl := ""
				if len(p.typeStack) > 0 {
					encl = p.typeStack[len(p.typeStack)-1]
				}
				ex.Members = append(ex.Members, Member{
					Name:     n.lit,
					Kind:     "field",
					TypeName: encl,
					Line:     n.line,
					Col:      n.col,
					Ignored:  lineHasIgnore(p.src, n.line),
				})
			}
			continue
		}
		p.acceptPunct(";")
		return
	}
}

func (p *parser) parseTypeRef(ex *Extracted) {
	// annotations
	for p.peek().kind == tPunct && p.peek().lit == "@" {
		p.parseAnnotation(ex)
	}
	if p.peek().kind != tIdent {
		return
	}
	// primitive or FQCN
	first := true
	for p.peek().kind == tIdent {
		t := p.peek()
		if first {
			if !isKeyword(t.lit) {
				p.recordUse(ex, t, "")
			}
			first = false
		} else if !isKeyword(t.lit) {
			p.recordUse(ex, t, "")
		}
		p.next()
		if !p.acceptPunct(".") {
			break
		}
	}
	p.skipGenerics(ex)
	for p.peek().kind == tPunct && p.peek().lit == "[" {
		p.next()
		p.acceptPunct("]")
	}
	// varargs
	p.acceptPunct("...")
}

func (p *parser) skipGenerics(ex *Extracted) {
	if p.peek().kind != tPunct || p.peek().lit != "<" {
		return
	}
	depth := 0
	steps := 0
	for p.peek().kind != tEOF && steps < 256 {
		steps++
		t := p.peek()
		if t.kind == tPunct && t.lit == "<" {
			depth++
			p.next()
			continue
		}
		if t.kind == tPunct && t.lit == ">" {
			depth--
			p.next()
			if depth <= 0 {
				return
			}
			continue
		}
		if t.kind == tIdent && ex != nil && !isKeyword(t.lit) {
			p.recordUse(ex, t, "")
		}
		p.next()
	}
}

func (p *parser) skipBalanced(open, close string, ex *Extracted) {
	if p.peek().kind != tPunct || p.peek().lit != open {
		return
	}
	depth := 0
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF && steps < limit {
		steps++
		t := p.peek()
		if t.kind == tPunct && t.lit == open {
			depth++
		} else if t.kind == tPunct && t.lit == close {
			depth--
			p.next()
			if depth == 0 {
				return
			}
			continue
		} else if t.kind == tIdent && ex != nil && !isSkippedUse(t.lit) {
			p.consumeIdentUse(ex)
			continue
		} else if t.kind == tString && ex != nil && t.lit != "" {
			ex.Strings = append(ex.Strings, t.lit)
		}
		p.next()
	}
}

func (p *parser) parseExprish(ex *Extracted, stopDepth int) {
	depth := 0
	steps := 0
	limit := len(p.toks)*2 + 8
	started := false
	for p.peek().kind != tEOF && steps < limit {
		steps++
		t := p.peek()
		if t.kind == tPunct {
			switch t.lit {
			case "{", "(", "[":
				depth++
				started = true
				p.next()
				continue
			case "}", ")", "]":
				if depth == stopDepth {
					return
				}
				depth--
				p.next()
				if depth <= stopDepth && started {
					return
				}
				continue
			case ";":
				if depth == stopDepth {
					p.next()
					return
				}
			}
		}
		if t.kind == tIdent && !isSkippedUse(t.lit) {
			p.consumeIdentUse(ex)
			started = true
			continue
		}
		if t.kind == tString && t.lit != "" {
			ex.Strings = append(ex.Strings, t.lit)
		}
		started = true
		p.next()
	}
}

func (p *parser) parseExprishUntil(ex *Extracted, stops ...string) {
	depth := 0
	steps := 0
	limit := len(p.toks)*2 + 8
	stopAt := map[string]bool{}
	for _, s := range stops {
		stopAt[s] = true
	}
	for p.peek().kind != tEOF && steps < limit {
		steps++
		t := p.peek()
		if t.kind == tPunct {
			if depth == 0 && stopAt[t.lit] {
				return
			}
			switch t.lit {
			case "{", "(", "[":
				depth++
			case "}", ")", "]":
				if depth == 0 {
					return
				}
				depth--
			}
		}
		if t.kind == tIdent && !isSkippedUse(t.lit) {
			p.consumeIdentUse(ex)
			continue
		}
		if t.kind == tString && t.lit != "" {
			ex.Strings = append(ex.Strings, t.lit)
		}
		p.next()
	}
}

func (p *parser) consumeIdentUse(ex *Extracted) {
	base := p.next()
	var chain []string
	// TypeName.member or com.example.Foo.bar or this.field or Type::method
	for {
		if p.peek().kind == tPunct && (p.peek().lit == "." || p.peek().lit == "::") {
			p.next()
			if p.peek().kind == tIdent {
				chain = append(chain, p.peek().lit)
				p.next()
				continue
			}
			// .class
			if p.peek().kind == tIdent && p.peek().lit == "class" {
				p.next()
			}
			break
		}
		break
	}
	member := ""
	if len(chain) > 0 {
		member = chain[0]
	}
	ex.Uses = append(ex.Uses, Use{
		Name:   base.lit,
		Member: member,
		Chain:  chain,
		Line:   base.line,
		Col:    base.col,
	})
	// Method reference Foo::bar already in chain.
}

func (p *parser) recordUse(ex *Extracted, t token, member string) {
	if ex == nil || t.kind != tIdent || t.lit == "" {
		return
	}
	ex.Uses = append(ex.Uses, Use{Name: t.lit, Member: member, Line: t.line, Col: t.col})
}

func isSkippedUse(name string) bool {
	if isKeyword(name) && name != "this" && name != "super" {
		return true
	}
	return isModifier(name)
}
