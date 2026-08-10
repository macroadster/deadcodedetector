// Package python detects unused imports, functions, classes, variables, and files.
package python

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
	tPunct
	tNewline
)

type token struct {
	kind   int
	lit    string
	line   int
	col    int
	indent int // column of first non-space on the line; -1 if not line-start token
}

type scanner struct {
	src  []byte
	i    int
	line int
	col  int
}

func tokenize(src []byte) []token {
	s := &scanner{src: src, line: 1, col: 1}
	if bytes.HasPrefix(src, []byte("#!")) {
		s.skipLine()
	}
	var out []token
	lineStart := true
	indent := 0
	steps := 0
	limit := len(src)*3 + 16
	for {
		steps++
		if steps > limit {
			break
		}
		s.skipSpaceAndComments(&lineStart, &indent)
		if s.i >= len(s.src) {
			break
		}
		// Explicit line continuation: backslash-newline
		if s.src[s.i] == '\\' {
			if s.i+1 < len(s.src) && s.src[s.i+1] == '\n' {
				s.advance()
				s.advance()
				s.line++
				s.col = 1
				lineStart = false
				continue
			}
			if s.i+2 < len(s.src) && s.src[s.i+1] == '\r' && s.src[s.i+2] == '\n' {
				s.advance()
				s.advance()
				s.advance()
				s.line++
				s.col = 1
				lineStart = false
				continue
			}
		}
		if s.src[s.i] == '\n' {
			startLine, startCol := s.line, s.col
			s.advance()
			s.line++
			s.col = 1
			out = append(out, token{kind: tNewline, lit: "\n", line: startLine, col: startCol, indent: indent})
			lineStart = true
			indent = 0
			continue
		}
		if s.src[s.i] == '\r' {
			s.advance()
			continue
		}
		startLine, startCol := s.line, s.col
		tokIndent := -1
		if lineStart {
			tokIndent = indent
		}
		var t token
		if isStringStart(s) {
			t = s.scanString()
		} else {
			t = s.next()
		}
		if t.kind == tEOF {
			if s.i < len(s.src) {
				s.advance()
			}
			continue
		}
		t.line, t.col, t.indent = startLine, startCol, tokIndent
		out = append(out, t)
		lineStart = false
	}
	return out
}

func (s *scanner) skipSpaceAndComments(lineStart *bool, indent *int) {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ' ' || c == '\t' || c == '\f' {
			if *lineStart {
				if c == '\t' {
					*indent += 8
				} else {
					*indent++
				}
			}
			s.advance()
			continue
		}
		if c == '#' {
			s.skipLine()
			continue
		}
		break
	}
}

func (s *scanner) skipLine() {
	for s.i < len(s.src) && s.src[s.i] != '\n' {
		s.advance()
	}
}

func (s *scanner) next() token {
	if s.i >= len(s.src) {
		return token{kind: tEOF}
	}
	c := s.src[s.i]
	if isIdentStart(c) {
		return s.scanIdent()
	}
	if isDigit(c) || (c == '.' && s.i+1 < len(s.src) && isDigit(s.src[s.i+1])) {
		return s.scanNumber()
	}
	if s.i+1 < len(s.src) {
		two := string(s.src[s.i : s.i+2])
		switch two {
		case "==", "!=", "<=", ">=", "//", "**", "<<", ">>",
			"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
			":=", "->", "...":
			s.advance()
			s.advance()
			return token{kind: tPunct, lit: two}
		}
	}
	s.advance()
	return token{kind: tPunct, lit: string(c)}
}

func isStringStart(s *scanner) bool {
	if s.i >= len(s.src) {
		return false
	}
	if s.src[s.i] == '\'' || s.src[s.i] == '"' {
		return true
	}
	i := s.i
	for i < len(s.src) {
		c := s.src[i] | 0x20
		if c == 'r' || c == 'u' || c == 'b' || c == 'f' {
			i++
			continue
		}
		break
	}
	if i == s.i || i >= len(s.src) {
		return false
	}
	if s.src[i] != '\'' && s.src[i] != '"' {
		return false
	}
	prefix := strings.ToLower(string(s.src[s.i:i]))
	switch prefix {
	case "r", "u", "b", "f", "fr", "rf", "br", "rb", "ur", "ru":
		return true
	default:
		return false
	}
}

func (s *scanner) scanString() token {
	// Consume letter prefixes that form a valid string prefix.
	prefixStart := s.i
	for s.i < len(s.src) {
		c := s.src[s.i] | 0x20
		if c != 'r' && c != 'u' && c != 'b' && c != 'f' {
			break
		}
		j := s.i + 1
		for j < len(s.src) {
			cj := s.src[j] | 0x20
			if cj == 'r' || cj == 'u' || cj == 'b' || cj == 'f' {
				j++
				continue
			}
			break
		}
		if j < len(s.src) && (s.src[j] == '\'' || s.src[j] == '"') {
			s.advance()
			continue
		}
		break
	}
	isFString := false
	for k := prefixStart; k < s.i; k++ {
		if s.src[k]|0x20 == 'f' {
			isFString = true
			break
		}
	}
	if s.i >= len(s.src) || (s.src[s.i] != '\'' && s.src[s.i] != '"') {
		return s.scanIdent()
	}
	quote := s.src[s.i]
	triple := s.i+2 < len(s.src) && s.src[s.i+1] == quote && s.src[s.i+2] == quote
	if triple {
		s.advance()
		s.advance()
		s.advance()
	} else {
		s.advance()
	}
	var content bytes.Buffer
	var fExprs []string
	for s.i < len(s.src) {
		c := s.src[s.i]
		// f-string interpolation: {expr}
		if isFString && c == '{' {
			if s.i+1 < len(s.src) && s.src[s.i+1] == '{' {
				// escaped brace
				s.advance()
				s.advance()
				content.WriteByte('{')
				continue
			}
			s.advance() // {
			expr, ok := s.scanFStringExpr()
			if ok && expr != "" {
				fExprs = append(fExprs, expr)
			}
			continue
		}
		if triple {
			if c == quote && s.i+2 < len(s.src) && s.src[s.i+1] == quote && s.src[s.i+2] == quote {
				s.advance()
				s.advance()
				s.advance()
				break
			}
			if c == '\n' {
				content.WriteByte(c)
				s.advance()
				s.line++
				s.col = 1
				continue
			}
			if c == '\\' {
				s.advance()
				if s.i < len(s.src) {
					if s.src[s.i] == '\n' {
						s.advance()
						s.line++
						s.col = 1
						continue
					}
					content.WriteByte(s.src[s.i])
					s.advance()
				}
				continue
			}
			content.WriteByte(c)
			s.advance()
			continue
		}
		if c == quote {
			s.advance()
			break
		}
		if c == '\n' {
			break
		}
		if c == '\\' {
			s.advance()
			if s.i < len(s.src) {
				if s.src[s.i] == '\n' {
					s.advance()
					s.line++
					s.col = 1
					continue
				}
				content.WriteByte(s.src[s.i])
				s.advance()
			}
			continue
		}
		content.WriteByte(c)
		s.advance()
	}
	lit := content.String()
	// Encode f-string expressions after a NUL separator so the parser can
	// recover them without a second pass over the source.
	if len(fExprs) > 0 {
		lit = lit + "\x00" + strings.Join(fExprs, "\x00")
	}
	return token{kind: tString, lit: lit}
}

// scanFStringExpr consumes an f-string {expression} (possibly nested braces)
// and returns the expression text (without format spec).
func (s *scanner) scanFStringExpr() (string, bool) {
	start := s.i
	depth := 1
	inStr := byte(0)
	triple := false
	for s.i < len(s.src) && depth > 0 {
		c := s.src[s.i]
		if inStr != 0 {
			if c == '\\' {
				s.advance()
				if s.i < len(s.src) {
					s.advance()
				}
				continue
			}
			if triple {
				if c == inStr && s.i+2 < len(s.src) && s.src[s.i+1] == inStr && s.src[s.i+2] == inStr {
					s.advance()
					s.advance()
					s.advance()
					inStr = 0
					triple = false
					continue
				}
			} else if c == inStr {
				s.advance()
				inStr = 0
				continue
			}
			if c == '\n' {
				s.line++
				s.col = 1
			}
			s.advance()
			continue
		}
		if c == '\'' || c == '"' {
			inStr = c
			if s.i+2 < len(s.src) && s.src[s.i+1] == c && s.src[s.i+2] == c {
				triple = true
				s.advance()
				s.advance()
				s.advance()
			} else {
				s.advance()
			}
			continue
		}
		if c == '{' {
			depth++
			s.advance()
			continue
		}
		if c == '}' {
			depth--
			if depth == 0 {
				expr := string(s.src[start:s.i])
				s.advance() // }
				// Strip format spec / conversion: expr!r:spec
				if i := strings.IndexAny(expr, "!:="); i >= 0 {
					// careful: `=` is debug (Python 3.8 f"{x=}") — keep name before =
					// `!` conversion, `:` format. Also walrus := inside expr —
					// only strip from last unmatched. Simple approach: strip at
					// first ! or : that is not inside nested parens.
					expr = stripFStringSpec(expr)
				}
				return strings.TrimSpace(expr), true
			}
			s.advance()
			continue
		}
		if c == '\n' {
			s.line++
			s.col = 1
		}
		s.advance()
	}
	return "", false
}

func stripFStringSpec(expr string) string {
	depth := 0
	inStr := byte(0)
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if inStr != 0 {
			if c == '\\' && i+1 < len(expr) {
				i++
				continue
			}
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			inStr = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && (c == '!' || c == ':') {
			return expr[:i]
		}
		// f"{x=}" debug syntax
		if depth == 0 && c == '=' && i+1 == len(expr) {
			return expr[:i]
		}
		if depth == 0 && c == '=' && i+1 < len(expr) && expr[i+1] != '=' {
			// could be debug if at end-ish; keep conservative: only if rest is empty or format
			rest := expr[i+1:]
			if rest == "" || rest[0] == '!' || rest[0] == ':' {
				return expr[:i]
			}
		}
	}
	return expr
}

func (s *scanner) scanIdent() token {
	start := s.i
	for s.i < len(s.src) && isIdentContinue(s.src[s.i]) {
		s.advance()
	}
	return token{kind: tIdent, lit: string(s.src[start:s.i])}
}

func (s *scanner) scanNumber() token {
	start := s.i
	if s.src[s.i] == '0' && s.i+1 < len(s.src) {
		n := s.src[s.i+1] | 0x20
		if n == 'x' || n == 'o' || n == 'b' {
			s.advance()
			s.advance()
			for s.i < len(s.src) && (isIdentContinue(s.src[s.i]) || s.src[s.i] == '_') {
				s.advance()
			}
			return token{kind: tNumber, lit: string(s.src[start:s.i])}
		}
	}
	for s.i < len(s.src) {
		c := s.src[s.i]
		if isDigit(c) || c == '_' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' ||
			c == 'j' || c == 'J' {
			s.advance()
			continue
		}
		break
	}
	return token{kind: tNumber, lit: string(s.src[start:s.i])}
}

func (s *scanner) advance() {
	if s.i >= len(s.src) {
		return
	}
	_, size := utf8.DecodeRune(s.src[s.i:])
	s.i += size
	s.col++
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// lineHasIgnore reports whether the 1-based source line carries a dcd ignore.
func lineHasIgnore(src []byte, line int) bool {
	if line <= 0 {
		return false
	}
	cur := 1
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			if cur == line {
				seg := strings.ToLower(string(src[start:i]))
				if strings.Contains(seg, "dcd:ignore") ||
					strings.Contains(seg, "deadcode:ignore") ||
					strings.Contains(seg, "nolint:dcd") {
					return true
				}
				if strings.Contains(seg, "noqa") &&
					(strings.Contains(seg, "f401") || strings.Contains(seg, "f841") || strings.Contains(seg, "dcd")) {
					return true
				}
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

func generatedPy(src []byte) bool {
	head := src
	if len(head) > 512 {
		head = head[:512]
	}
	s := string(head)
	return (strings.Contains(s, "Code generated") && strings.Contains(s, "DO NOT EDIT")) ||
		strings.Contains(s, "@generated") ||
		strings.Contains(s, "AUTO-GENERATED")
}

func isDunder(name string) bool {
	return len(name) >= 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}

func looksLikeTestName(name string) bool {
	if strings.HasPrefix(name, "test_") || strings.HasPrefix(name, "Test") {
		return true
	}
	switch name {
	case "setUp", "tearDown", "setUpClass", "tearDownClass",
		"setUpModule", "tearDownModule", "asyncSetUp", "asyncTearDown":
		return true
	}
	return false
}

var pyKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true,
	"class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true,
	"for": true, "from": true, "global": true, "if": true,
	"import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true,
	"raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

func isKeyword(s string) bool { return pyKeywords[s] }
