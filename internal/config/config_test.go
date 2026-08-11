package config

import "testing"

func TestParseLangs(t *testing.T) {
	got, err := ParseLangs("go, js, CSS")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "go" || got[1] != "js" || got[2] != "css" {
		t.Fatalf("%v", got)
	}
	got, err = ParseLangs("python")
	if err != nil || len(got) != 1 || got[0] != "py" {
		t.Fatalf("python: %v %v", got, err)
	}
	got, err = ParseLangs("java")
	if err != nil || len(got) != 1 || got[0] != "java" {
		t.Fatalf("java: %v %v", got, err)
	}
	if _, err := ParseLangs("ruby"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseExported(t *testing.T) {
	v, err := ParseExported("auto")
	if err != nil || v != nil {
		t.Fatalf("%v %v", v, err)
	}
	v, err = ParseExported("true")
	if err != nil || v == nil || !*v {
		t.Fatalf("%v %v", v, err)
	}
	v, err = ParseExported("false")
	if err != nil || v == nil || *v {
		t.Fatalf("%v %v", v, err)
	}
}

func TestWantsAuto(t *testing.T) {
	c := Default()
	if !c.Wants("go", map[string]bool{"go": true}) {
		t.Fatal("auto should run detected langs")
	}
	if c.Wants("css", map[string]bool{"go": true}) {
		t.Fatal("auto should not run missing langs")
	}
	c.Langs = []string{"css"}
	if !c.Wants("css", map[string]bool{"go": true}) {
		t.Fatal("explicit lang should run even if undetected")
	}
}
