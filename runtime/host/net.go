package host

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

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

	rateLimit RateLimit
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

	n.rateLimit.Wait()

	client := n.client()
	if req.Timeout > 0 {
		c := *client
		c.Timeout = req.Timeout
		client = &c
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		req.ResponseError = err
		return netRequestError
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		req.ResponseError = err
		return netRequestError
	}
	req.Response = resp
	req.ResponseData = data
	return netSuccess
}

func (n *Net) sendAll(ctx context.Context, m api.Module, descriptorsOffset, length int32) netResult {
	if descriptorsOffset < 0 || length <= 0 {
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

	errs := make([]int32, length)
	var wg sync.WaitGroup
	for idx, d := range descriptors {
		wg.Add(1)
		go func(idx int, descriptor int32) {
			defer wg.Done()
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
