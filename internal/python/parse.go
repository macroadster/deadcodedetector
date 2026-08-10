package python

import (
	"strings"
)

// Extracted is the module-level fact base for one Python file.
type Extracted struct {
	Imports []Import
	Decls   []Decl
	Uses    []Use
	// AttrUses maps base name -> set of attribute names accessed (base.attr).
	AttrUses map[string]map[string]bool
	Strings  []string
	// All lists names from __all__ = [...].
	All []string
	// HasMain is true when the file has if __name__ == "__main__".
	HasMain bool
	// IgnoreAll is set by a file-level dcd:ignore-file comment.
	IgnoreAll bool
}

// Import is an import or from-import binding.
type Import struct {
	// Module is the imported module path (e.g. "os.path", ".config", "scripts.utils").
	Module string
	// Name is the original name for from-import (empty for import x).
	Name string
	// Local is the local binding name.
	Local string
	// Level is the number of leading dots for relative imports (0 = absolute).
	Level int
	// Star is true for "from x import *".
	Star bool
	// From is true for from-import.
	From bool
	Line, Col int
	// resolved is the absolute path of Module, if local.
	resolved string
}

// Decl is a top-level binding (function, class, or assignment).
type Decl struct {
	Name      string
	Line, Col int
	Kind      string // function, class, var
	Ignored   bool
}

// Use is a name reference.
type Use struct {
	Name      string
	Line, Col int
	// Member is set for base.member attribute access (Name is the base).
	Member string
}

func extract(src []byte) Extracted {
	ex := Extracted{AttrUses: map[string]map[string]bool{}}
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

func (p *parser) skipNewlines() {
	for p.peek().kind == tNewline {
		p.i++
	}
}

func (p *parser) parse(ex *Extracted) {
	steps := 0
	limit := len(p.toks)*3 + 16
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		start := p.i
		p.skipNewlines()
		if p.peek().kind == tEOF {
			break
		}
		t := p.peek()
		// Only treat indent 0 (or unknown -1 from continued lines) as top-level
		// for declarations. Uses are collected everywhere.
		topLevel := t.indent == 0

		if t.kind == tIdent {
			switch t.lit {
			case "import":
				p.parseImport(ex)
				p.finish(start)
				continue
			case "from":
				p.parseFromImport(ex)
				p.finish(start)
				continue
			case "def":
				if topLevel {
					p.parseDef(ex, false)
					p.finish(start)
					continue
				}
			case "async":
				if topLevel && p.peekN(1).kind == tIdent && p.peekN(1).lit == "def" {
					p.next() // async
					p.parseDef(ex, false)
					p.finish(start)
					continue
				}
			case "class":
				if topLevel {
					p.parseClass(ex)
					p.finish(start)
					continue
				}
			case "if":
				if topLevel {
					p.parseIfMain(ex)
					// fall through to also collect uses in the block via expr walk
				}
			}
		}
		// Decorators at top level: @foo then def/class
		if topLevel && t.kind == tPunct && t.lit == "@" {
			p.parseDecoratorBlock(ex)
			p.finish(start)
			continue
		}
		// Top-level assignment: Name = ... or Name: type = ...
		if topLevel && t.kind == tIdent && !isKeyword(t.lit) {
			if p.looksLikeAssignment() {
				p.parseAssignment(ex)
				p.finish(start)
				continue
			}
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

func (p *parser) looksLikeAssignment() bool {
	// NAME = ... or NAME: ... or NAME, NAME = ...
	// Not NAME( ...
	j := 0
	for {
		t := p.peekN(j)
		if t.kind == tIdent && !isKeyword(t.lit) {
			j++
			if p.peekN(j).kind == tPunct && p.peekN(j).lit == "," {
				j++
				continue
			}
			break
		}
		return false
	}
	t := p.peekN(j)
	if t.kind == tPunct && (t.lit == "=" || t.lit == ":=" || t.lit == ":") {
		return true
	}
	return false
}

func (p *parser) parseDecoratorBlock(ex *Extracted) {
	// Consume one or more @decorator lines, recording uses, then def/class.
	for p.peek().kind == tPunct && p.peek().lit == "@" {
		p.next()
		p.parseExprish(ex, 0)
		p.skipNewlines()
	}
	if p.acceptIdent("async") {
		// async def
	}
	if p.peek().kind == tIdent && p.peek().lit == "def" {
		p.parseDef(ex, false)
		return
	}
	if p.peek().kind == tIdent && p.peek().lit == "class" {
		p.parseClass(ex)
		return
	}
}

func (p *parser) parseDef(ex *Extracted, _ bool) {
	p.next() // def
	name := p.peek()
	if name.kind != tIdent {
		return
	}
	p.next()
	ignored := lineHasIgnore(p.src, name.line)
	// Previous line decorator ignore: check a few lines above.
	if !ignored {
		ignored = lineHasIgnore(p.src, name.line-1)
	}
	ex.Decls = append(ex.Decls, Decl{
		Name:    name.lit,
		Line:    name.line,
		Col:     name.col,
		Kind:    "function",
		Ignored: ignored,
	})
	// Parse rest of signature and body as expressions for uses.
	// Stop at next top-level-ish by consuming until we've walked nested blocks
	// via paren depth + indent heuristic in parseExprish loops at caller.
	// Here consume the def line and let outer loop handle body as exprish
	// (body is indented so won't re-parse as top-level def of interest for Decl,
	// but uses will be collected).
	p.parseExprish(ex, 0) // rest of def line (args, return annotation)
}

func (p *parser) parseClass(ex *Extracted) {
	p.next() // class
	name := p.peek()
	if name.kind != tIdent {
		return
	}
	p.next()
	ignored := lineHasIgnore(p.src, name.line) || lineHasIgnore(p.src, name.line-1)
	ex.Decls = append(ex.Decls, Decl{
		Name:    name.lit,
		Line:    name.line,
		Col:     name.col,
		Kind:    "class",
		Ignored: ignored,
	})
	p.parseExprish(ex, 0) // bases, rest of line
}

func (p *parser) parseAssignment(ex *Extracted) {
	// Collect targets until = or :
	var targets []token
	for {
		t := p.peek()
		if t.kind == tIdent && !isKeyword(t.lit) {
			targets = append(targets, t)
			p.next()
			if p.acceptPunct(",") {
				continue
			}
			break
		}
		break
	}
	ann := false
	if p.acceptPunct(":") {
		ann = true
		// skip annotation expression until = or newline
		for p.peek().kind != tEOF && p.peek().kind != tNewline {
			if p.peek().kind == tPunct && p.peek().lit == "=" {
				break
			}
			// still collect uses in annotation
			if p.peek().kind == tIdent && !isKeyword(p.peek().lit) {
				// could be type use
				p.recordUse(ex, p.peek(), "")
			}
			// handle attribute in annotation
			if p.peek().kind == tIdent {
				base := p.next()
				if p.peek().kind == tPunct && p.peek().lit == "." {
					// base.attr
					for p.peek().kind == tPunct && p.peek().lit == "." {
						p.next()
						if p.peek().kind == tIdent {
							mem := p.next()
							p.recordUse(ex, base, mem.lit)
							base = mem
						}
					}
					continue
				}
				p.recordUse(ex, base, "")
				continue
			}
			p.next()
		}
	}
	if p.acceptPunct("=") || p.acceptPunct(":=") {
		for _, t := range targets {
			if t.lit == "__all__" {
				// parse list of strings for __all__
				p.parseAllList(ex)
				continue
			}
			// Only record module-level vars (not re-assignments of imports etc.)
			// First assignment wins as Decl; subsequent are uses... actually
			// assignment target is a definition. For re-assignment of existing
			// name, still fine — use-count of other refs matters.
			ignored := lineHasIgnore(p.src, t.line)
			ex.Decls = append(ex.Decls, Decl{
				Name:    t.lit,
				Line:    t.line,
				Col:     t.col,
				Kind:    "var",
				Ignored: ignored,
			})
		}
		// RHS uses
		p.parseExprish(ex, 0)
		return
	}
	if ann {
		// annotated assignment without value: name: Type
		for _, t := range targets {
			ex.Decls = append(ex.Decls, Decl{
				Name: t.lit, Line: t.line, Col: t.col, Kind: "var",
				Ignored: lineHasIgnore(p.src, t.line),
			})
		}
	}
	_ = ann
}

func (p *parser) parseAllList(ex *Extracted) {
	// Expect [ "a", "b" ] or ( "a", "b" )
	p.skipNewlines()
	open := ""
	if p.acceptPunct("[") {
		open = "]"
	} else if p.acceptPunct("(") {
		open = ")"
	} else {
		return
	}
	for p.peek().kind != tEOF {
		p.skipNewlines()
		if p.peek().kind == tPunct && p.peek().lit == open {
			p.next()
			break
		}
		if p.peek().kind == tString {
			ex.All = append(ex.All, p.peek().lit)
			p.next()
			p.acceptPunct(",")
			continue
		}
		if p.peek().kind == tPunct && (p.peek().lit == "," || p.peek().lit == open) {
			if p.peek().lit == open {
				p.next()
				break
			}
			p.next()
			continue
		}
		// unexpected; abort
		break
	}
}

func (p *parser) parseIfMain(ex *Extracted) {
	// if __name__ == "__main__":
	// peek pattern without always consuming as special
	if p.peek().lit != "if" {
		return
	}
	// Lookahead
	if p.peekN(1).kind == tIdent && p.peekN(1).lit == "__name__" {
		// check for == "__main__" or == '__main__'
		for k := 2; k < 8 && p.i+k < len(p.toks); k++ {
			t := p.peekN(k)
			if t.kind == tString && (t.lit == "__main__") {
				ex.HasMain = true
				break
			}
			if t.kind == tNewline {
				break
			}
		}
	}
}

func (p *parser) parseImport(ex *Extracted) {
	// import a, b as c, d.e as f
	line, col := p.peek().line, p.peek().col
	p.next() // import
	for {
		p.skipNewlines()
		mod, ok := p.parseDottedName()
		if !ok {
			break
		}
		local := strings.Split(mod, ".")[0]
		if p.acceptIdent("as") {
			if p.peek().kind == tIdent {
				local = p.peek().lit
				p.next()
			}
		}
		ex.Imports = append(ex.Imports, Import{
			Module: mod,
			Local:  local,
			Line:   line,
			Col:    col,
			From:   false,
		})
		if !p.acceptPunct(",") {
			break
		}
		line, col = p.peek().line, p.peek().col
	}
}

func (p *parser) parseFromImport(ex *Extracted) {
	// from ...mod import a, b as c
	// from . import x
	line, col := p.peek().line, p.peek().col
	p.next() // from
	level := 0
	for p.acceptPunct(".") {
		level++
	}
	// also handle `from ..` where dots may be a single "..." token? we use single dots
	mod := ""
	if p.peek().kind == tIdent {
		if name, ok := p.parseDottedName(); ok {
			mod = name
		}
	}
	if !p.acceptIdent("import") {
		return
	}
	p.skipNewlines()
	// parenthesized import list
	paren := p.acceptPunct("(")
	if paren {
		p.skipNewlines()
	}
	for {
		p.skipNewlines()
		if paren && p.acceptPunct(")") {
			break
		}
		if p.acceptPunct("*") {
			ex.Imports = append(ex.Imports, Import{
				Module: mod, Level: level, Star: true, From: true,
				Line: line, Col: col,
			})
			break
		}
		if p.peek().kind != tIdent {
			break
		}
		name := p.peek().lit
		p.next()
		local := name
		if p.acceptIdent("as") {
			if p.peek().kind == tIdent {
				local = p.peek().lit
				p.next()
			}
		}
		ex.Imports = append(ex.Imports, Import{
			Module: mod, Name: name, Local: local, Level: level, From: true,
			Line: line, Col: col,
		})
		p.skipNewlines()
		if p.acceptPunct(",") {
			continue
		}
		if paren {
			p.skipNewlines()
			p.acceptPunct(")")
		}
		break
	}
}

func (p *parser) parseDottedName() (string, bool) {
	if p.peek().kind != tIdent {
		return "", false
	}
	parts := []string{p.peek().lit}
	p.next()
	for p.acceptPunct(".") {
		if p.peek().kind != tIdent {
			break
		}
		parts = append(parts, p.peek().lit)
		p.next()
	}
	return strings.Join(parts, "."), true
}

// parseExprish walks tokens collecting name uses until a stopping point.
// depth tracks (), [], {} nesting. Stops at newline when depth==0 if stopOnNL.
func (p *parser) parseExprish(ex *Extracted, depth int) {
	steps := 0
	limit := len(p.toks)*2 + 8
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		t := p.peek()
		if t.kind == tNewline {
			if depth == 0 {
				p.next()
				return
			}
			p.next()
			continue
		}
		if t.kind == tPunct {
			switch t.lit {
			case "(", "[", "{":
				p.next()
				depth++
				continue
			case ")", "]", "}":
				p.next()
				if depth > 0 {
					depth--
				}
				if depth == 0 {
					// keep going on same logical stmt? for call chains yes
				}
				continue
			case "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", ":=",
				"//=", "**=", "<<=", ">>=":
				// assignment inside expr (for loop etc.) — RHS after is use-y;
				// targets before already consumed. Just skip op.
				p.next()
				continue
			}
		}
		if t.kind == tIdent {
			// Nested import/from — still record as imports for cross-module.
			if t.lit == "import" && depth == 0 {
				// only at statement-ish positions; may false-positive on names
				// but "import" is keyword so fine
				p.parseImport(ex)
				continue
			}
			if t.lit == "from" && depth == 0 && p.looksLikeFromImport() {
				p.parseFromImport(ex)
				continue
			}
			if isKeyword(t.lit) {
				// def/class inside nested scope — skip name binding as use
				if t.lit == "def" || t.lit == "class" {
					p.next()
					if p.peek().kind == tIdent {
						// nested def name is not a module-level use of itself
						p.next()
					}
					continue
				}
				p.next()
				continue
			}
			base := p.next()
			// Attribute chain: base.attr.attr2
			if p.peek().kind == tPunct && p.peek().lit == "." {
				first := true
				for p.peek().kind == tPunct && p.peek().lit == "." {
					p.next()
					if p.peek().kind != tIdent {
						break
					}
					mem := p.next()
					if first {
						p.recordUse(ex, base, mem.lit)
						first = false
					} else {
						// deeper attrs: still record attr name as free word? skip base
						// Record as attr on original base only for first hop.
					}
					// Also record the member as a free use so method-style
					// references via self.foo still count for nested... no,
					// self.foo shouldn't mark module-level foo. Skip.
					_ = mem
				}
				continue
			}
			p.recordUse(ex, base, "")
			continue
		}
		if t.kind == tString {
			p.recordString(ex, t)
			p.next()
			continue
		}
		p.next()
	}
}

// recordString stores string content and, for f-strings, identifiers used
// inside {...} interpolations (encoded by the scanner after a NUL byte).
func (p *parser) recordString(ex *Extracted, t token) {
	lit := t.lit
	if i := strings.IndexByte(lit, 0); i >= 0 {
		plain := lit[:i]
		if plain != "" {
			ex.Strings = append(ex.Strings, plain)
		}
		for _, expr := range strings.Split(lit[i+1:], "\x00") {
			if expr == "" {
				continue
			}
			p.recordFStringExpr(ex, expr, t.line, t.col)
		}
		return
	}
	if lit != "" {
		ex.Strings = append(ex.Strings, lit)
	}
}

func (p *parser) recordFStringExpr(ex *Extracted, expr string, line, col int) {
	// Tokenize the expression for identifiers and attribute chains.
	// Lightweight scan: pull idents, skip keywords/numbers.
	b := []byte(expr)
	i := 0
	for i < len(b) {
		c := b[i]
		if isIdentStart(c) {
			j := i + 1
			for j < len(b) && isIdentContinue(b[j]) {
				j++
			}
			name := string(b[i:j])
			i = j
			if isKeyword(name) {
				continue
			}
			// attribute chain name.attr
			member := ""
			for i < len(b) && b[i] == '.' {
				i++
				k := i
				for k < len(b) && isIdentContinue(b[k]) {
					k++
				}
				if k == i {
					break
				}
				if member == "" {
					member = string(b[i:k])
				}
				i = k
			}
			ex.Uses = append(ex.Uses, Use{Name: name, Line: line, Col: col, Member: member})
			if member != "" {
				if ex.AttrUses[name] == nil {
					ex.AttrUses[name] = map[string]bool{}
				}
				ex.AttrUses[name][member] = true
			}
			continue
		}
		i++
	}
}

func (p *parser) looksLikeFromImport() bool {
	// from X import | from . import | from ..X import
	if p.peek().lit != "from" {
		return false
	}
	n := p.peekN(1)
	if n.kind == tPunct && n.lit == "." {
		return true
	}
	if n.kind == tIdent {
		// from something import
		for k := 2; k < 12 && p.i+k < len(p.toks); k++ {
			t := p.peekN(k)
			if t.kind == tIdent && t.lit == "import" {
				return true
			}
			if t.kind == tNewline {
				return false
			}
		}
	}
	return false
}

func (p *parser) recordUse(ex *Extracted, base token, member string) {
	if base.kind != tIdent || isKeyword(base.lit) {
		return
	}
	ex.Uses = append(ex.Uses, Use{Name: base.lit, Line: base.line, Col: base.col, Member: member})
	if member != "" {
		if ex.AttrUses[base.lit] == nil {
			ex.AttrUses[base.lit] = map[string]bool{}
		}
		ex.AttrUses[base.lit][member] = true
	}
}
