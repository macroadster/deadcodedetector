package ignore

import "testing"

func TestMatcher(t *testing.T) {
	m := FromPatterns([]string{
		"node_modules/",
		"*.min.js",
		"vendor/",
		"!vendor/keep.js",
		"testdata/",
	})
	cases := []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"src/main.go", false, false},
		{"node_modules", true, true},
		{"node_modules/x.js", false, true},
		{"app.min.js", false, true},
		{"app.js", false, false},
		{"vendor", true, true},
		{"testdata", true, true},
		{"testdata/foo.go", false, true},
	}
	for _, tc := range cases {
		if got := m.Ignore(tc.rel, tc.dir); got != tc.want {
			t.Errorf("Ignore(%q, dir=%v)=%v want %v", tc.rel, tc.dir, got, tc.want)
		}
	}
}
