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
	mergeCookies(path, []*http.Cookie{{Name: "a", Value: "b", Domain: "example.com"}})
	if b, err := os.ReadFile(path + ".corrupt"); err != nil || string(b) != "{not valid json" {
		t.Fatalf("corrupt file not preserved: %q %v", b, err)
	}
	if got, corrupt := readCookieFile(path); corrupt || len(got["example.com"]) != 1 {
		t.Fatalf("merge result: %+v %v", got, corrupt)
	}
}
