package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSettings struct{ m map[string]any }

func newMemSettings() *memSettings { return &memSettings{m: map[string]any{}} }

func (s *memSettings) Object(key string) any { return s.m[key] }

func (s *memSettings) SetValue(key string, value any) error {
	if value == nil {
		delete(s.m, key)
	} else {
		s.m[key] = value
	}
	return nil
}

// TestNetSendFlareSolverrRetry exercises the full second-line-retry path: a
// GET request that comes back as a Cloudflare challenge is solved via a
// (faked) FlareSolverr instance, and the retried request carries the
// solved cookie and User-Agent.
func TestNetSendFlareSolverrRetry(t *testing.T) {
	var sawRetryCookie, sawRetryUA string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("cf_clearance"); err == nil {
			sawRetryCookie = c.Value
			sawRetryUA = r.Header.Get("User-Agent")
			w.Write([]byte("ok"))
			return
		}
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Just a moment..."))
	}))
	defer target.Close()

	targetURL, _ := url.Parse(target.URL)

	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req flaresolverrRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding flaresolverr request: %v", err)
		}
		if req.Cmd != "request.get" {
			t.Fatalf("unexpected cmd %q", req.Cmd)
		}
		resp := flaresolverrResponse{Status: "ok"}
		resp.Solution.UserAgent = "TestBrowser/1.0"
		resp.Solution.Cookies = []flaresolverrCookie{
			{Name: "cf_clearance", Value: "solved-token", Domain: targetURL.Hostname(), Path: "/"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer flare.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	settings := newMemSettings()

	var gotCallbackCookies []*http.Cookie
	n := &Net{
		Store:        NewStore(),
		Client:       client,
		FlareSolverr: NewFlareSolverrClient(flare.URL),
		Settings:     settings,
		SourceKey:    "testsource",
		OnFlareSolverrCookies: func(u *url.URL, cookies []*http.Cookie) {
			gotCallbackCookies = cookies
		},
	}

	descriptor := n.Store.Store(&NetRequest{Method: NetMethodGet, URL: targetURL, Headers: map[string]string{}})

	result := n.send(context.Background(), descriptor)
	if result != netSuccess {
		t.Fatalf("send: expected netSuccess, got %v", result)
	}

	req, ok := n.request(descriptor)
	if !ok {
		t.Fatal("request descriptor missing after send")
	}
	if string(req.ResponseData) != "ok" {
		t.Fatalf("expected retried request to reach the real content, got %q (challenge page never got past)", req.ResponseData)
	}
	if sawRetryCookie != "solved-token" {
		t.Fatalf("expected retried request to carry solved cf_clearance cookie, got %q", sawRetryCookie)
	}
	if sawRetryUA != "TestBrowser/1.0" {
		t.Fatalf("expected retried request to carry the FlareSolverr User-Agent, got %q", sawRetryUA)
	}
	if len(gotCallbackCookies) != 1 || gotCallbackCookies[0].Value != "solved-token" {
		t.Fatalf("expected OnFlareSolverrCookies to fire with the solved cookie, got %v", gotCallbackCookies)
	}
	if ua := settings.Object("testsource." + flareSolverrUAKey); ua != "TestBrowser/1.0" {
		t.Fatalf("expected the solved User-Agent to be persisted under the source's settings key, got %v", ua)
	}

	// A second request (fresh descriptor, no challenge simulated this time
	// since the cookie is now in the jar) should still present the
	// persisted UA even without a fresh solve.
	var sawSecondUA string
	target2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSecondUA = r.Header.Get("User-Agent")
	}))
	defer target2.Close()
	target2URL, _ := url.Parse(target2.URL)

	descriptor2 := n.Store.Store(&NetRequest{Method: NetMethodGet, URL: target2URL, Headers: map[string]string{}})
	if result := n.send(context.Background(), descriptor2); result != netSuccess {
		t.Fatalf("second send: expected netSuccess, got %v", result)
	}
	if sawSecondUA != "TestBrowser/1.0" {
		t.Fatalf("expected persisted UA to be applied to a later request with no explicit UA, got %q", sawSecondUA)
	}
}

// TestConcurrentChallengesSolveOnce: several goroutines (as net.send_all
// runs them) hitting the same challenge must share one FlareSolverr solve.
func TestConcurrentChallengesSolveOnce(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("cf_clearance"); err == nil && r.Header.Get("User-Agent") == "TestBrowser/1.0" && c.Value == "solved" {
			w.Write([]byte("ok"))
			return
		}
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Just a moment..."))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)

	var solves atomic.Int32
	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		solves.Add(1)
		time.Sleep(200 * time.Millisecond) // long enough for every sender to queue up behind this solve
		resp := flaresolverrResponse{Status: "ok"}
		resp.Solution.UserAgent = "TestBrowser/1.0"
		resp.Solution.Cookies = []flaresolverrCookie{{Name: "cf_clearance", Value: "solved", Domain: targetURL.Hostname(), Path: "/"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer flare.Close()

	jar, _ := cookiejar.New(nil)
	n := &Net{
		Store:        NewStore(),
		Client:       &http.Client{Jar: jar},
		FlareSolverr: NewFlareSolverrClient(flare.URL),
		Settings:     newSyncMemSettings(),
		SourceKey:    "testsource",
	}

	const senders = 8
	results := make([]netResult, senders)
	descriptors := make([]int32, senders)
	for i := range descriptors {
		descriptors[i] = n.Store.Store(&NetRequest{Method: NetMethodGet, URL: targetURL, Headers: map[string]string{}})
	}
	var wg sync.WaitGroup
	for i, d := range descriptors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = n.send(context.Background(), d)
		}()
	}
	wg.Wait()

	if got := solves.Load(); got != 1 {
		t.Fatalf("expected exactly 1 FlareSolverr solve, got %d", got)
	}
	for i, d := range descriptors {
		req, _ := n.request(d)
		if results[i] != netSuccess || string(req.ResponseData) != "ok" {
			t.Fatalf("sender %d: result %v, body %q", i, results[i], req.ResponseData)
		}
	}
}

type syncMemSettings struct {
	mu sync.Mutex
	*memSettings
}

func newSyncMemSettings() *syncMemSettings { return &syncMemSettings{memSettings: newMemSettings()} }

func (s *syncMemSettings) Object(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memSettings.Object(key)
}

func (s *syncMemSettings) SetValue(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memSettings.SetValue(key, value)
}

func TestIsCloudflareChallenge(t *testing.T) {
	cases := []struct {
		name   string
		status int
		server string
		header string
		body   string
		want   bool
	}{
		{"cf-mitigated header", 200, "", "challenge", "", true},
		{"503 cloudflare with marker", 503, "cloudflare", "", "Just a moment...", true},
		{"403 cloudflare with marker", 403, "cloudflare", "", "cf-chl-widget", true},
		{"503 non-cloudflare server", 503, "nginx", "", "Just a moment...", false},
		{"503 cloudflare no marker", 503, "cloudflare", "", "server error", false},
		{"200 cloudflare ordinary page", 200, "cloudflare", "", "hello", false},
		{"403 cloudflare ordinary auth failure", 403, "cloudflare", "", "unauthorized", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{}}
			if tc.server != "" {
				resp.Header.Set("Server", tc.server)
			}
			if tc.header != "" {
				resp.Header.Set("cf-mitigated", tc.header)
			}
			if got := isCloudflareChallenge(resp, []byte(tc.body)); got != tc.want {
				t.Errorf("isCloudflareChallenge() = %v, want %v", got, tc.want)
			}
		})
	}
}
