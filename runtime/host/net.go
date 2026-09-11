package host

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// NetworkConcurrencyFromEnv returns the configured Net.MaxConcurrency value
// from the NETWORK_CONCURRENCY environment variable, or 0 (meaning
// "unset, use defaultSendAllMaxConcurrency") if it's absent or not a
// positive integer.
func NetworkConcurrencyFromEnv() int {
	n, err := strconv.Atoi(os.Getenv("NETWORK_CONCURRENCY"))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

type netResult int32

const (
	netSuccess           netResult = 0
	netInvalidDescriptor netResult = -1
	netInvalidString     netResult = -2
	netInvalidMethod     netResult = -3
	netInvalidURL        netResult = -4
	netInvalidHTML       netResult = -5
	netInvalidBufferSize netResult = -6
	netMissingData       netResult = -7
	netMissingResponse   netResult = -8
	netMissingURL        netResult = -9
	netRequestError      netResult = -10
	netFailedMemoryWrite netResult = -11
	netNotAnImage        netResult = -12
)

type NetMethod int32

const (
	NetMethodGet NetMethod = iota
	NetMethodPost
	NetMethodPut
	NetMethodHead
	NetMethodDelete
	NetMethodPatch
	NetMethodOptions
	NetMethodConnect
	NetMethodTrace
)

func (m NetMethod) httpString() (string, bool) {
	switch m {
	case NetMethodGet:
		return http.MethodGet, true
	case NetMethodPost:
		return http.MethodPost, true
	case NetMethodPut:
		return http.MethodPut, true
	case NetMethodHead:
		return http.MethodHead, true
	case NetMethodDelete:
		return http.MethodDelete, true
	case NetMethodPatch:
		return http.MethodPatch, true
	case NetMethodOptions:
		return http.MethodOptions, true
	case NetMethodConnect:
		return http.MethodConnect, true
	case NetMethodTrace:
		return http.MethodTrace, true
	default:
		return "", false
	}
}

// NetRequest mirrors AidokuRunner's NetRequest.swift. Stored as a pointer
// in the Store so in-place mutation (set_header, and the response fields
// populated by send) doesn't require re-storing after every call, unlike
// the Swift value-type original.
type NetRequest struct {
	Method  NetMethod
	URL     *url.URL
	Headers map[string]string
	Body    []byte
	Timeout time.Duration

	Response      *http.Response
	ResponseData  []byte
	ResponseError error
}

func (r *NetRequest) ToHTTPRequest(ctx context.Context) (*http.Request, error) {
	if r.URL == nil {
		return nil, nil
	}
	methodStr, ok := r.Method.httpString()
	if !ok {
		methodStr = http.MethodGet
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, methodStr, r.URL.String(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range r.Headers {
		req.Header.Add(k, v)
	}
	return req, nil
}

// Net implements the `net` namespace (Imports/Net.swift): init, send,
// send_all, set_url, set_header, set_body, set_timeout, data_len,
// read_data, get_image, get_status_code, get_url, get_header, html,
// set_rate_limit.
type Net struct {
	Store *Store

	// Client performs HTTP requests. Defaults to SharedHTTPClient if nil —
	// see its doc comment for why that matters (cookie sharing with the
	// webview DOM/fetch/XHR layer).
	Client *http.Client

	// DecodeImage decodes response bytes into a stored image, returning a
	// Store descriptor. Wired up once the canvas namespace exists; before
	// that, get_image returns notAnImage.
	DecodeImage func(data []byte) (descriptor int32, ok bool)

	// ParseHTML parses response bytes as HTML relative to baseURL and
	// stores the resulting document, returning its Store descriptor. Wired
	// up once the html namespace exists.
	ParseHTML func(data []byte, baseURL string) (descriptor int32, err error)

	// FlareSolverr, set when FLARESOLVERR_HOST is configured, is used as a
	// second-line retry when a GET request comes back as a Cloudflare
	// challenge (see isCloudflareChallenge): it drives a real browser to
	// solve the challenge, then the resulting cookies and browser
	// User-Agent are used to retry the request. Settings/SourceKey persist
	// the solved User-Agent (see flareSolverrUAKey) in the same settings
	// store defaults.get/set uses, namespaced the same way
	// ("<SourceKey>.<key>") — so once a source's UA is solved, every future
	// request (including after a process restart, and including the
	// webview's own fetches — see WebView.FlareSolverr) keeps presenting
	// it, which matters because Cloudflare ties clearance to a specific UA.
	// OnFlareSolverrCookies, if set, is called with the cookies FlareSolverr
	// returned after a successful solve, letting the caller persist them
	// somewhere durable across process restarts (they're always injected
	// into the client Jar for the current process regardless).
	FlareSolverr          *FlareSolverrClient
	Settings              SettingsStore
	SourceKey             string
	OnFlareSolverrCookies func(u *url.URL, cookies []*http.Cookie)

	// MaxConcurrency bounds how many requests sendAll runs at once. <= 0
	// (the default) falls back to defaultSendAllMaxConcurrency.
	MaxConcurrency int

	rateLimit RateLimit
}

// defaultSendAllMaxConcurrency is used when Net.MaxConcurrency is unset.
// Each goroutine's underlying HTTP round trip can pin an OS thread on a
// device with as little as 256MB RAM -- an unbounded fan-out over a large
// descriptor batch would spin up one thread per descriptor with no cap.
const defaultSendAllMaxConcurrency = 8

func (n *Net) maxConcurrency() int {
	if n.MaxConcurrency > 0 {
		return n.MaxConcurrency
	}
	return defaultSendAllMaxConcurrency
}

func (n *Net) flareSolverr() *flareSolverrRetryer {
	return &flareSolverrRetryer{
		Client:    n.FlareSolverr,
		Settings:  n.Settings,
		SourceKey: n.SourceKey,
		OnCookies: n.OnFlareSolverrCookies,
	}
}

func (n *Net) client() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return SharedHTTPClient()
}

func (n *Net) request(descriptor int32) (*NetRequest, bool) {
	req, ok := n.Store.Fetch(descriptor).(*NetRequest)
	return req, ok
}

// LinkNet registers the `net` namespace onto the given host module builder.
func LinkNet(builder wazero.HostModuleBuilder, n *Net) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, method int32) int32 {
			m := NetMethod(method)
			if _, ok := m.httpString(); !ok {
				return int32(netInvalidMethod)
			}
			return n.Store.Store(&NetRequest{Method: m, Headers: map[string]string{}})
		}).
		Export("init")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return int32(n.send(ctx, descriptor))
		}).
		Export("send")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptors, length int32) int32 {
			return int32(n.sendAll(ctx, m, descriptors, length))
		}).
		Export("send_all")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, value, length int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if value < 0 || length <= 0 {
				return int32(netInvalidString)
			}
			b, ok := m.Memory().Read(uint32(value), uint32(length))
			if !ok {
				return int32(netInvalidString)
			}
			u, err := url.Parse(string(b))
			if err != nil {
				return int32(netInvalidURL)
			}
			req.URL = u
			return int32(netSuccess)
		}).
		Export("set_url")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, key, keyLength, value, valueLength int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if key < 0 || keyLength <= 0 || value < 0 || valueLength <= 0 {
				return int32(netInvalidString)
			}
			keyBytes, ok := m.Memory().Read(uint32(key), uint32(keyLength))
			if !ok {
				return int32(netInvalidString)
			}
			valueBytes, ok := m.Memory().Read(uint32(value), uint32(valueLength))
			if !ok {
				return int32(netInvalidString)
			}
			req.Headers[string(keyBytes)] = string(valueBytes)
			return int32(netSuccess)
		}).
		Export("set_header")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, value, length int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if value < 0 || length <= 0 {
				return int32(netInvalidString)
			}
			b, ok := m.Memory().Read(uint32(value), uint32(length))
			if !ok {
				return int32(netInvalidString)
			}
			body := make([]byte, len(b))
			copy(body, b)
			req.Body = body
			return int32(netSuccess)
		}).
		Export("set_body")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32, value float64) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			req.Timeout = time.Duration(value * float64(time.Second))
			return int32(netSuccess)
		}).
		Export("set_timeout")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.ResponseData == nil {
				return int32(netMissingData)
			}
			return int32(len(req.ResponseData))
		}).
		Export("data_len")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor int32, buffer, size uint32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.ResponseData == nil {
				return int32(netMissingData)
			}
			if int(size) > len(req.ResponseData) {
				return int32(netInvalidBufferSize)
			}
			if !m.Memory().Write(buffer, req.ResponseData[:size]) {
				return int32(netFailedMemoryWrite)
			}
			return int32(netSuccess)
		}).
		Export("read_data")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.ResponseData == nil {
				return int32(netMissingData)
			}
			if n.DecodeImage == nil {
				return int32(netNotAnImage)
			}
			d, ok := n.DecodeImage(req.ResponseData)
			if !ok {
				return int32(netNotAnImage)
			}
			return d
		}).
		Export("get_image")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.Response == nil {
				return int32(netMissingResponse)
			}
			return int32(req.Response.StatusCode)
		}).
		Export("get_status_code")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.Response == nil {
				return int32(netMissingResponse)
			}
			if req.Response.Request == nil || req.Response.Request.URL == nil {
				return int32(netMissingURL)
			}
			return n.Store.Store(req.Response.Request.URL.String())
		}).
		Export("get_url")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, key, keyLength int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.Response == nil {
				return int32(netMissingResponse)
			}
			if key < 0 || keyLength <= 0 {
				return int32(netInvalidString)
			}
			keyBytes, ok := m.Memory().Read(uint32(key), uint32(keyLength))
			if !ok {
				return int32(netInvalidString)
			}
			value := req.Response.Header.Get(string(keyBytes))
			if value == "" {
				return int32(netMissingData)
			}
			return n.Store.Store(value)
		}).
		Export("get_header")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			req, ok := n.request(descriptor)
			if !ok {
				return int32(netInvalidDescriptor)
			}
			if req.ResponseData == nil {
				return int32(netMissingData)
			}
			if n.ParseHTML == nil {
				return int32(netInvalidHTML)
			}
			baseURL := ""
			if req.Response != nil && req.Response.Request != nil && req.Response.Request.URL != nil {
				baseURL = req.Response.Request.URL.String()
			}
			d, err := n.ParseHTML(req.ResponseData, baseURL)
			if err != nil {
				return int32(netInvalidHTML)
			}
			return d
		}).
		Export("html")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, permits, period, unit int32) {
			seconds := 1
			switch unit {
			case 1:
				seconds = 60
			case 2:
				seconds = 3600
			}
			n.rateLimit.Set(int(permits), time.Duration(int(period)*seconds)*time.Second)
		}).
		Export("set_rate_limit")

	return builder
}

func (n *Net) send(ctx context.Context, descriptor int32) netResult {
	req, ok := n.request(descriptor)
	if !ok {
		return netInvalidDescriptor
	}
	httpReq, err := req.ToHTTPRequest(ctx)
	if err != nil || httpReq == nil {
		return netMissingURL
	}
	fs := n.flareSolverr()
	if httpReq.Header.Get("User-Agent") == "" {
		if ua := fs.persistedUserAgent(); ua != "" {
			httpReq.Header.Set("User-Agent", ua)
		}
	}

	n.rateLimit.Wait()

	client := n.client()
	if req.Timeout > 0 {
		c := *client
		c.Timeout = req.Timeout
		client = &c
	}

	resp, data, err := doHTTPRequest(client, httpReq)
	if err != nil {
		// A guest can reuse the same descriptor across multiple send()
		// calls (e.g. set_url then send() again). Without clearing these,
		// a failed re-send would leave Response/ResponseData holding the
		// previous, successful send's data -- a guest that reads them
		// without separately checking ResponseError would silently see a
		// stale response instead of the failure.
		req.Response = nil
		req.ResponseData = nil
		req.ResponseError = err
		return netRequestError
	}
	resp, data = fs.maybeRetry(ctx, client, httpReq, resp, data)

	req.Response = resp
	req.ResponseData = data
	return netSuccess
}

// descriptorsFitInMemory reports whether length descriptors (4 bytes each)
// starting at offset fit inside a guest memory of memSize bytes. length is
// a raw guest-supplied int32, not a postcard-decoded value, so it doesn't
// get ReadLen's bound -- without this check a guest could pass e.g.
// length = 2^31-1 and sendAll's make([]int32, length) would try to
// allocate ~8GB before the memory-bounds check on the first ReadUint32Le
// ever runs. The descriptor array is read from guest memory at 4
// bytes/entry, so it can never legitimately need more entries than fit in
// the guest's own linear memory. Widened to uint64 so the multiply itself
// can't overflow back into range for a huge length.
func descriptorsFitInMemory(offset, length int32, memSize uint32) bool {
	end := uint64(offset) + uint64(length)*4
	return end <= uint64(memSize)
}

// hasDuplicateDescriptor reports whether descriptors contains the same
// value more than once. See sendAll's comment on why that's rejected.
func hasDuplicateDescriptor(descriptors []int32) bool {
	seen := make(map[int32]bool, len(descriptors))
	for _, d := range descriptors {
		if seen[d] {
			return true
		}
		seen[d] = true
	}
	return false
}

func (n *Net) sendAll(ctx context.Context, m api.Module, descriptorsOffset, length int32) netResult {
	if descriptorsOffset < 0 || length <= 0 {
		return netInvalidDescriptor
	}
	if !descriptorsFitInMemory(descriptorsOffset, length, m.Memory().Size()) {
		return netInvalidDescriptor
	}
	descriptors := make([]int32, length)
	for idx := 0; idx < int(length); idx++ {
		v, ok := m.Memory().ReadUint32Le(uint32(descriptorsOffset) + uint32(idx*4))
		if !ok {
			return netInvalidDescriptor
		}
		descriptors[idx] = int32(v)
	}

	// n.send mutates the *NetRequest behind each descriptor in place
	// (Response/ResponseData/ResponseError). If the same descriptor
	// appeared twice, two goroutines below would write those fields on the
	// same object concurrently -- an actual data race, not just a
	// last-writer-wins ambiguity, since e.g. ResponseData is a multi-word
	// slice header a torn concurrent write can leave pointing at garbage.
	if hasDuplicateDescriptor(descriptors) {
		return netInvalidDescriptor
	}

	errs := make([]int32, length)
	var wg sync.WaitGroup
	sem := make(chan struct{}, n.maxConcurrency())
	for idx, d := range descriptors {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, descriptor int32) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[idx] = int32(n.send(ctx, descriptor))
		}(idx, d)
	}
	wg.Wait()

	hasError := false
	for idx, e := range errs {
		if e != int32(netSuccess) {
			hasError = true
		}
		m.Memory().WriteUint32Le(uint32(descriptorsOffset)+uint32(idx*4), uint32(e))
	}

	if hasError {
		return netRequestError
	}
	return netSuccess
}
