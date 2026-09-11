package host

import (
	"strings"
	"testing"
	"time"
)

func TestParseNetscapeCookies(t *testing.T) {
	data := `# Netscape HTTP Cookie File
comix.to	FALSE	/	TRUE	9999999999	session	pT5v
.comix.to	TRUE	/	TRUE	9999999999	cf_clearance	abc123

expired.to	FALSE	/	FALSE	1	old	gone
`
	cookies, err := ParseNetscapeCookies([]byte(data))
	if err != nil {
		t.Fatalf("ParseNetscapeCookies: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("expected 2 non-expired cookies, got %d: %+v", len(cookies), cookies)
	}

	var session, cf *string
	for _, c := range cookies {
		if c.Name == "session" {
			session = &c.Domain
		}
		if c.Name == "cf_clearance" {
			cf = &c.Domain
		}
	}
	if session == nil || *session != "comix.to" {
		t.Fatalf("host-only session cookie: got domain %v", session)
	}
	if cf == nil || *cf != ".comix.to" {
		t.Fatalf("domain cf_clearance cookie: got domain %v", cf)
	}

	// The expired cookie (expiry 1, in the past) must have been dropped.
	for _, c := range cookies {
		if c.Name == "old" {
			t.Fatalf("expired cookie was not dropped: %+v", c)
		}
	}
}

func TestParseNetscapeCookiesHandlesHttpOnlyPrefix(t *testing.T) {
	// A real browser export marks an HttpOnly cookie (cf_clearance always
	// is) with a "#HttpOnly_" prefix before the normal fields, not a real
	// comment -- it must still parse, not be skipped as one.
	data := "#HttpOnly_.comix.to\tTRUE\t/\tTRUE\t9999999999\tcf_clearance\tabc123\n"
	cookies, err := ParseNetscapeCookies([]byte(data))
	if err != nil {
		t.Fatalf("ParseNetscapeCookies: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie from a #HttpOnly_ line, got %d: %+v", len(cookies), cookies)
	}
	if cookies[0].Name != "cf_clearance" || cookies[0].Domain != ".comix.to" {
		t.Fatalf("unexpected cookie: %+v", cookies[0])
	}
}

func TestParseNetscapeCookiesSkipsExpiredAttr(t *testing.T) {
	// A cookie whose Expires is still in the future keeps its Expires set.
	cookies, err := ParseNetscapeCookies([]byte("x.to\tFALSE\t/\tFALSE\t9999999999\tn\tv\n"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	future := time.Unix(9999999999, 0)
	_ = now
	if len(cookies) != 1 || !cookies[0].Expires.Equal(future) || cookies[0].Value != "v" {
		t.Fatalf("unexpected cookie: %+v", cookies)
	}
	if strings.TrimPrefix(cookies[0].Domain, ".") == "" {
		t.Fatalf("expected a domain, got %q", cookies[0].Domain)
	}
}
