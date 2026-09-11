package host

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"
)

// defaultHTTPTimeout bounds a request with no explicit net.set_timeout,
// matching URLSession.shared's own default (60s). Without this, a page
// fetch, webview fetch, or canvas.load_font against an unresponsive server
// hangs forever: the CLI runs requests on context.Background() (no
// deadline of its own) and holds the per-chapter flock (see
// downloads.AcquireLock) the whole time it's stuck.
const defaultHTTPTimeout = 60 * time.Second

var (
	sharedHTTPClientOnce sync.Once
	sharedHTTPClient     *http.Client
)

// defaultUserAgent is the browser User-Agent presented when a source's
// request doesn't set its own. It matches the iPad/Safari string the WebView
// already exposes as navigator.userAgent (see webview.go), so net.* and the
// webview present consistently — and, critically, sites like WeebCentral
// return HTTP 403 to Go's bare "Go-http-client/1.1" agent while serving real
// content to a browser agent.
const defaultUserAgent = "Mozilla/5.0 (iPad; CPU iPad OS 26_5_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.5.2 Mobile/15E148 Safari/605.1.15"

// defaultUAInjector adds the browser User-Agent to any outgoing request that
// doesn't already carry one of its own. Requests that set an explicit UA (a
// source sending its own header) are left untouched.
type defaultUAInjector struct{}

func (defaultUAInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// SharedHTTPClient returns a process-wide *http.Client with a cookie jar,
// matching AidokuRunner's use of URLSession.shared for the `net` namespace
// (Net.swift) — which, on iOS, shares cookie storage with WKWebView. Net
// and WebView both default to this client when no override is configured,
// so a cookie set during a webview challenge (e.g. Cloudflare clearance)
// is automatically picked up by subsequent net.send() calls, and vice
// versa. A shared jar across sources is safe: cookiejar.Jar still scopes
// cookies per-domain, so unrelated sources' cookies never mix.
//
// Unlike Go's default client it presents a browser User-Agent (see
// defaultUserAgent), so sites gatekeeping on UA respond normally.
func SharedHTTPClient() *http.Client {
	sharedHTTPClientOnce.Do(func() {
		jar, _ := cookiejar.New(nil)
		sharedHTTPClient = &http.Client{Jar: jar, Transport: defaultUAInjector{}, Timeout: defaultHTTPTimeout}
	})
	return sharedHTTPClient
}
