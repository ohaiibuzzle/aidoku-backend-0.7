package host

import "testing"

func TestLDMLYear(t *testing.T) {
	for pattern, want := range map[string]string{
		"y-MM-dd":    "2006-01-02",
		"yy-MM-dd":   "06-01-02",
		"yyyy-MM-dd": "2006-01-02",
	} {
		if got := ldmlToGoLayout(pattern); got != want {
			t.Errorf("ldmlToGoLayout(%q) = %q, want %q", pattern, got, want)
		}
	}
}
