package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCookieEntryHostOnlyStaysHostOnly guards against a host-only cookie
// (IncludeSubdomains == false) coming back out of cookie() with a non-empty
// Domain field: per RFC 6265 (and Go's cookiejar, which is what this feeds
// via jar.SetCookies in injectCookies), ANY non-empty Domain attribute --
// dotted or not -- makes a cookie apply to subdomains too. The only way to
// get a host-only cookie back out of SetCookies is an empty Domain.
func TestCookieEntryHostOnlyStaysHostOnly(t *testing.T) {
	e := cookieEntry{Domain: "example.com", IncludeSubdomains: false, Name: "a", Value: "b"}
	c := e.cookie()
	if c.Domain != "" {
		t.Errorf("host-only cookie got Domain = %q, want empty (or it becomes a domain cookie on injection)", c.Domain)
	}
}

func TestCookieEntryIncludeSubdomainsSetsDomain(t *testing.T) {
	e := cookieEntry{Domain: "example.com", IncludeSubdomains: true, Name: "a", Value: "b"}
	c := e.cookie()
	if c.Domain != "example.com" {
		t.Errorf("subdomain cookie got Domain = %q, want %q", c.Domain, "example.com")
	}
}

func TestWriteCookieFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	f := cookieFile{
		"example.com": []cookieEntry{{Domain: "example.com", Name: "a", Value: "b"}},
	}
	if err := writeCookieFile(path, f); err != nil {
		t.Fatalf("writeCookieFile: %v", err)
	}
	got, _ := readCookieFile(path)
	if len(got) != 1 || len(got["example.com"]) != 1 || got["example.com"][0].Name != "a" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

// TestWriteCookieFileLeavesNoTempFile guards the temp+rename fix: a
// leftover .cookies-*.tmp file next to cookies.json would mean the write
// path isn't actually atomic (or is failing to clean up after itself).
func TestWriteCookieFileLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	if err := writeCookieFile(path, cookieFile{}); err != nil {
		t.Fatalf("writeCookieFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "cookies.json" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}

// TestReadCookieFileCorruptDoesNotPanic checks that a corrupt cookies.json
// degrades to "no cookies" (matching the pre-existing behavior a caller
// relies on) rather than panicking -- readCookieFile now also warns to
// stderr in this case, but that isn't observable from here.
func TestReadCookieFileCorruptDoesNotPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seeding corrupt file: %v", err)
	}
	got, corrupt := readCookieFile(path)
	if len(got) != 0 || !corrupt {
		t.Fatalf("expected empty, corrupt cookieFile for corrupt input, got %+v %v", got, corrupt)
	}
}

// A merge into a corrupt cookies.json must keep the old bytes aside rather
// than overwrite them with only the new cookies.
func TestMergeCookiesKeepsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeCookies(path, []*http.Cookie{{Name: "a", Value: "b", Domain: "example.com"}}); err != nil {
		t.Fatalf("mergeCookies: %v", err)
	}
	if b, err := os.ReadFile(path + ".corrupt"); err != nil || string(b) != "{not valid json" {
		t.Fatalf("corrupt file not preserved: %q %v", b, err)
	}
	if got, corrupt := readCookieFile(path); corrupt || len(got["example.com"]) != 1 {
		t.Fatalf("merge result: %+v %v", got, corrupt)
	}
}

func TestRemoveCookieEntries(t *testing.T) {
	file := func() cookieFile {
		return cookieFile{
			"www.example.com": {{Name: "host", Path: "/"}, {Name: "keep", Path: "/"}, {Name: "deep", Path: "/a"}},
			"example.com":     {{Name: "sub", IncludeSubdomains: true}, {Name: "hostonly"}},
			"other.test":      {{Name: "host"}},
		}
	}
	names := func(f cookieFile, domain string) string {
		var out []string
		for _, e := range f[domain] {
			out = append(out, e.Name)
		}
		return strings.Join(out, ",")
	}
	const host = "www.example.com"

	f := file()
	if removeCookieEntries(f, host, "", []string{"missing"}) {
		t.Error("nothing matched, but reported a change (would rewrite cookies.json for nothing)")
	}
	f = file()
	if !removeCookieEntries(f, host, "", []string{"host", "deep", "sub", "hostonly"}) {
		t.Fatal("expected a change")
	}
	if got := names(f, host); got != "keep,deep" {
		t.Errorf("page host entries = %q (a non-/ path can't be expired in the jar)", got)
	}
	if got := names(f, "example.com"); got != "hostonly" {
		t.Errorf("parent entries = %q (a parent's host-only cookie isn't visible)", got)
	}
	if got := names(f, "other.test"); got != "host" {
		t.Errorf("another site's entry was touched: %q", got)
	}

	f = file()
	removeCookieEntries(f, host, "example.com", []string{"host"})
	if got := names(f, host); !strings.Contains(got, "host") {
		t.Errorf("a domain-limited delete removed a row on another domain: %q", got)
	}
	f = cookieFile{host: {{Name: "only", Path: "/"}}}
	removeCookieEntries(f, host, "", []string{"only"})
	if _, ok := f[host]; ok {
		t.Error("an emptied domain should be dropped")
	}
}

func TestForgetCookiesRewritesOnlyWhenNeeded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	if err := writeCookieFile(path, cookieFile{"example.com": {{Name: "a", Path: "/"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := forgetCookies(path, "example.com", "", []string{"zzz"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Error("cookies.json was replaced though nothing was removed")
	}
	if err := forgetCookies(path, "example.com", "", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if f, _ := readCookieFile(path); len(f) != 0 {
		t.Errorf("cookie survived: %v", f)
	}
}
