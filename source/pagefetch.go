package source

import (
	"context"
	"fmt"
	"image"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
)

// defaultPageUserAgent is sent on page image requests when the source's own
// request doesn't set one. Plenty of image CDNs treat Go's default
// "Go-http-client" UA as a bot signal.
const defaultPageUserAgent = "Mozilla/5.0 (iPad; CPU iPad OS 26_5_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.5.2 Mobile/15E148 Safari/605.1.15"

// Mirrors upstream DownloadTask's maxPageRetries / retryBaseDelay / maxRetryDelay.
const (
	maxPageRetries = 3
	retryBaseDelay = time.Second
	maxRetryDelay  = 60 * time.Second
)

// pageFetch is the outcome of a fetch that reached the point of sending a
// request. failure is set when the server or network didn't deliver the page,
// which process_page_image may still recover from; the other fields then hold
// only what was learned before giving up.
type pageFetch struct {
	req     *http.Request
	status  int
	header  http.Header
	body    []byte
	failure error
}

// downloadURLPage fetches a URL page and, for sources that have it, runs
// process_page_image over the result.
func (s *Source) downloadURLPage(ctx context.Context, rawURL string, pc models.PageContext) ([]byte, error) {
	f, err := s.fetchURL(ctx, rawURL, pc)
	if err != nil {
		return nil, err
	}
	if s.Runner.Features.ProcessesPages {
		return s.processPage(ctx, pc, f)
	}
	return f.body, f.failure
}

// downloadURL fetches rawURL with no page context or processing.
func (s *Source) downloadURL(ctx context.Context, rawURL string) ([]byte, error) {
	f, err := s.fetchURL(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return f.body, f.failure
}

// fetchURL GETs rawURL, retrying 429/5xx and transport errors like upstream's
// fetchPageData. The request is rebuilt on every attempt (the guest's body
// reader is consumed by a send). The error return is for what nothing can
// recover from: no request could be built, or ctx is done.
func (s *Source) fetchURL(ctx context.Context, rawURL string, pc models.PageContext) (pageFetch, error) {
	if rawURL == "" {
		return pageFetch{}, fmt.Errorf("source: page has empty URL")
	}
	for attempt := 0; ; attempt++ {
		req, err := s.buildImageRequest(ctx, rawURL, pc)
		if err != nil {
			return pageFetch{}, err
		}
		f, retryable := fetchOnce(req, rawURL)
		if f.failure != nil && ctx.Err() != nil {
			return pageFetch{}, f.failure
		}
		if !retryable || attempt >= maxPageRetries {
			return f, nil
		}
		t := time.NewTimer(retryDelay(f.header, attempt))
		select {
		case <-ctx.Done():
			t.Stop()
			return pageFetch{}, fmt.Errorf("%w (while retrying: %v)", ctx.Err(), f.failure)
		case <-t.C:
		}
	}
}

// fetchOnce makes one attempt and reports whether a failure is worth retrying.
// A failed or over-cap body read isn't.
func fetchOnce(req *http.Request, rawURL string) (f pageFetch, retryable bool) {
	f.req = req
	resp, err := host.SharedHTTPClient().Do(req)
	if err != nil {
		f.failure = fmt.Errorf("source: fetching %s: %w", rawURL, err)
		return f, true
	}
	defer resp.Body.Close()
	f.status, f.header = resp.StatusCode, resp.Header
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		f.failure = fmt.Errorf("source: fetching %s: unexpected status %s", rawURL, resp.Status)
		return f, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	}
	if f.body, err = host.ReadAllLimited(resp.Body, host.MaxResponseBytes); err != nil {
		f.failure = fmt.Errorf("source: reading %s: %w", rawURL, err)
	}
	return f, false
}

// retryDelay honours Retry-After (seconds or HTTP date) capped at
// maxRetryDelay, else exponential backoff from retryBaseDelay.
func retryDelay(header http.Header, attempt int) time.Duration {
	v := strings.TrimSpace(header.Get("Retry-After"))
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs == secs { // NaN check
		return min(maxRetryDelay, max(0, time.Duration(min(secs, 3600)*float64(time.Second))))
	}
	if t, err := http.ParseTime(v); err == nil {
		return min(maxRetryDelay, max(0, time.Until(t)))
	}
	return min(maxRetryDelay, retryBaseDelay<<attempt)
}

// processPage runs the source's process_page_image on a fetched page, as
// upstream's PageInterceptorProcessor does. After a failed fetch the guest
// still gets a 404 and a blank placeholder, so it can recover.
//
// Like upstream, the guest gets a decoded image when the bytes decode, else
// the raw bytes. Decoding once here matters: host.Canvas re-decodes stored
// bytes on every draw, which is once per tile for a descrambler.
func (s *Source) processPage(ctx context.Context, pc models.PageContext, f pageFetch) ([]byte, error) {
	u := f.req.URL.String()
	resp := models.Response{
		Code:    404,
		Headers: map[string]string{},
		Request: models.Request{URL: &u, Headers: flattenHeader(f.req.Header)},
	}
	var stored any = image.NewGray(image.Rect(0, 0, 1, 1))
	if f.failure == nil {
		resp.Code, resp.Headers = uint16(f.status), flattenHeader(f.header)
		stored = f.body
		if img, err := host.DecodeImageBounded(f.body); err == nil {
			stored = img
		}
	}
	resp.Image = s.Runner.StoreValue(stored)
	// The guest may return this same descriptor, which resolveImageRef also
	// frees; Store.Remove is a no-op the second time.
	defer s.Runner.RemoveValue(resp.Image)

	out, err := s.Runner.ProcessPageImage(ctx, resp, pc)
	if err != nil {
		if f.failure != nil {
			return nil, fmt.Errorf("source: process_page_image: %w (after fetch failed: %v)", err, f.failure)
		}
		return nil, fmt.Errorf("source: process_page_image: %w", err)
	}
	return s.resolveImageRef(out)
}

func flattenHeader(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		m[k] = strings.Join(v, ", ")
	}
	return m
}

// buildImageRequest builds the outgoing HTTP request for a page-content URL
// (a single image, or a ZipFile page's containing zip). Like upstream's
// getModifiedImageRequest, any failure to get a request from the guest falls
// back to a plain GET. A guest trap closes the wasm module, so later guest
// calls then fail too. The page context goes only to get_image_request, never
// into HTTP headers.
func (s *Source) buildImageRequest(ctx context.Context, rawURL string, pc models.PageContext) (*http.Request, error) {
	var req *http.Request
	if netReq, err := s.GetImageRequest(ctx, rawURL, pc); err == nil {
		req, _ = netReq.ToHTTPRequest(ctx) // nil on any failure, or if the guest set no URL
	}
	if req == nil {
		var err error
		if req, err = http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil); err != nil {
			return nil, fmt.Errorf("source: building request for %s: %w", rawURL, err)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultPageUserAgent)
	}
	return req, nil
}
