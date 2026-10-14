package host

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The shared client must present a browser User-Agent when the request has
// none, and leave an explicit one alone. Sites like WeebCentral 403 the bare
// Go UA, which previously made sources that rely on a default UA return
// empty results.
func TestSharedHTTPClientDefaultUserAgent(t *testing.T) {
	var gotUA string
	var gotUA2 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/no" {
			gotUA = r.Header.Get("User-Agent")
		} else {
			gotUA2 = r.Header.Get("User-Agent")
		}
	}))
	defer srv.Close()

	c := SharedHTTPClient()

	resp, err := c.Get(srv.URL + "/no")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotUA == "" {
		t.Fatalf("expected a default browser User-Agent, got %q", gotUA)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/explicit", nil)
	req.Header.Set("User-Agent", "MySourceAgent/1.0")
	resp2, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if gotUA2 != "MySourceAgent/1.0" {
		t.Fatalf("explicit UA should not be overridden, got %q", gotUA2)
	}
}

// End-to-end: a cookie injected into the shared client's jar is actually
// sent on a subsequent request (this is what gets a source past Cloudflare
// once a browser's cf_clearance is injected).
func TestSharedClientCookieInjectionSent(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Cookie")
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL + "/")
	c := SharedHTTPClient()
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "cf_clearance", Value: "secret", Path: "/"}})

	resp, err := c.Get(srv.URL + "/thing")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(gotHeader, "cf_clearance=secret") {
		t.Fatalf("expected injected cookie on request, got Cookie=%q", gotHeader)
	}
}

// hasCookie reports whether the jar handed back a cookie by that name --
// presence rather than a count, so re-running this test (or sharing the
// process-wide jar with another test) can't turn it into a false failure.
func hasCookie(cookies []*http.Cookie, name string) bool {
	for _, c := range cookies {
		if c.Name == name {
			return true
		}
	}
	return false
}

// The jar is process-wide (URLSession.shared semantics: every Interpreter
// shares it), so it must enforce RFC 6265's public-suffix rule. Built with
// cookiejar.New(nil) it does not: the public-suffix rejection is skipped and
// buckets are keyed by the last two labels, so a Set-Cookie whose Domain is
// a multi-label public suffix ("co.uk") is accepted from one host and then
// sent to any unrelated host sharing that suffix -- one source injecting
// cookies into another source's requests, and clobbering a site's
// cf_clearance.
func TestSharedHTTPClientJarRejectsPublicSuffixCookie(t *testing.T) {
	jar := SharedHTTPClient().Jar
	attacker := &url.URL{Scheme: "https", Host: "attacker.co.uk", Path: "/"}
	victim := &url.URL{Scheme: "https", Host: "victim.co.uk", Path: "/"}

	jar.SetCookies(attacker, []*http.Cookie{{Name: "sid", Value: "attacker", Domain: "co.uk", Path: "/"}})
	if sent := jar.Cookies(victim); hasCookie(sent, "sid") {
		t.Errorf("a cookie scoped to the public suffix co.uk was sent to another host: %v", sent)
	}

	// The scoping the shared jar exists for must keep working: a domain
	// cookie for the registrable domain reaches that host and its
	// subdomains, and a host-only cookie reaches exactly its host.
	jar.SetCookies(attacker, []*http.Cookie{{Name: "own", Value: "1", Domain: "attacker.co.uk", Path: "/"}})
	if sent := jar.Cookies(attacker); !hasCookie(sent, "own") {
		t.Errorf("a domain cookie for attacker.co.uk must be sent to attacker.co.uk, got %v", sent)
	}
	sub := &url.URL{Scheme: "https", Host: "sub.attacker.co.uk", Path: "/"}
	if sent := jar.Cookies(sub); !hasCookie(sent, "own") {
		t.Errorf("a domain cookie for attacker.co.uk must be sent to its subdomains, got %v", sent)
	}

	jar.SetCookies(attacker, []*http.Cookie{{Name: "hostonly", Value: "1", Path: "/"}})
	if sent := jar.Cookies(attacker); !hasCookie(sent, "hostonly") {
		t.Errorf("a host-only cookie must still be sent to its host, got %v", sent)
	}
	if sent := jar.Cookies(sub); hasCookie(sent, "hostonly") {
		t.Errorf("a host-only cookie must not be sent to a subdomain, got %v", sent)
	}
}
