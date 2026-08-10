package javascript

import (
	"strings"
	"testing"
	"time"
)

func TestParserMakesProgress(t *testing.T) {
	cases := []struct{ name, src string }{
		{"comma", "foo,\nbar\n"},
		{"rparen", "foo)\n"},
		{"rbrace", "foo}\n"},
		{"rbrack", "foo]\n"},
		{"just_comma", ","},
		{"just_rbrace", "}"},
		{"export_then_comma", "export const x = 1\n,"},
		{"require_comma", "require('a'), require('b')"},
		{"jsx_comma", "export function A(){ return <div>{a, b}</div> }"},
		{"minified", "!function(){return 1}(),function(){return 2}()"},
		{"generic", "const x = <T>(y: T) => y"},
		{"generic_extends", "const x = <T extends Foo>(y: T) => y"},
		{"tsx", "export default function App(){ return (<div className={foo}><span/></div>) }"},
		{"stray_closers", "function f(){}\n}\n)\n]\n,"},
		{"empty", ""},
		{"only_punct", "((((("},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				_ = extract([]byte(tc.src))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("parser hung on %s: %q", tc.name, tc.src)
			}
		})
	}
}

func TestStringLiteralsIncludeTemplateTernaryClasses(t *testing.T) {
	src := []byte("const x = `sl-bubble assistant typing${busy ? ' sl-bubble-work' : ''}`\n")
	lits := StringLiterals(src)
	joined := strings.Join(lits, " ")
	if !strings.Contains(joined, "sl-bubble") || !strings.Contains(joined, "sl-bubble-work") {
		t.Fatalf("template ternary class missing from literals: %q", lits)
	}
}

func TestRequireCommaStillRecordsBoth(t *testing.T) {
	ex := extract([]byte("require('./a'), require('./b')"))
	if len(ex.DynamicSpecs) < 2 {
		t.Fatalf("expected both require() specs, got %v", ex.DynamicSpecs)
	}
}
