package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime"
)

// A Source whose guest exports nothing: get_image_request is unimplemented, so
// every fetch goes through the plain-GET path.
func bareSource() *Source { return &Source{Runner: &runtime.Interpreter{}} }

func TestFetchURLRetriesThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	data, err := bareSource().downloadURL(context.Background(), srv.URL)
	if err != nil || string(data) != "ok" || n.Load() != 3 {
		t.Fatalf("data=%q err=%v attempts=%d", data, err, n.Load())
	}
}

func TestFetchURLNoRetryOn404AndGivesUpAfterMax(t *testing.T) {
	for status, want := range map[int]int32{404: 1, 500: 1 + maxPageRetries} {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(status)
		}))
		if _, err := bareSource().downloadURL(context.Background(), srv.URL); err == nil || n.Load() != want {
			t.Errorf("status %d: err=%v attempts=%d, want %d", status, err, n.Load(), want)
		}
		srv.Close()
	}
}

func TestFetchURLRetryAfterAndCtxCancel(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := bareSource().downloadURL(ctx, srv.URL)
	if err == nil || time.Since(start) > 5*time.Second || n.Load() != 1 {
		t.Fatalf("err=%v elapsed=%v attempts=%d", err, time.Since(start), n.Load())
	}
}

func TestRetryDelay(t *testing.T) {
	after := func(v string) http.Header { return http.Header{"Retry-After": {v}} }
	for _, c := range []struct {
		h    http.Header
		att  int
		want time.Duration
	}{
		{after("5"), 0, 5 * time.Second},
		{after("9999"), 0, maxRetryDelay},
		{after("-3"), 0, 0},
		{after("NaN"), 1, 2 * time.Second}, // NaN falls to backoff
		{after("junk"), 2, 4 * time.Second},
		{nil, 0, time.Second},
		{nil, 10, maxRetryDelay},
	} {
		if got := retryDelay(c.h, c.att); got != c.want {
			t.Errorf("retryDelay(%v, %d) = %v, want %v", c.h, c.att, got, c.want)
		}
	}
	d := retryDelay(after(time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat)), 0)
	if d < 5*time.Second || d > 10*time.Second {
		t.Errorf("HTTP-date Retry-After gave %v", d)
	}
}

func TestPageContextIsNotSentAsHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	pc := models.PageContext{"Referer": "https://evil.example/", "User-Agent": "ctx-ua", "X-Foo": "bar"}
	if _, err := bareSource().downloadURLPage(context.Background(), srv.URL, pc); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-Foo") != "" || got.Get("Referer") != "" || got.Get("User-Agent") != defaultPageUserAgent {
		t.Errorf("context leaked into headers: %v", got)
	}
}

func TestFlattenHeader(t *testing.T) {
	m := flattenHeader(http.Header{"Content-Type": {"image/png"}, "Set-Cookie": {"a=1", "b=2"}})
	if m["Content-Type"] != "image/png" || !strings.Contains(m["Set-Cookie"], "a=1, b=2") {
		t.Errorf("got %v", m)
	}
}
