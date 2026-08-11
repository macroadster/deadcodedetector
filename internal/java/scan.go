// Package java detects unused files, imports, types, and members in Java.
package java

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// Token kinds.
const (
	tEOF = iota
	tIdent
	tNumber
	tString
	tChar
	tPunct
)

type token struct {
	kind int
	lit  string
	line int
	col  int
}

type scanner struct {
	src  []byte
	i    int
	line int
	col  int
}

func tokenize(src []byte) []token {
	s := &scanner{src: src, line: 1, col: 1}
	var out []token
	steps := 0
	limit := len(src)*3 + 16
	for {
		steps++
		if steps > limit {
			break
		}
		s.skipSpaceAndComments()
		if s.i >= len(s.src) {
			break
		}
		line, col := s.line, s.col
		t := s.next()
		if t.kind == tEOF {
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		t.line, t.col = line, col
		out = append(out, t)
	}
	return out
}

func (s *scanner) skipSpaceAndComments() {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ' ' || c == '\t' || c == '\f' || c == '\r' {
			s.advance()
			continue
		}
		if c == '\n' {
			s.advance()
			continue
		}
		if c == '/' && s.i+1 < len(s.src) {
			switch s.src[s.i+1] {
			case '/':
				s.advance()
				s.advance()
				for s.i < len(s.src) && s.src[s.i] != '\n' {
					s.advance()
				}
				continue
			case '*':
				s.advance()
				s.advance()
				for s.i+1 < len(s.src) && !(s.src[s.i] == '*' && s.src[s.i+1] == '/') {
					s.advance()
				}
				if s.i+1 < len(s.src) {
					s.advance()
					s.advance()
				}
				continue
			}
		}
		break
	}
}

func (s *scanner) next() token {
	if s.i >= len(s.src) {
		return token{kind: tEOF}
	}
	// non-sealed is a single keyword.
	if s.starts("non-sealed") {
		s.advanceN(len("non-sealed"))
		return token{kind: tIdent, lit: "non-sealed"}
	}
	c := s.src[s.i]
	switch c {
	case '"':
		if s.starts(`"""`) {
			return s.scanTextBlock()
		}
		return s.scanString()
	case '\'':
		return s.scanChar()
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
	case ':':
		if s.starts("::") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "::"}
		}
		s.advance()
		return token{kind: tPunct, lit: ":"}
	case '-':
		if s.starts("->") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "->"}
		}
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
	case '=':
		if s.starts("==") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "=="}
		}
		s.advance()
		return token{kind: tPunct, lit: "="}
	case '!':
		if s.starts("!=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "!="}
		}
		s.advance()
		return token{kind: tPunct, lit: "!"}
	case '&':
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
	case '<':
		// Emit '<' alone so List<List<T>> is easy to walk. << becomes two tokens.
		if s.starts("<=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "<="}
		}
		s.advance()
		return token{kind: tPunct, lit: "<"}
	case '>':
		if s.starts(">=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: ">="}
		}
		s.advance()
		return token{kind: tPunct, lit: ">"}
	case '*':
		if s.starts("*=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "*="}
		}
		s.advance()
		return token{kind: tPunct, lit: "*"}
	case '%':
		if s.starts("%=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "%="}
		}
		s.advance()
		return token{kind: tPunct, lit: "%"}
	case '^':
		if s.starts("^=") {
			s.advanceN(2)
			return token{kind: tPunct, lit: "^="}
		}
		s.advance()
		return token{kind: tPunct, lit: "^"}
	case '/', '~', ',', ';', '?', '(', ')', '[', ']', '{', '}', '@', '#':
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
	for s.i < len(s.src) && isIdentContinue(s.src[s.i]) {
		s.advance()
	}
	return token{kind: tIdent, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanNumber() token {
	start := s.i
	if s.starts("0x") || s.starts("0X") {
		s.advanceN(2)
		for s.i < len(s.src) && isHex(s.src[s.i]) {
			s.advance()
		}
	} else if s.starts("0b") || s.starts("0B") {
		s.advanceN(2)
		for s.i < len(s.src) && (s.src[s.i] == '0' || s.src[s.i] == '1' || s.src[s.i] == '_') {
			s.advance()
		}
	} else {
		for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '_') {
			s.advance()
		}
		if s.i < len(s.src) && s.src[s.i] == '.' && s.i+1 < len(s.src) && isDigit(s.src[s.i+1]) {
			s.advance()
			for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '_') {
				s.advance()
			}
		}
		if s.i < len(s.src) && (s.src[s.i] == 'e' || s.src[s.i] == 'E') {
			s.advance()
			if s.i < len(s.src) && (s.src[s.i] == '+' || s.src[s.i] == '-') {
				s.advance()
			}
			for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '_') {
				s.advance()
			}
		}
	}
	if s.i < len(s.src) {
		c := s.src[s.i] | 0x20
		if c == 'l' || c == 'f' || c == 'd' {
			s.advance()
		}
	}
	return token{kind: tNumber, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanString() token {
	s.advance() // opening "
	start := s.i
	for s.i < len(s.src) && s.src[s.i] != '"' && s.src[s.i] != '\n' {
		if s.src[s.i] == '\\' && s.i+1 < len(s.src) {
			s.advance()
		}
		s.advance()
	}
	lit := string(s.src[start:s.i])
	if s.i < len(s.src) && s.src[s.i] == '"' {
		s.advance()
	}
	return token{kind: tString, lit: lit}
}

func (s *scanner) scanTextBlock() token {
	s.advanceN(3) // """
	// Optional newline after opener.
	if s.i < len(s.src) && s.src[s.i] == '\r' {
		s.advance()
	}
	if s.i < len(s.src) && s.src[s.i] == '\n' {
		s.advance()
	}
	start := s.i
	steps := 0
	limit := len(s.src) + 4
	for s.i < len(s.src) {
		steps++
		if steps > limit {
			break
		}
		if s.starts(`"""`) {
			lit := string(s.src[start:s.i])
			s.advanceN(3)
			return token{kind: tString, lit: lit}
		}
		if s.src[s.i] == '\\' && s.i+1 < len(s.src) {
			s.advance()
		}
		s.advance()
	}
	return token{kind: tString, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanChar() token {
	s.advance() // '
	start := s.i
	for s.i < len(s.src) && s.src[s.i] != '\'' && s.src[s.i] != '\n' {
		if s.src[s.i] == '\\' && s.i+1 < len(s.src) {
			s.advance()
		}
		s.advance()
	}
	lit := string(s.src[start:s.i])
	if s.i < len(s.src) && s.src[s.i] == '\'' {
		s.advance()
	}
	return token{kind: tChar, lit: lit}
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
		s.i++
		return
	}
	_, w := utf8.DecodeRune(s.src[s.i:])
	if w < 1 {
		w = 1
	}
	s.i += w
	s.col++
}

func (s *scanner) advanceN(n int) {
	for i := 0; i < n; i++ {
		s.advance()
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || c == '_' || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c >= 0x80
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

func isKeyword(s string) bool {
	switch s {
	case "abstract", "assert", "boolean", "break", "byte", "case", "catch",
		"char", "class", "const", "continue", "default", "do", "double",
		"else", "enum", "extends", "final", "finally", "float", "for",
		"goto", "if", "implements", "import", "instanceof", "int",
		"interface", "long", "native", "new", "package", "private",
		"protected", "public", "return", "short", "static", "strictfp",
		"super", "switch", "synchronized", "this", "throw", "throws",
		"transient", "try", "void", "volatile", "while",
		"var", "yield", "record", "sealed", "permits", "non-sealed",
		"true", "false", "null":
		return true
	}
	return false
}

func isModifier(s string) bool {
	switch s {
	case "public", "protected", "private", "static", "final", "abstract",
		"native", "synchronized", "transient", "volatile", "default",
		"strictfp", "sealed", "non-sealed":
		return true
	}
	return false
}

func isTypeKeyword(s string) bool {
	switch s {
	case "class", "interface", "enum", "record":
		return true
	}
	return false
}

// lineHasIgnore reports whether the 1-based source line carries a dcd ignore.
func lineHasIgnore(src []byte, line int) bool {
	if line <= 0 {
		return false
	}
	cur := 1
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			if cur == line || cur == line-1 {
				seg := strings.ToLower(string(src[start:i]))
				if strings.Contains(seg, "dcd:ignore") ||
					strings.Contains(seg, "deadcode:ignore") ||
					strings.Contains(seg, "nolint:dcd") {
					return true
				}
				if strings.Contains(seg, "suppresswarnings") && strings.Contains(seg, "unused") {
					return true
				}
			}
			if cur == line {
				return false
			}
			cur++
			start = i + 1
		}
	}
	return false
}

func fileIgnore(src []byte) bool {
	head := src
	if len(head) > 1024 {
		head = head[:1024]
	}
	low := strings.ToLower(string(head))
	return strings.Contains(low, "dcd:ignore-file") || strings.Contains(low, "deadcode:ignore-file")
}

func generatedJava(src []byte) bool {
	head := src
	if len(head) > 1024 {
		head = head[:1024]
	}
	s := string(head)
	low := strings.ToLower(s)
	return (strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT")) ||
		strings.Contains(s, "@generated") ||
		strings.Contains(s, "AUTO-GENERATED") ||
		strings.Contains(low, "@generated") ||
		strings.Contains(s, "javax.annotation.Generated") ||
		strings.Contains(s, "jakarta.annotation.Generated")
}
