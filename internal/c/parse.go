package c

import (
	"strings"
)

// Extracted is the translation-unit fact base for one C file.
type Extracted struct {
	Includes []Include
	Macros   []Macro
	Decls    []Decl
	Uses     []Use
	Strings  []string
	DynSyms  []string // literal names passed to dlsym / GetProcAddress
	DynLibs  []string // literal paths passed to dlopen / LoadLibrary
	// HasIndirectDlsym is true when dlsym/GetProcAddress is called with a non-literal.
	HasIndirectDlsym bool
	HasMain          bool
	IgnoreAll        bool
	// HeaderGuard is the include-guard macro, if any.
	HeaderGuard string
	// IsHeader is set by the caller from the file extension.
	IsHeader bool
}

// Include is a #include directive.
type Include struct {
	Path      string
	System    bool // <...> vs "..."
	Line, Col int
	// resolved is the absolute path of a project-local header, if any.
	resolved string
}

// Macro is a #define.
type Macro struct {
	Name      string
	FuncLike  bool
	Line, Col int
	Ignored   bool
	Guard     bool
}

// Decl is a file-scope function or object definition.
type Decl struct {
	Name      string
	Kind      string // function, var, type
	Static    bool
	Extern    bool
	Exported  bool // dllexport / default visibility
	Keep      bool // constructor / destructor / used / DllMain
	Line, Col int
	Ignored   bool
}

// Use is an identifier reference (not the defining token).
type Use struct {
	Name      string
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

func (p *parser) skipNewlines() {
	for p.peek().kind == tNewline {
		p.next()
	}
}

func (p *parser) parse(ex *Extracted) {
	steps := 0
	limit := len(p.toks)*3 + 16
	p.detectHeaderGuard(ex)
	for p.peek().kind != tEOF {
		steps++
		if steps > limit {
			return
		}
		p.skipNewlines()
		if p.peek().kind == tEOF {
			return
		}
		start := p.i
		if p.peek().kind == tPunct && p.peek().lit == "#" {
			p.parseDirective(ex)
			p.finish(start)
			continue
		}
		p.parseFileScope(ex)
		p.finish(start)
	}
}

func (p *parser) finish(start int) {
	if p.i <= start && p.peek().kind != tEOF {
		p.next()
	}
}

func (p *parser) detectHeaderGuard(ex *Extracted) {
	// Skip leading newlines.
	i := p.i
	for i < len(p.toks) && p.toks[i].kind == tNewline {
		i++
	}
	if i >= len(p.toks) || p.toks[i].kind != tPunct || p.toks[i].lit != "#" {
		return
	}
	i++
	if i >= len(p.toks) || p.toks[i].kind != tIdent {
		return
	}
	dir := p.toks[i].lit
	i++
	var name string
	switch dir {
	case "ifndef":
		if i < len(p.toks) && p.toks[i].kind == tIdent {
			name = p.toks[i].lit
		}
	case "if":
		// #if !defined(NAME) or #if ! defined NAME
		if i < len(p.toks) && p.toks[i].kind == tPunct && p.toks[i].lit == "!" {
			i++
		} else {
			return
		}
		if i < len(p.toks) && p.toks[i].kind == tIdent && p.toks[i].lit == "defined" {
			i++
			if i < len(p.toks) && p.toks[i].kind == tPunct && p.toks[i].lit == "(" {
				i++
			}
			if i < len(p.toks) && p.toks[i].kind == tIdent {
				name = p.toks[i].lit
			}
		}
	default:
		return
	}
	if name == "" {
		return
	}
	// Next non-empty directive should be #define NAME.
	for i < len(p.toks) && p.toks[i].kind != tNewline {
		i++
	}
	for i < len(p.toks) && p.toks[i].kind == tNewline {
		i++
	}
	if i >= len(p.toks) || p.toks[i].kind != tPunct || p.toks[i].lit != "#" {
		return
	}
	i++
	if i >= len(p.toks) || p.toks[i].kind != tIdent || p.toks[i].lit != "define" {
		return
	}
	i++
	if i < len(p.toks) && p.toks[i].kind == tIdent && p.toks[i].lit == name {
		ex.HeaderGuard = name
	}
}

func (p *parser) parseDirective(ex *Extracted) {
	hash := p.next() // #
	if p.peek().kind != tIdent {
		p.skipToNewline(ex, false)
		return
	}
	dir := p.next()
	switch dir.lit {
	case "include":
		p.parseInclude(ex, hash)
	case "define":
		p.parseDefine(ex, hash)
	case "undef", "ifdef", "ifndef":
		if p.peek().kind == tIdent {
			p.recordUse(ex, p.peek())
			p.next()
		}
		p.skipToNewline(ex, true)
	case "if", "elif":
		p.parseIfCond(ex)
	case "else", "endif", "pragma", "error", "warning", "line":
		p.skipToNewline(ex, true)
	default:
		p.skipToNewline(ex, true)
	}
	// #if 0 ... #endif is dropped so dead-disabled code is not reported.
	if dir.lit == "if" && p.justClosedIfZero(hash) {
		p.skipDisabledBlock()
	}
}

func (p *parser) justClosedIfZero(hash token) bool {
	// Look at tokens between # and the newline we just consumed / are at.
	// Simpler: inspect source line.
	line := lineText(p.src, hash.line)
	trim := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
	trim = strings.TrimSpace(strings.TrimPrefix(trim, "if"))
	trim = strings.TrimSpace(trim)
	switch trim {
	case "0", "0L", "0UL", "0u", "0x0", "false":
		return true
	}
	return false
}

func (p *parser) skipDisabledBlock() {
	depth := 1
	steps := 0
	limit := len(p.toks) + 8
	for p.peek().kind != tEOF && depth > 0 {
		steps++
		if steps > limit {
			return
		}
		if p.peek().kind == tPunct && p.peek().lit == "#" && p.peekN(1).kind == tIdent {
			name := p.peekN(1).lit
			p.next()
			p.next()
			switch name {
			case "if", "ifdef", "ifndef":
				depth++
			case "endif":
				depth--
			}
			p.skipToNewline(nil, false)
			continue
		}
		p.next()
	}
}

func (p *parser) parseInclude(ex *Extracted, hash token) {
	inc := Include{Line: hash.line, Col: hash.col}
	if p.peek().kind == tString {
		inc.Path = p.next().lit
		ex.Includes = append(ex.Includes, inc)
		p.skipToNewline(ex, false)
		return
	}
	if p.peek().kind == tPunct && p.peek().lit == "<" {
		p.next()
		var parts []string
		for p.peek().kind != tEOF && p.peek().kind != tNewline && !(p.peek().kind == tPunct && p.peek().lit == ">") {
			parts = append(parts, p.peek().lit)
			p.next()
		}
		p.acceptPunct(">")
		inc.Path = strings.Join(parts, "")
		inc.System = true
		ex.Includes = append(ex.Includes, inc)
		p.skipToNewline(ex, false)
		return
	}
	// Computed include: keep the identifier as a use.
	p.skipToNewline(ex, true)
}

func (p *parser) parseDefine(ex *Extracted, hash token) {
	if p.peek().kind != tIdent {
		p.skipToNewline(ex, true)
		return
	}
	name := p.next()
	fn := p.peek().kind == tPunct && p.peek().lit == "(" && !p.peek().spaced
	m := Macro{
		Name:     name.lit,
		FuncLike: fn,
		Line:     name.line,
		Col:      name.col,
		Ignored:  lineHasIgnore(p.src, name.line) || lineHasIgnore(p.src, hash.line),
		Guard:    ex.HeaderGuard != "" && name.lit == ex.HeaderGuard,
	}
	ex.Macros = append(ex.Macros, m)
	p.skipToNewline(ex, true)
}

func (p *parser) parseIfCond(ex *Extracted) {
	p.skipToNewline(ex, true)
}

func (p *parser) skipToNewline(ex *Extracted, recordUses bool) {
	steps := 0
	for p.peek().kind != tEOF && p.peek().kind != tNewline {
		steps++
		if steps > 10000 {
			return
		}
		t := p.peek()
		if recordUses && ex != nil {
			if t.kind == tIdent && t.lit != "defined" {
				p.recordUse(ex, t)
			} else if t.kind == tString {
				ex.Strings = append(ex.Strings, t.lit)
			}
		}
		p.next()
	}
	if p.peek().kind == tNewline {
		p.next()
	}
}

func (p *parser) parseFileScope(ex *Extracted) {
	// extern "C" { ... } — keep parsing at file scope inside the brace.
	if p.peek().kind == tIdent && p.peek().lit == "extern" &&
		p.peekN(1).kind == tString {
		p.next()
		p.next()
		if p.peek().kind == tPunct && p.peek().lit == "{" {
			p.next()
		}
		return
	}
	if p.peek().kind == tPunct && p.peek().lit == "}" {
		p.next()
		return
	}

	sp := p.parseSpecifiers(ex)
	if p.peek().kind == tPunct && (p.peek().lit == ";" || p.peek().lit == "{") {
		// struct/enum tag only, or stray brace.
		if p.peek().lit == "{" {
			p.skipBalanced("{", "}", ex)
		} else {
			p.next()
		}
		return
	}

	// One or more declarators.
	for {
		d, ok := p.parseDeclarator(ex)
		if !ok || d.name == "" {
			p.skipDeclTail(ex)
			return
		}
		p.skipNewlines()
		if d.function && p.peek().kind == tPunct && p.peek().lit == ";" {
			// Prototype: keep the name so #include use-checks can see it,
			// but do not report the declaration as unused code.
			ex.Decls = append(ex.Decls, Decl{
				Name:    d.name,
				Kind:    "function",
				Static:  sp.static,
				Extern:  true,
				Line:    d.line,
				Col:     d.col,
				Ignored: true,
			})
			p.next()
			return
		}
		if d.function && p.peek().kind == tPunct && p.peek().lit == "{" {
			kind := "function"
			decl := Decl{
				Name:     d.name,
				Kind:     kind,
				Static:   sp.static,
				Extern:   sp.extern,
				Exported: sp.exported && !sp.static,
				Keep:     sp.keep || isEntryFunc(d.name),
				Line:     d.line,
				Col:      d.col,
				Ignored:  sp.ignored || lineHasIgnore(p.src, d.line),
			}
			if isEntryFunc(d.name) {
				ex.HasMain = true
				decl.Keep = true
			}
			ex.Decls = append(ex.Decls, decl)
			p.skipBalanced("{", "}", ex)
			return
		}
		// K&R parameter declarations then body.
		if d.function && p.peek().kind == tIdent {
			for p.peek().kind != tEOF && !(p.peek().kind == tPunct && (p.peek().lit == "{" || p.peek().lit == ";")) {
				if p.peek().kind == tIdent {
					p.recordUse(ex, p.peek())
				}
				p.next()
			}
			if p.peek().kind == tPunct && p.peek().lit == "{" {
				ex.Decls = append(ex.Decls, Decl{
					Name:     d.name,
					Kind:     "function",
					Static:   sp.static,
					Extern:   sp.extern,
					Exported: sp.exported && !sp.static,
					Keep:     sp.keep || isEntryFunc(d.name),
					Line:     d.line,
					Col:      d.col,
					Ignored:  sp.ignored || lineHasIgnore(p.src, d.line),
				})
				if isEntryFunc(d.name) {
					ex.HasMain = true
				}
				p.skipBalanced("{", "}", ex)
				return
			}
		}
		if !d.function && !sp.typedef {
			// extern without an initializer is a declaration, not a definition.
			hasInit := p.peek().kind == tPunct && p.peek().lit == "="
			if !sp.extern || hasInit {
				ex.Decls = append(ex.Decls, Decl{
					Name:     d.name,
					Kind:     "var",
					Static:   sp.static,
					Extern:   sp.extern,
					Exported: sp.exported && !sp.static,
					Keep:     sp.keep,
					Line:     d.line,
					Col:      d.col,
					Ignored:  sp.ignored || lineHasIgnore(p.src, d.line),
				})
			}
		}
		if sp.typedef && d.name != "" {
			ex.Decls = append(ex.Decls, Decl{
				Name:    d.name,
				Kind:    "type",
				Static:  true, // types are not "exports" in the dylib sense
				Line:    d.line,
				Col:     d.col,
				Ignored: true, // prefer FN: unused typedefs are noisy
			})
		}
		if p.peek().kind == tPunct && p.peek().lit == "=" {
			p.next()
			p.skipInitializer(ex)
		}
		if p.acceptPunct(",") {
			continue
		}
		p.acceptPunct(";")
		return
	}
}

type specs struct {
	static   bool
	extern   bool
	typedef  bool
	exported bool
	keep     bool
	ignored  bool
}

func (p *parser) parseSpecifiers(ex *Extracted) specs {
	var sp specs
	steps := 0
	for steps < 64 {
		steps++
		p.skipNewlines()
		t := p.peek()
		if t.kind == tIdent {
			switch t.lit {
			case "static":
				sp.static = true
				p.next()
				continue
			case "extern":
				sp.extern = true
				p.next()
				continue
			case "typedef":
				sp.typedef = true
				p.next()
				continue
			case "inline", "__inline", "__inline__", "_inline",
				"const", "volatile", "restrict", "_Atomic",
				"register", "auto", "_Noreturn", "_Thread_local",
				"__thread", "signed", "unsigned", "short", "long",
				"int", "char", "void", "float", "double", "_Bool",
				"_Complex", "_Imaginary", "bool", "size_t",
				"int8_t", "int16_t", "int32_t", "int64_t",
				"uint8_t", "uint16_t", "uint32_t", "uint64_t",
				"uintptr_t", "intptr_t", "ptrdiff_t", "ssize_t",
				"FILE", "va_list",
				"__restrict", "__restrict__", "__const", "__volatile",
				"__extension__":
				p.next()
				continue
			case "struct", "union", "enum":
				p.next()
				if p.peek().kind == tIdent {
					// Tag is both a declaration and a possible use.
					p.recordUse(ex, p.peek())
					p.next()
				}
				if p.peek().kind == tPunct && p.peek().lit == "{" {
					p.skipBalanced("{", "}", ex)
				}
				continue
			case "__attribute__", "__attribute":
				p.next()
				p.parseAttribute(ex, &sp)
				continue
			case "__declspec":
				p.next()
				p.parseDeclspec(ex, &sp)
				continue
			case "JNIEXPORT", "APIEXPORT", "DLLEXPORT", "EXPORT":
				sp.exported = true
				p.next()
				continue
			}
			// Bare identifier: could be a typedef-name. Leave it for the declarator
			// if the next token looks like the name; otherwise consume as a type.
			if p.looksLikeTypeThenName() {
				p.recordUse(ex, t)
				p.next()
				continue
			}
			return sp
		}
		if t.kind == tPunct && t.lit == "*" {
			// Pointers belong to the declarator.
			return sp
		}
		return sp
	}
	return sp
}

func (p *parser) looksLikeTypeThenName() bool {
	// typedef-name then declarator:  foo_t bar  /  foo_t *p  /  foo_t (*fp)
	// Not a type:  foo(  is a function named foo.
	n := p.peekN(1)
	if n.kind == tIdent {
		return true
	}
	if n.kind != tPunct {
		return false
	}
	switch n.lit {
	case "*", "[":
		return true
	case "(":
		n2 := p.peekN(2)
		return n2.kind == tPunct && (n2.lit == "*" || n2.lit == "(")
	}
	return false
}

type declarator struct {
	name     string
	function bool
	line     int
	col      int
}

func (p *parser) parseDeclarator(ex *Extracted) (declarator, bool) {
	p.skipNewlines()
	for p.peek().kind == tPunct && p.peek().lit == "*" {
		p.next()
		for p.peek().kind == tIdent && isQualifier(p.peek().lit) {
			p.next()
		}
	}
	var d declarator
	fromGroup := false
	if p.peek().kind == tPunct && p.peek().lit == "(" {
		// Grouped declarator (*name)(...) vs function params of an abstract type.
		if p.looksGroupedDeclarator() {
			p.next()
			inner, ok := p.parseDeclarator(ex)
			if !ok {
				p.skipBalanced("(", ")", ex)
				return d, false
			}
			p.acceptPunct(")")
			d = inner
			fromGroup = true
		} else {
			p.skipBalanced("(", ")", ex)
			d.function = true
		}
	} else if p.peek().kind == tIdent {
		if isKeyword(p.peek().lit) && p.peek().lit != "main" {
			return d, false
		}
		t := p.next()
		d.name = t.lit
		d.line, d.col = t.line, t.col
	} else {
		return d, false
	}
	for {
		p.skipNewlines()
		if p.peek().kind == tPunct && p.peek().lit == "(" {
			p.skipBalanced("(", ")", ex)
			// Postfix (params) is a function definition only when it applies
			// directly to the identifier (`int foo(int)` / `int *foo(int)`).
			// After a grouping (`int (*fp)(int)`) the name is an object.
			if !fromGroup {
				d.function = true
			}
			continue
		}
		if p.peek().kind == tPunct && p.peek().lit == "[" {
			p.skipBalanced("[", "]", ex)
			continue
		}
		break
	}
	return d, d.name != ""
}

func (p *parser) looksGroupedDeclarator() bool {
	n := p.peekN(1)
	if n.kind == tPunct && (n.lit == "*" || n.lit == "(") {
		return true
	}
	// (ident) is a grouped declarator when not a param type list.
	if n.kind == tIdent && !isTypeKeyword(n.lit) {
		n2 := p.peekN(2)
		if n2.kind == tPunct && (n2.lit == ")" || n2.lit == "[" || n2.lit == "(" || n2.lit == ",") {
			// (name) or (name[ or (name( — grouped. (name, is a param list.
			if n2.lit == "," {
				return false
			}
			return true
		}
	}
	return false
}

func (p *parser) parseAttribute(ex *Extracted, sp *specs) {
	// __attribute__((...))
	if p.peek().kind == tPunct && p.peek().lit == "(" {
		// Record names inside for alias("foo") and visibility.
		inner := p.captureBalanced("(", ")")
		low := strings.ToLower(inner)
		if strings.Contains(low, "constructor") ||
			strings.Contains(low, "destructor") ||
			strings.Contains(low, "used") ||
			strings.Contains(low, "retain") {
			sp.keep = true
		}
		if strings.Contains(low, "dllexport") ||
			strings.Contains(low, `visibility("default")`) ||
			strings.Contains(low, `visibility ( "default" )`) {
			sp.exported = true
		}
		if i := strings.Index(low, "alias"); i >= 0 {
			// alias("real") — the string is a use of that symbol.
			if s := firstStringLit(inner); s != "" {
				ex.Uses = append(ex.Uses, Use{Name: s})
			}
		}
	}
}

func (p *parser) parseDeclspec(ex *Extracted, sp *specs) {
	if p.peek().kind != tPunct || p.peek().lit != "(" {
		return
	}
	inner := p.captureBalanced("(", ")")
	low := strings.ToLower(inner)
	if strings.Contains(low, "dllexport") {
		sp.exported = true
	}
	if strings.Contains(low, "dllimport") {
		sp.extern = true
	}
	_ = ex
}

func (p *parser) skipDeclTail(ex *Extracted) {
	steps := 0
	for p.peek().kind != tEOF && p.peek().kind != tNewline {
		steps++
		if steps > 10000 {
			return
		}
		t := p.peek()
		if t.kind == tPunct {
			switch t.lit {
			case "{":
				p.skipBalanced("{", "}", ex)
				return
			case ";":
				p.next()
				return
			case "(":
				p.skipBalanced("(", ")", ex)
				continue
			case "[":
				p.skipBalanced("[", "]", ex)
				continue
			}
		}
		if t.kind == tIdent {
			p.recordUse(ex, t)
		}
		if t.kind == tString {
			ex.Strings = append(ex.Strings, t.lit)
		}
		p.next()
	}
}

func (p *parser) skipInitializer(ex *Extracted) {
	if p.peek().kind == tPunct && p.peek().lit == "{" {
		p.skipBalanced("{", "}", ex)
		return
	}
	steps := 0
	depth := 0
	for p.peek().kind != tEOF {
		steps++
		if steps > 10000 {
			return
		}
		t := p.peek()
		if t.kind == tPunct {
			switch t.lit {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					return
				}
				depth--
			case ",", ";":
				if depth == 0 {
					return
				}
			}
		}
		if t.kind == tIdent {
			p.recordUse(ex, t)
		}
		if t.kind == tString {
			ex.Strings = append(ex.Strings, t.lit)
		}
		p.next()
	}
}

func (p *parser) skipBalanced(open, close string, ex *Extracted) {
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
		if ex != nil {
			if t.kind == tIdent {
				p.recordUse(ex, t)
			} else if t.kind == tString {
				ex.Strings = append(ex.Strings, t.lit)
			}
		}
		p.next()
	}
}

func (p *parser) captureBalanced(open, close string) string {
	start := p.i
	p.skipBalanced(open, close, nil)
	var b strings.Builder
	for i := start; i < p.i && i < len(p.toks); i++ {
		if i > start {
			b.WriteByte(' ')
		}
		b.WriteString(p.toks[i].lit)
	}
	return b.String()
}

func (p *parser) recordUse(ex *Extracted, t token) {
	if t.kind != tIdent || t.lit == "" {
		return
	}
	if isKeyword(t.lit) || isQualifier(t.lit) {
		return
	}
	ex.Uses = append(ex.Uses, Use{Name: t.lit, Line: t.line, Col: t.col})
	// dlsym / GetProcAddress / dlopen argument capture.
	switch t.lit {
	case "dlsym", "GetProcAddress", "GetProcAddressA", "GetProcAddressW":
		p.recordDynCall(ex, true)
	case "dlopen", "LoadLibrary", "LoadLibraryA", "LoadLibraryW",
		"LoadLibraryEx", "LoadLibraryExA", "LoadLibraryExW":
		p.recordDynCall(ex, false)
	}
}

func (p *parser) recordDynCall(ex *Extracted, isSym bool) {
	// peek is still the callee ident; caller has not consumed it? recordUse
	// is called BEFORE next() in skipBalanced, but AFTER next() in some paths.
	// Look ahead from current position for '('.
	j := p.i
	// If current token is the callee, start after it; if we already consumed
	// it, current may be '('.
	if j < len(p.toks) && p.toks[j].kind == tIdent {
		j++
	}
	if j >= len(p.toks) || p.toks[j].kind != tPunct || p.toks[j].lit != "(" {
		return
	}
	j++ // after (
	// first arg, then comma, then second arg for dlsym.
	arg := 0
	depth := 1
	sawLiteral := false
	for j < len(p.toks) && depth > 0 {
		t := p.toks[j]
		if t.kind == tPunct {
			switch t.lit {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			case ",":
				if depth == 1 {
					arg++
				}
			}
		}
		want := 0
		if isSym {
			want = 1
		}
		if depth == 1 && arg == want && t.kind == tString {
			sawLiteral = true
			if isSym {
				if isIdentString(t.lit) {
					ex.DynSyms = append(ex.DynSyms, t.lit)
				}
			} else {
				ex.DynLibs = append(ex.DynLibs, t.lit)
			}
		}
		j++
	}
	if isSym && !sawLiteral {
		ex.HasIndirectDlsym = true
	}
}

func isEntryFunc(name string) bool {
	switch name {
	case "main", "wmain", "WinMain", "wWinMain", "DllMain", "_start":
		return true
	}
	return false
}

func isQualifier(s string) bool {
	switch s {
	case "const", "volatile", "restrict", "_Atomic",
		"__restrict", "__restrict__", "__const", "__volatile":
		return true
	}
	return false
}

func isTypeKeyword(s string) bool {
	switch s {
	case "void", "char", "short", "int", "long", "float", "double",
		"signed", "unsigned", "_Bool", "bool", "struct", "union", "enum",
		"_Complex", "_Imaginary", "size_t", "FILE":
		return true
	}
	return false
}

func isKeyword(s string) bool {
	switch s {
	case "auto", "break", "case", "char", "const", "continue", "default",
		"do", "double", "else", "enum", "extern", "float", "for", "goto",
		"if", "inline", "int", "long", "register", "restrict", "return",
		"short", "signed", "sizeof", "static", "struct", "switch", "typedef",
		"union", "unsigned", "void", "volatile", "while", "_Alignas",
		"_Alignof", "_Atomic", "_Bool", "_Complex", "_Generic", "_Imaginary",
		"_Noreturn", "_Static_assert", "_Thread_local", "bool", "true",
		"false", "alignof", "alignas", "static_assert", "thread_local",
		"typeof", "typeof_unqual", "nullptr",
		"__attribute__", "__attribute", "__declspec", "__asm__", "__asm",
		"asm", "__typeof__", "__typeof", "__inline", "__inline__",
		"__restrict", "__restrict__", "__extension__":
		return true
	}
	return false
}

func isIdentString(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 0 {
			if !isIdentStart(c) {
				return false
			}
			continue
		}
		if !isIdentContinue(c) {
			return false
		}
	}
	return true
}

func firstStringLit(s string) string {
	i := strings.Index(s, `"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i+1:], `"`)
	if j < 0 {
		return ""
	}
	return s[i+1 : i+1+j]
}

func lineText(src []byte, line int) string {
	cur := 1
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			if cur == line {
				return string(src[start:i])
			}
			cur++
			start = i + 1
		}
	}
	return ""
}
