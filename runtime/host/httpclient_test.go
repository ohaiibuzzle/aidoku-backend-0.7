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
