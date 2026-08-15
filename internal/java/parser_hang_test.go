package java

import (
	"strings"
	"testing"
	"time"
)

func TestParserTerminatesOnGarbage(t *testing.T) {
	cases := [][]byte{
		[]byte("class {{{{{"),
		[]byte("import com.foo."),
		[]byte("/* unterminated"),
		[]byte(`"""unterminated text block`),
		[]byte(strings.Repeat("<<<<>>>>", 200)),
		[]byte("enum E { A, B, C"),
		[]byte("record R(int x"),
		[]byte("@interface A {"),
		[]byte("package com.example\nimport static\nclass X"),
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
