// Package c detects unused functions, variables, macros, includes, files,
// and unused dynamic-library exports in C.
package c

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
	tNewline
)

type token struct {
	kind   int
	lit    string
	line   int
	col    int
	spaced bool // true if whitespace/comments preceded this token
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
	lineStart := true
	steps := 0
	limit := len(src)*3 + 16
	for {
		steps++
		if steps > limit {
			break
		}
		spaced := s.skipSpaceAndComments(&lineStart)
		if s.i >= len(s.src) {
			break
		}
		// Logical line continuation: backslash + newline is not a token.
		if s.src[s.i] == '\\' && s.continuesLine() {
			s.skipLineContinue()
			continue
		}
		if s.src[s.i] == '\n' {
			line, col := s.line, s.col
			s.advance()
			out = append(out, token{kind: tNewline, lit: "\n", line: line, col: col, spaced: spaced})
			lineStart = true
			continue
		}
		if s.src[s.i] == '\r' {
			s.advance()
			continue
		}
		line, col := s.line, s.col
		t := s.next()
		if t.kind == tEOF {
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		t.line, t.col, t.spaced = line, col, spaced
		out = append(out, t)
		lineStart = false
	}
	return out
}

func (s *scanner) skipSpaceAndComments(lineStart *bool) bool {
	spaced := false
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ' ' || c == '\t' || c == '\f' || c == '\v' {
			spaced = true
			s.advance()
			continue
		}
		if c == '/' && s.i+1 < len(s.src) {
			switch s.src[s.i+1] {
			case '/':
				spaced = true
				s.advance()
				s.advance()
				for s.i < len(s.src) && s.src[s.i] != '\n' {
					s.advance()
				}
				continue
			case '*':
				spaced = true
				s.advance()
				s.advance()
				for s.i+1 < len(s.src) && !(s.src[s.i] == '*' && s.src[s.i+1] == '/') {
					if s.src[s.i] == '\n' {
						*lineStart = true
					}
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
	return spaced
}

func (s *scanner) continuesLine() bool {
	j := s.i + 1
	if j < len(s.src) && s.src[j] == '\r' {
		j++
	}
	return j < len(s.src) && s.src[j] == '\n'
}

func (s *scanner) skipLineContinue() {
	s.advance() // backslash
	if s.i < len(s.src) && s.src[s.i] == '\r' {
		s.advance()
	}
	if s.i < len(s.src) && s.src[s.i] == '\n' {
		s.advance()
	}
}

func (s *scanner) next() token {
	if s.i >= len(s.src) {
		return token{kind: tEOF}
	}
	if s.looksLikeWideLiteral() {
		return s.scanStringOrChar()
	}
	c := s.src[s.i]
	switch c {
	case '"':
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
	}
	if s.i+1 < len(s.src) {
		two := string(s.src[s.i : s.i+2])
		switch two {
		case "==", "!=", "<=", ">=", "&&", "||", "<<", ">>",
			"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
			"++", "--", "->", "##":
			s.advance()
			s.advance()
			return token{kind: tPunct, lit: two}
		}
	}
	if s.starts("<<=") || s.starts(">>=") {
		lit := string(s.src[s.i : s.i+3])
		s.advanceN(3)
		return token{kind: tPunct, lit: lit}
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

func (s *scanner) looksLikeWideLiteral() bool {
	if s.i >= len(s.src) {
		return false
	}
	// L"..." / L'...' / u"..." / U"..." / u8"..."
	if s.src[s.i] == 'L' || s.src[s.i] == 'U' || s.src[s.i] == 'u' {
		n := 1
		if s.src[s.i] == 'u' && s.i+1 < len(s.src) && s.src[s.i+1] == '8' {
			n = 2
		}
		if s.i+n < len(s.src) {
			q := s.src[s.i+n]
			return q == '"' || q == '\''
		}
	}
	return false
}

func (s *scanner) scanStringOrChar() token {
	// Consume prefix then delegate.
	if s.src[s.i] == 'u' && s.i+1 < len(s.src) && s.src[s.i+1] == '8' {
		s.advanceN(2)
	} else {
		s.advance()
	}
	if s.i < len(s.src) && s.src[s.i] == '\'' {
		return s.scanChar()
	}
	return s.scanString()
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
		for s.i < len(s.src) && (s.src[s.i] == '0' || s.src[s.i] == '1' || s.src[s.i] == '\'') {
			s.advance()
		}
	} else {
		for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '\'') {
			s.advance()
		}
		if s.i < len(s.src) && s.src[s.i] == '.' && s.i+1 < len(s.src) && isDigit(s.src[s.i+1]) {
			s.advance()
			for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '\'') {
				s.advance()
			}
		}
		if s.i < len(s.src) && (s.src[s.i] == 'e' || s.src[s.i] == 'E' || s.src[s.i] == 'p' || s.src[s.i] == 'P') {
			s.advance()
			if s.i < len(s.src) && (s.src[s.i] == '+' || s.src[s.i] == '-') {
				s.advance()
			}
			for s.i < len(s.src) && (isDigit(s.src[s.i]) || s.src[s.i] == '\'') {
				s.advance()
			}
		}
	}
	for s.i < len(s.src) {
		c := s.src[s.i] | 0x20
		if c == 'u' || c == 'l' || c == 'f' || c == 'd' {
			s.advance()
			continue
		}
		break
	}
	return token{kind: tNumber, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanString() token {
	s.advance() // opening "
	start := s.i
	steps := 0
	limit := len(s.src) + 4
	for s.i < len(s.src) && s.src[s.i] != '"' && s.src[s.i] != '\n' {
		steps++
		if steps > limit {
			break
		}
		if s.src[s.i] == '\\' && s.i+1 < len(s.src) {
			s.advance()
		}
		s.advance()
	}
	lit := string(s.src[start:s.i])
	if s.i < len(s.src) && s.src[s.i] == '"' {
		s.advance()
	}
	return token{kind: tString, lit: unescapeC(lit)}
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

func unescapeC(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case '\\', '"', '\'':
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '\''
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c >= 0x80
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

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

func generatedC(src []byte) bool {
	head := src
	if len(head) > 1024 {
		head = head[:1024]
	}
	s := string(head)
	return (strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT")) ||
		strings.Contains(s, "DO NOT EDIT") && strings.Contains(s, "generated") ||
		strings.Contains(s, "AUTO-GENERATED") ||
		strings.Contains(s, "Automatically generated")
}
