package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eric/deadcodedetector/internal/finding"
)

func TestTextAndJSON(t *testing.T) {
	fs := []finding.Finding{{
		Language: finding.Go,
		Kind:     finding.UnusedFunction,
		Path:     "a.go",
		Line:     3,
		Column:   6,
		Name:     "dead",
		Message:  "unused function dead",
	}}
	var buf bytes.Buffer
	if err := Write(&buf, "text", fs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "a.go:3:6: unused function dead") {
		t.Fatalf("text: %s", buf.String())
	}
	buf.Reset()
	if err := Write(&buf, "json", fs); err != nil {
		t.Fatal(err)
	}
	var r jsonReport
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Count != 1 || r.Findings[0].Name != "dead" {
		t.Fatalf("%+v", r)
	}
	buf.Reset()
	if err := Write(&buf, "sarif", fs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"ruleId": "unused_function"`) {
		t.Fatalf("sarif: %s", buf.String())
	}
}
