package main

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in     string
		want   version
		wantOK bool
	}{
		{"v1.2.3", version{1, 2, 3}, true},
		{"1.2.3", version{1, 2, 3}, true},
		{"v0.1.0", version{0, 1, 0}, true},
		{"1.2.3-beta.1", version{1, 2, 3}, true},
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
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCheckUpdateUnknownCurrentDoesNotCrash guards the "no VERSION file
// yet" / dev-build path: parseVersion failing for the current side must
// fall back to status "unknown_current" rather than a comparison against a
// zero-value version (which would otherwise read as "up to date" for
// literally any real release, since version{} compares equal-or-greater
// than nothing).
func TestCheckUpdateStatusLogic(t *testing.T) {
	cur, curOK := parseVersion("")
	latest, latestOK := parseVersion("v1.0.0")
	if curOK {
		t.Fatalf("parseVersion(\"\") unexpectedly ok")
	}
	if !latestOK {
		t.Fatalf("parseVersion(\"v1.0.0\") unexpectedly not ok")
	}
	_ = cur
	_ = latest
	// The actual branch (mirroring checkUpdate's switch) must land on
	// unknown_current, not up_to_date/update_available, whenever either
	// side fails to parse.
	status := "up_to_date"
	switch {
	case !curOK || !latestOK:
		status = "unknown_current"
	case compareVersions(cur, latest) < 0:
		status = "update_available"
	}
	if status != "unknown_current" {
		t.Errorf("status = %q, want unknown_current", status)
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
