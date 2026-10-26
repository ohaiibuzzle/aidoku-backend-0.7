package main

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in     string
		want   version
		wantOK bool
	}{
		{"v1.2.3", version{1, 2, 3, 1}, true},
		{"1.2.3", version{1, 2, 3, 1}, true},
		{"v0.1.0", version{0, 1, 0, 1}, true},
		{"1.2.3-beta.1", version{1, 2, 3, 0}, true},
		{"dev-abc1234", version{}, false},
		{"", version{}, false},
		{"v1.2", version{}, false},
		{"v1.2.3.4", version{}, false},
	}
	for _, c := range cases {
		got, ok := parseVersion(c.in)
		if ok != c.wantOK {
			t.Errorf("parseVersion(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseVersion(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b version
		want int
	}{
		{version{1, 0, 0}, version{1, 0, 0}, 0},
		{version{1, 0, 0}, version{1, 0, 1}, -1},
		{version{1, 2, 0}, version{1, 1, 9}, 1},
		{version{0, 9, 9}, version{1, 0, 0}, -1},
		{version{1, 2, 3, 0}, version{1, 2, 3, 1}, -1}, // 1.2.3-rc1 < 1.2.3
		{version{1, 2, 3, 1}, version{1, 2, 4, 0}, -1}, // 1.2.3 < 1.2.4-rc1
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestUpdateStatus guards the "no VERSION file yet" / dev-build path:
// parseVersion failing for either side must give "unknown_current" rather
// than a comparison against a zero-value version (which would otherwise
// read as "up to date" for literally any real release).
func TestUpdateStatus(t *testing.T) {
	cases := []struct{ cur, latest, want string }{
		{"", "v1.0.0", "unknown_current"},
		{"dev-abc1234", "v1.0.0", "unknown_current"},
		{"v1.0.0", "garbage", "unknown_current"},
		{"v0.9.0", "v1.0.0", "update_available"},
		{"v1.0.0-rc1", "v1.0.0", "update_available"},
		{"v1.0.0", "v1.0.0", "up_to_date"},
		{"v1.1.0", "v1.0.0", "up_to_date"},
	}
	for _, c := range cases {
		if got := updateStatus(c.cur, c.latest); got != c.want {
			t.Errorf("updateStatus(%q, %q) = %q, want %q", c.cur, c.latest, got, c.want)
		}
	}
}

func TestStripTopLevelDir(t *testing.T) {
	cases := map[string]string{
		"aidoku.koplugin/":                     "",
		"aidoku.koplugin/main.lua":             "main.lua",
		"aidoku.koplugin/bin/arm64/aidoku-run": "bin/arm64/aidoku-run",
		"loose-file.txt":                       "",
	}
	for in, want := range cases {
		if got := stripTopLevelDir(in); got != want {
			t.Errorf("stripTopLevelDir(%q) = %q, want %q", in, got, want)
		}
	}
}
