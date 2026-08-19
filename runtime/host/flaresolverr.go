package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// FlareSolverrHostFromEnv returns the configured FlareSolverr host from the
// FLARESOLVERR_HOST environment variable (e.g. "localhost:8191"), or "" if
// unset.
func FlareSolverrHostFromEnv() string {
	return strings.TrimSpace(os.Getenv("FLARESOLVERR_HOST"))
}

// FlareSolverrClient drives a FlareSolverr instance (https://github.com/FlareSolverr/FlareSolverr)
// to solve a Cloudflare challenge that a plain HTTP request couldn't get
// past on its own: FlareSolverr loads the URL in a real, headless browser,
// waits out the challenge, and hands back the resulting cookies (including
// cf_clearance) and the browser's User-Agent, which the caller must reuse on
// every subsequent request for those cookies to keep working.
type FlareSolverrClient struct {
	// Host is FLARESOLVERR_HOST's value, e.g. "localhost:8191". A scheme
	// prefix is optional; "http://" is assumed if omitted.
	Host string

	// HTTPClient is used to call FlareSolverr itself (not the target site).
	// Defaults to a client with a generous timeout, since solving a
	// challenge in a real browser routinely takes tens of seconds.
	HTTPClient *http.Client
}

// NewFlareSolverrClient returns a client for the given host, or nil if host
// is empty.
func NewFlareSolverrClient(host string) *FlareSolverrClient {
	if host == "" {
		return nil
	}
	return &FlareSolverrClient{Host: host}
}

func (c *FlareSolverrClient) endpoint() string {
	h := c.Host
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	return strings.TrimRight(h, "/") + "/v1"
}

func (c *FlareSolverrClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 90 * time.Second}
}

// FlareSolverrSolution is the outcome of successfully solving a challenge
// for a URL.
type FlareSolverrSolution struct {
	UserAgent string
	Cookies   []*http.Cookie
}

type flaresolverrRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type flaresolverrCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
}

type flaresolverrResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		Cookies   []flaresolverrCookie `json:"cookies"`
		UserAgent string               `json:"userAgent"`
	} `json:"solution"`
}

// Solve asks FlareSolverr to load targetURL in a real browser and solve
// whatever Cloudflare challenge it hits, returning the cookies and
// User-Agent to use on subsequent requests for that site.
func (c *FlareSolverrClient) Solve(ctx context.Context, targetURL string) (*FlareSolverrSolution, error) {
	body, err := json.Marshal(flaresolverrRequest{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: 60000,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed flaresolverrResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("flaresolverr: decoding response: %w", err)
	}
	if parsed.Status != "ok" {
		return nil, fmt.Errorf("flaresolverr: %s", parsed.Message)
	}

	cookies := make([]*http.Cookie, 0, len(parsed.Solution.Cookies))
	for _, fc := range parsed.Solution.Cookies {
		cookie := &http.Cookie{
			Name:     fc.Name,
			Value:    fc.Value,
			Path:     fc.Path,
			Domain:   fc.Domain,
			Secure:   fc.Secure,
			HttpOnly: fc.HTTPOnly,
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		if fc.Expires > 0 {
			cookie.Expires = time.Unix(int64(fc.Expires), 0)
		}
		cookies = append(cookies, cookie)
	}

	return &FlareSolverrSolution{
		UserAgent: parsed.Solution.UserAgent,
		Cookies:   cookies,
	}, nil
}

const flareSolverrUAKey = "flaresolverrUserAgent"

// doHTTPRequest performs httpReq and fully reads its body, closing it
// before returning. Shared by net.go's Net.send and webview.go's
// webviewContext.doRequest, both of which need the same
// do-then-buffer-the-body shape to run FlareSolverr detection/retry on top.
func doHTTPRequest(client *http.Client, httpReq *http.Request) (*http.Response, []byte, error) {
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, data, nil
}

// flareSolverrRetryer bundles what's needed to detect a Cloudflare
// challenge, solve it, and persist what FlareSolverr learns — shared by
// every HTTP call site a source's guest code can drive (net.* and the
// webview's script/module fetches), so a challenge hit via either path
// gets the same treatment and the solved UA/cookies benefit both.
//
// A nil *flareSolverrRetryer is safe to call methods on (all of them treat
// it as "FlareSolverr not configured for this source").
type flareSolverrRetryer struct {
	Client    *FlareSolverrClient
	Settings  SettingsStore
	SourceKey string
	// OnCookies, if set, is called with the cookies FlareSolverr returned
	// after a successful solve, in addition to injecting them into the
	// request's client Jar.
	OnCookies func(u *url.URL, cookies []*http.Cookie)
}

// persistedUserAgent returns the User-Agent a prior solve persisted for
// this source, or "" if none is on record.
func (fs *flareSolverrRetryer) persistedUserAgent() string {
	if fs == nil || fs.Settings == nil || fs.SourceKey == "" {
		return ""
	}
	ua, _ := fs.Settings.Object(fs.SourceKey + "." + flareSolverrUAKey).(string)
	return ua
}

func (fs *flareSolverrRetryer) persistUserAgent(ua string) {
	if fs == nil || fs.Settings == nil || fs.SourceKey == "" || ua == "" {
		return
	}
	_ = fs.Settings.SetValue(fs.SourceKey+"."+flareSolverrUAKey, ua)
}

// maybeRetry checks whether resp/body look like a Cloudflare challenge and,
// if so, solves it via FlareSolverr and retries httpReq once with the
// solved cookies (injected into client's Jar) and User-Agent. httpReq must
// be a GET with no body — the only shape FlareSolverr's request.get command
// can replay. Returns resp/body unchanged if fs is nil, the response wasn't
// a challenge, or the solve/retry itself failed.
func (fs *flareSolverrRetryer) maybeRetry(ctx context.Context, client *http.Client, httpReq *http.Request, resp *http.Response, body []byte) (*http.Response, []byte) {
	if fs == nil || fs.Client == nil || httpReq.Method != http.MethodGet || !isCloudflareChallenge(resp, body) {
		return resp, body
	}
	solution, err := fs.Client.Solve(ctx, httpReq.URL.String())
	if err != nil || solution == nil {
		return resp, body
	}
	if len(solution.Cookies) > 0 && client.Jar != nil {
		client.Jar.SetCookies(httpReq.URL, solution.Cookies)
		if fs.OnCookies != nil {
			fs.OnCookies(httpReq.URL, solution.Cookies)
		}
	}
	if solution.UserAgent != "" {
		fs.persistUserAgent(solution.UserAgent)
	}

	retryReq, err := http.NewRequestWithContext(ctx, http.MethodGet, httpReq.URL.String(), nil)
	if err != nil {
		return resp, body
	}
	retryReq.Header = httpReq.Header.Clone()
	if solution.UserAgent != "" {
		retryReq.Header.Set("User-Agent", solution.UserAgent)
	}
	resp2, data2, err2 := doHTTPRequest(client, retryReq)
	if err2 != nil {
		return resp, body
	}
	return resp2, data2
}

// isCloudflareChallenge reports whether resp/body look like a Cloudflare
// interstitial rather than the site's real response: either the response
// carries a cf-mitigated header, or it's a 403/503 served by cloudflare
// with one of the challenge page's known body markers. This is
// deliberately conservative to avoid mistaking an ordinary 403 from a
// cloudflare-fronted site (e.g. an auth failure) for a challenge.
func isCloudflareChallenge(resp *http.Response, body []byte) bool {
	if resp == nil {
		return false
	}
	if resp.Header.Get("cf-mitigated") != "" {
		return true
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Server")), "cloudflare") {
		return false
	}
	b := string(body)
	return strings.Contains(b, "Just a moment") ||
		strings.Contains(b, "challenge-platform") ||
		strings.Contains(b, "cf-chl")
}
