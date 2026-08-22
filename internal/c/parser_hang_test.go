package c

import (
	"strings"
	"testing"
	"time"
)

func TestParserTerminatesOnGarbage(t *testing.T) {
	cases := [][]byte{
		[]byte("int foo("),
		[]byte("#define"),
		[]byte("#include <"),
		[]byte("/* unterminated"),
		[]byte(`"unterminated`),
		[]byte(strings.Repeat("{{{{", 200)),
		[]byte("static int (*fp"),
		[]byte("#if 0\nint x(\n"),
		[]byte("extern \"C\" {"),
		[]byte("__attribute__((constructor"),
		[]byte("int foo(x)\nint x;"),
	}
	for i, src := range cases {
		done := make(chan struct{})
		go func() {
			_ = extract(src)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("case %d hung", i)
		}
	}
}
