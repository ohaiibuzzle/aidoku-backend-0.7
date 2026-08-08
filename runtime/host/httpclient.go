package host

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
)

var (
	sharedHTTPClientOnce sync.Once
	sharedHTTPClient     *http.Client
)

// SharedHTTPClient returns a process-wide *http.Client with a cookie jar,
// matching AidokuRunner's use of URLSession.shared for the `net` namespace
// (Net.swift) — which, on iOS, shares cookie storage with WKWebView. Net
// and WebView both default to this client when no override is configured,
// so a cookie set during a webview challenge (e.g. Cloudflare clearance)
// is automatically picked up by subsequent net.send() calls, and vice
// versa. A shared jar across sources is safe: cookiejar.Jar still scopes
// cookies per-domain, so unrelated sources' cookies never mix.
func SharedHTTPClient() *http.Client {
	sharedHTTPClientOnce.Do(func() {
		jar, _ := cookiejar.New(nil)
		sharedHTTPClient = &http.Client{Jar: jar}
	})
	return sharedHTTPClient
}
