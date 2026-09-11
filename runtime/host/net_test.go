package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestSendClearsStaleResponseOnFailedResend guards against a guest reusing
// the same descriptor across multiple send() calls (e.g. set_url then
// send() again) seeing a prior successful send's Response/ResponseData
// still populated after a later send fails -- a guest that checks for
// response data without separately checking ResponseError could otherwise
// silently treat a failed re-send as if it had succeeded with stale data.
func TestSendClearsStaleResponseOnFailedResend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	store := NewStore()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	req := &NetRequest{Method: NetMethodGet, URL: u, Headers: map[string]string{}}
	descriptor := store.Store(req)
	n := &Net{Store: store, Client: srv.Client()}

	if result := n.send(context.Background(), descriptor); result != netSuccess {
		t.Fatalf("first send: result = %v, want netSuccess", result)
	}
	if req.Response == nil || len(req.ResponseData) == 0 {
		t.Fatalf("expected a populated response after the first send")
	}

	srv.Close() // the second send must fail against the now-closed server

	if result := n.send(context.Background(), descriptor); result != netRequestError {
		t.Fatalf("second send: result = %v, want netRequestError", result)
	}
	if req.Response != nil {
		t.Errorf("stale Response not cleared after a failed re-send")
	}
	if req.ResponseData != nil {
		t.Errorf("stale ResponseData not cleared after a failed re-send")
	}
	if req.ResponseError == nil {
		t.Errorf("expected ResponseError to be set after a failed re-send")
	}
}

// TestDescriptorsFitInMemory guards send_all's guest-controlled length
// against the OOM it used to allow: a guest passing a huge length used to
// reach make([]int32, length) before any memory-bounds check ran.
func TestDescriptorsFitInMemory(t *testing.T) {
	cases := []struct {
		name           string
		offset, length int32
		memSize        uint32
		want           bool
	}{
		{"fits exactly", 0, 4, 16, true},
		{"fits with room", 0, 4, 1024, true},
		{"offset plus length exceeds memory", 0, 5, 16, false},
		{"huge length far exceeds memory", 0, 1<<30, 1 << 16, false},
		{"huge length would overflow a naive int32 multiply", 100, 1 << 29, 1 << 16, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := descriptorsFitInMemory(c.offset, c.length, c.memSize)
			if got != c.want {
				t.Errorf("descriptorsFitInMemory(%d, %d, %d) = %v, want %v", c.offset, c.length, c.memSize, got, c.want)
			}
		})
	}
}

// TestHasDuplicateDescriptor guards send_all against two goroutines
// concurrently mutating the same *NetRequest (Response/ResponseData/
// ResponseError) when a guest passes the same descriptor twice in one
// batch.
func TestHasDuplicateDescriptor(t *testing.T) {
	cases := []struct {
		name        string
		descriptors []int32
		want        bool
	}{
		{"empty", nil, false},
		{"all unique", []int32{1, 2, 3}, false},
		{"duplicate", []int32{1, 2, 1}, true},
		{"adjacent duplicate", []int32{5, 5}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasDuplicateDescriptor(c.descriptors); got != c.want {
				t.Errorf("hasDuplicateDescriptor(%v) = %v, want %v", c.descriptors, got, c.want)
			}
		})
	}
}

func TestSharedHTTPClientHasDefaultTimeout(t *testing.T) {
	if got := SharedHTTPClient().Timeout; got <= 0 {
		t.Errorf("SharedHTTPClient().Timeout = %v, want a positive default", got)
	}
}
