// Package javascript detects unused files, exports, imports, and locals in JS/TS.
package javascript

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token kinds produced by the scanner.
const (
	tEOF = iota
	tIdent
	tNumber
	tString
	tTemplate
	tPunct
	tRegex
)

type token struct {
	kind int
	lit  string
	line int
	col  int
}

type scanner struct {
	src     []byte
	i       int
	line    int
	col     int
	last    int // previous non-comment token kind
	lastLit string
}

func tokenize(src []byte) []token {
	s := &scanner{src: src, line: 1, col: 1, last: tEOF}
	// Shebang
	if bytes.HasPrefix(src, []byte("#!")) {
		s.skipLine()
	}
	var out []token
	for {
		s.skipSpaceAndComments()
		if s.i >= len(s.src) {
			break
		}
		start := s.i
		line, col := s.line, s.col
		t := s.next()
		if t.kind == tEOF {
			if s.i == start {
				s.advance() // avoid infinite loop on unexpected byte
			}
			continue
		}
		t.line, t.col = line, col
		out = append(out, t)
		s.last = t.kind
		s.lastLit = t.lit
	}
	return out
}

func (s *scanner) next() token {
	if s.i >= len(s.src) {
		return token{kind: tEOF}
	}
	c := s.src[s.i]
	switch c {
	case '\'', '"':
		return s.scanString(c)
	case '`':
		return s.scanTemplate()
	case '/':
		return s.scanSlash()
	case '.':
		if s.i+1 < len(s.src) && isDigit(s.src[s.i+1]) {
			return s.scanNumber()
		}
		if s.starts("...") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "..."}
		}
		s.advance()
		return token{kind: tPunct, lit: "."}
	case '?':
		if s.starts("?.") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "?."}
		}
		if s.starts("??") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "??"}
		}
		s.advance()
		return token{kind: tPunct, lit: "?"}
	case '=':
		if s.starts("=>") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "=>"}
		}
		if s.starts("===") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "==="}
		}
		if s.starts("==") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "=="}
		}
		s.advance()
		return token{kind: tPunct, lit: "="}
	case '!':
		if s.starts("!==") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "!=="}
		}
		if s.starts("!=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "!="}
		}
		s.advance()
		return token{kind: tPunct, lit: "!"}
	case '<':
		if s.starts("<<=") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "<<="}
		}
		if s.starts("<<") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "<<"}
		}
		if s.starts("<=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "<="}
		}
		s.advance()
		return token{kind: tPunct, lit: "<"}
	case '>':
		if s.starts(">>>=") {
			s.advanceN(4)
			return token{kind: tPunct, lit: ">>>="}
		}
		if s.starts(">>>") {
			s.advanceN(3)
			return token{kind: tPunct, lit: ">>>"}
		}
		if s.starts(">>=") {
			s.advanceN(3)
			return token{kind: tPunct, lit: ">>="}
		}
		if s.starts(">>") {
			s.advanceN(2)
			return token{kind: tPunct, lit: ">>"}
		}
		if s.starts(">=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: ">="}
		}
		s.advance()
		return token{kind: tPunct, lit: ">"}
	case '&':
		if s.starts("&&=") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "&&="}
		}
		if s.starts("&&") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "&&"}
		}
		if s.starts("&=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "&="}
		}
		s.advance()
		return token{kind: tPunct, lit: "&"}
	case '|':
		if s.starts("||=") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "||="}
		}
		if s.starts("||") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "||"}
		}
		if s.starts("|=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "|="}
		}
		s.advance()
		return token{kind: tPunct, lit: "|"}
	case '+':
		if s.starts("++") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "++"}
		}
		if s.starts("+=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "+="}
		}
		s.advance()
		return token{kind: tPunct, lit: "+"}
	case '-':
		if s.starts("--") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "--"}
		}
		if s.starts("-=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "-="}
		}
		s.advance()
		return token{kind: tPunct, lit: "-"}
	case '*':
		if s.starts("**=") {
			s.advanceN(3)
			return token{kind: tPunct, lit: "**="}
		}
		if s.starts("**") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "**"}
		}
		if s.starts("*=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "*="}
		}
		s.advance()
		return token{kind: tPunct, lit: "*"}
	case '%', '^', '~', ',', ';', ':', '(', ')', '[', ']', '{', '}', '@', '#':
		if c == '%' && s.starts("%=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "%="}
		}
		if c == '^' && s.starts("^=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "^="}
		}
		s.advance()
		return token{kind: tPunct, lit: string(c)}
	}
	if isIdentStart(c) {
		return s.scanIdent()
	}
	if isDigit(c) {
		return s.scanNumber()
	}
	s.advance()
	return token{kind: tPunct, lit: string(c)}
}

func (s *scanner) scanIdent() token {
	start := s.i
	s.advance()
	for s.i < len(s.src) {
		c := s.src[s.i]
		if isIdentContinue(c) {
			s.advance()
			continue
		}
		// Unicode identifier continue
		r, size := utf8.DecodeRune(s.src[s.i:])
		if r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			s.i += size
			s.col += size
			continue
		}
		break
	}
	return token{kind: tIdent, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanNumber() token {
	start := s.i
	if s.starts("0x") || s.starts("0X") || s.starts("0b") || s.starts("0B") || s.starts("0o") || s.starts("0O") {
		s.advanceN(2)
		for s.i < len(s.src) && isIdentContinue(s.src[s.i]) {
			s.advance()
		}
		return token{kind: tNumber, lit: string(s.src[start:s.i])}
	}
	for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '_' || s.src[s.i] == '.') {
		s.advance()
	}
	if s.i < len(s.src) && (s.src[s.i] == 'e' || s.src[s.i] == 'E') {
		s.advance()
		if s.i < len(s.src) && (s.src[s.i] == '+' || s.src[s.i] == '-') {
			s.advance()
		}
		for s.i < len(s.src) && isDigit(s.src[s.i]) {
			s.advance()
		}
	}
	if s.i < len(s.src) && (s.src[s.i] == 'n') {
		s.advance()
	}
	return token{kind: tNumber, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanString(quote byte) token {
	s.advance() // opening
	var b strings.Builder
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				b.WriteByte(s.src[s.i])
				s.advance()
			}
			continue
		}
		if c == quote {
			s.advance()
			break
		}
		if c == '\n' {
			break
		}
		b.WriteByte(c)
		s.advance()
	}
	return token{kind: tString, lit: b.String()}
}

func (s *scanner) scanTemplate() token {
	s.advance() // `
	var b strings.Builder
	depth := 0
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				b.WriteByte(s.src[s.i])
				s.advance()
			}
			continue
		}
		if c == '`' && depth == 0 {
			s.advance()
			break
		}
		if c == '$' && s.i+1 < len(s.src) && s.src[s.i+1] == '{' {
			b.WriteByte(' ')
			s.advanceN(2)
			depth++
			continue
		}
		if c == '{' && depth > 0 {
			depth++
			s.advance()
			continue
		}
		if c == '}' && depth > 0 {
			depth--
			s.advance()
			continue
		}
		if c == '\'' || c == '"' {
			// skip nested strings inside ${}
			q := c
			s.advance()
			for s.i < len(s.src) && s.src[s.i] != q {
				if s.src[s.i] == '\\' {
					s.advance()
				}
				if s.i < len(s.src) {
					s.advance()
				}
			}
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		b.WriteByte(c)
		s.advance()
	}
	return token{kind: tTemplate, lit: b.String()}
}

func (s *scanner) scanSlash() token {
	// Comment already handled. Could be regex or divide.
	if s.canStartRegex() {
		return s.scanRegex()
	}
	if s.starts("/=") {
		s.advanceN(2)
		return token{kind: tPunct, lit: "/="}
	}
	s.advance()
	return token{kind: tPunct, lit: "/"}
}

func (s *scanner) canStartRegex() bool {
	// After an identifier, number, string, ), ], ++, --, }, / is division.
	switch s.last {
	case tIdent, tNumber, tString, tTemplate, tRegex:
		return false
	case tPunct:
		switch s.lastLit {
		case ")", "]", "}", "++", "--":
			return false
		}
	}
	return true
}

func (s *scanner) scanRegex() token {
	start := s.i
	s.advance() // /
	inClass := false
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '\\' {
			s.advanceN(2)
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			s.advance()
			for s.i < len(s.src) && isIdentContinue(s.src[s.i]) {
				s.advance() // flags
			}
			return token{kind: tRegex, lit: string(s.src[start:s.i])}
		} else if c == '\n' {
			break
		}
		s.advance()
	}
	// Failed regex; treat as divide.
	s.i = start
	s.advance()
	return token{kind: tPunct, lit: "/"}
}

func (s *scanner) skipSpaceAndComments() {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			s.advance()
			continue
		}
		if s.starts("//") {
			s.skipLine()
			continue
		}
		if s.starts("/*") {
			s.advanceN(2)
			for s.i < len(s.src) && !s.starts("*/") {
				s.advance()
			}
			if s.starts("*/") {
				s.advanceN(2)
			}
			continue
		}
		// HTML comment in JS (legacy)
		if s.starts("<!--") {
			s.skipLine()
			continue
		}
		return
	}
}

func (s *scanner) skipLine() {
	for s.i < len(s.src) && s.src[s.i] != '\n' {
		s.advance()
	}
}

func (s *scanner) starts(p string) bool {
	b := []byte(p)
	if s.i+len(b) > len(s.src) {
		return false
	}
	return bytes.Equal(s.src[s.i:s.i+len(b)], b)
}

func (s *scanner) advance() {
	if s.i >= len(s.src) {
		return
	}
	if s.src[s.i] == '\n' {
		s.line++
		s.col = 1
	} else {
		s.col++
	}
	s.i++
}

func (s *scanner) advanceN(n int) {
	for i := 0; i < n; i++ {
		s.advance()
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c >= 0x80
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

func isKeyword(s string) bool {
	switch s {
	case "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends",
		"false", "finally", "for", "function", "if", "import", "in",
		"instanceof", "new", "null", "return", "super", "switch", "this",
		"throw", "true", "try", "typeof", "var", "void", "while", "with",
		"yield", "let", "static", "implements", "interface", "package",
		"private", "protected", "public", "await", "async", "from", "as",
		"of", "type", "namespace", "abstract", "readonly", "satisfies",
		"infer", "keyof", "unique", "declare", "module", "asserts":
		return true
	}
	return false
}
