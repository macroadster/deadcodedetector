// Package report formats findings for humans and machines.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/eric/deadcodedetector/internal/finding"
)

// Write emits findings in the named format ("text", "json", "sarif").
func Write(w io.Writer, format string, fs []finding.Finding) error {
	finding.Sort(fs)
	switch format {
	case "", "text":
		return writeText(w, fs)
	case "json":
		return writeJSON(w, fs)
	case "sarif":
		return writeSARIF(w, fs)
	default:
		return fmt.Errorf("unknown format %q (want text, json, sarif)", format)
	}
}

func writeText(w io.Writer, fs []finding.Finding) error {
	for _, f := range fs {
		if _, err := fmt.Fprintln(w, f.String()); err != nil {
			return err
		}
	}
	if len(fs) == 0 {
		_, err := fmt.Fprintln(w, "no dead code found")
		return err
	}
	sum := finding.Summary(fs)
	var langs []string
	for lang, n := range sum {
		langs = append(langs, fmt.Sprintf("%s=%d", lang, n))
	}
	sort.Strings(langs)
	_, err := fmt.Fprintf(w, "\n%d finding(s)", len(fs))
	if err != nil {
		return err
	}
	if len(langs) > 0 {
		_, err = fmt.Fprintf(w, " (%s)", joinComma(langs))
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w)
	return err
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

type jsonReport struct {
	Findings []finding.Finding `json:"findings"`
	Summary  map[string]int    `json:"summary"`
	Count    int               `json:"count"`
}

func writeJSON(w io.Writer, fs []finding.Finding) error {
	sum := map[string]int{}
	for _, f := range fs {
		sum[string(f.Language)]++
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonReport{Findings: fs, Summary: sum, Count: len(fs)})
}

type sarifLog struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string `json:"id"`
	ShortDescription struct {
		Text string `json:"text"`
	} `json:"shortDescription"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation struct {
		URI string `json:"uri"`
	} `json:"artifactLocation"`
	Region struct {
		StartLine   int `json:"startLine"`
		StartColumn int `json:"startColumn"`
	} `json:"region"`
}

func writeSARIF(w io.Writer, fs []finding.Finding) error {
	seen := map[finding.Kind]bool{}
	var rules []sarifRule
	var results []sarifResult
	for _, f := range fs {
		if !seen[f.Kind] {
			seen[f.Kind] = true
			r := sarifRule{ID: string(f.Kind)}
			r.ShortDescription.Text = string(f.Kind)
			rules = append(rules, r)
		}
		line, col := f.Line, f.Column
		if line <= 0 {
			line = 1
		}
		if col <= 0 {
			col = 1
		}
		res := sarifResult{
			RuleID:  string(f.Kind),
			Level:   "warning",
			Message: sarifMessage{Text: f.Message},
			Locations: []sarifLocation{{
				PhysicalLocation: sarifPhysical{},
			}},
		}
		res.Locations[0].PhysicalLocation.ArtifactLocation.URI = f.Path
		res.Locations[0].PhysicalLocation.Region.StartLine = line
		res.Locations[0].PhysicalLocation.Region.StartColumn = col
		results = append(results, res)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	log := sarifLog{
		Version: "2.1.0",
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarifRun{{
			Tool:    sarifTool{Driver: sarifDriver{Name: "dcd", Rules: rules}},
			Results: results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
