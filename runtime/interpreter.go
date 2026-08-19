// Package runtime implements AidokuRunner's Interpreter.swift: it loads a
// compiled Aidoku source .wasm module with wazero, links every host
// namespace (via the host subpackage), probes which optional entry points
// the guest exports, and exposes typed Go methods mirroring Runner.swift's
// protocol.
package runtime

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
)

// Config mirrors AidokuRunner's InterpreterConfiguration.
type Config struct {
	PrintHandler func(string)

	// HTTPClient overrides the client used by the `net` namespace. Defaults
	// to http.DefaultClient.
	HTTPClient *http.Client

	// Settings backs the `defaults` namespace. AidokuRunner uses a single
	// process-wide SettingsStore.shared instance across every Interpreter;
	// callers should likewise construct one settingsstore.Store and share
	// it across every source they load. If nil, defaults.get/set becomes a
	// no-op (reads return "no value", writes are dropped) rather than
	// failing Interpreter construction.
	Settings host.SettingsStore
}

// Interpreter loads and runs a single compiled Aidoku source .wasm module.
// Guest calls are serialized with a mutex, mirroring the Swift
// implementation's `actor Interpreter` isolation: wasm linear memory isn't
// safe for concurrent guest entry.
type Interpreter struct {
	mu sync.Mutex

	sourceKey string
	rt        wazero.Runtime
	module    api.Module
	store     *host.Store
	env       *host.Env
	net       *host.Net

	// httpClient is the client wired into the net/webview namespaces, and
	// cookieJar is the http.CookieJar backing it — by default the
	// process-wide shared jar (matching AidokuRunner's URLSession.shared).
	httpClient *http.Client
	cookieJar  http.CookieJar

	Features models.SourceFeatures
}

// noopSettingsStore is used when Config.Settings is left nil: reads report
// "no value" and writes are silently dropped, rather than failing
// Interpreter construction outright.
type noopSettingsStore struct{}

func (noopSettingsStore) Object(string) any          { return nil }
func (noopSettingsStore) SetValue(string, any) error { return nil }

// New loads bytes as a compiled Aidoku source and links the currently
// implemented host namespaces (env, std; more are added as later phases
// land) onto it.
func New(ctx context.Context, sourceKey string, wasmBytes []byte, config Config) (*Interpreter, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter())

	printHandler := config.PrintHandler
	if printHandler == nil {
		printHandler = func(s string) { fmt.Println(s) }
	}

	i := &Interpreter{
		sourceKey: sourceKey,
		rt:        rt,
		store:     host.NewStore(),
	}

	i.env = &host.Env{
		PrintHandler: printHandler,
	}
	if _, err := host.LinkEnv(rt.NewHostModuleBuilder("env"), i.env).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking env namespace: %w", err)
	}

	std := &host.Std{Store: i.store, PrintHandler: printHandler}
	if _, err := host.LinkStd(rt.NewHostModuleBuilder("std"), std).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking std namespace: %w", err)
	}

	settings := config.Settings
	if settings == nil {
		settings = noopSettingsStore{}
	}
	defaults := &host.Defaults{Store: i.store, Settings: settings, DefaultNamespace: sourceKey}
	if _, err := host.LinkDefaults(rt.NewHostModuleBuilder("defaults"), defaults).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking defaults namespace: %w", err)
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		// Shared across every Interpreter in the process, matching
		// AidokuRunner's use of URLSession.shared — which is also how a
		// webview-cleared cookie ends up visible to plain net.* calls.
		httpClient = host.SharedHTTPClient()
	}
	i.httpClient = httpClient
	i.cookieJar = httpClient.Jar

	i.net = &host.Net{Store: i.store, Client: httpClient}

	htmlLib := host.NewHtml(i.store)
	i.net.ParseHTML = htmlLib.ParseHTML

	canvasLib := &host.Canvas{Store: i.store, HTTPClient: httpClient}
	i.net.DecodeImage = canvasLib.DecodeImage

	if _, err := host.LinkNet(rt.NewHostModuleBuilder("net"), i.net).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking net namespace: %w", err)
	}

	if _, err := host.LinkHtml(rt.NewHostModuleBuilder("html"), htmlLib).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking html namespace: %w", err)
	}

	js := &host.JS{Store: i.store}
	webview := &host.WebView{Store: i.store, Client: httpClient, PrintHandler: printHandler}
	jsBuilder := host.LinkJS(rt.NewHostModuleBuilder("js"), js)
	jsBuilder = host.LinkWebView(jsBuilder, webview)
	if _, err := jsBuilder.Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking js namespace: %w", err)
	}

	if _, err := host.LinkCanvas(rt.NewHostModuleBuilder("canvas"), canvasLib).Instantiate(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: linking canvas namespace: %w", err)
	}

	module, err := rt.InstantiateWithConfig(ctx, wasmBytes, wazero.NewModuleConfig().WithName(sourceKey))
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("runtime: instantiating guest module: %w", err)
	}
	i.module = module

	i.Features = models.SourceFeatures{
		ProvidesListings:         i.hasExport("get_manga_list"),
		ProvidesHome:             i.hasExport("get_home"),
		DynamicFilters:           i.hasExport("get_filters"),
		DynamicSettings:          i.hasExport("get_settings"),
		DynamicListings:          i.hasExport("get_listings"),
		ProcessesPages:           i.hasExport("process_page_image"),
		ProvidesImageRequests:    i.hasExport("get_image_request"),
		ProvidesPageDescriptions: i.hasExport("get_page_description"),
		ProvidesAlternateCovers:  i.hasExport("get_alternate_covers"),
		ProvidesBaseURL:          i.hasExport("get_base_url"),
		HandlesNotifications:     i.hasExport("handle_notification"),
		HandlesDeepLinks:         i.hasExport("handle_deep_link"),
		HandlesBasicLogin:        i.hasExport("handle_basic_login"),
		HandlesWebLogin:          i.hasExport("handle_web_login"),
		HandlesMigration:         i.hasExport("handle_key_migration"),
	}

	if fn := module.ExportedFunction("start"); fn != nil {
		if _, err := fn.Call(ctx); err != nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("runtime: calling guest start(): %w", err)
		}
	}

	return i, nil
}

func (i *Interpreter) hasExport(name string) bool {
	return i.module.ExportedFunction(name) != nil
}

// Close releases the wazero runtime and everything it instantiated,
// including any store items (e.g. *quickjs.VM-backed JS/webview contexts)
// holding native memory that a guest never explicitly freed via
// std.destroy.
func (i *Interpreter) Close(ctx context.Context) error {
	_ = i.store.Close()
	return i.rt.Close(ctx)
}

// --- Cookie injection ---
//
// These let the host application (or a CLI user) insert cookies into the
// cookie jar backing this source's net.* and webview requests — e.g. a
// Cloudflare cf_clearance/__cf_bm taken from a real browser, so requests to
// a CF-protected domain carry it instead of bouncing off the challenge
// page. Because the jar is shared process-wide (URLSession.shared semantics)
// but scoped per-domain, injecting a cookie for a host only affects that
// host, across every source that talks to it.

// CookieJar returns the http.CookieJar backing this Interpreter's net/
// webview requests. It is nil only when the caller supplied an HTTPClient
// with no jar.
func (i *Interpreter) CookieJar() http.CookieJar {
	return i.cookieJar
}

// SetCookie adds a single name=value cookie scoped to u, applied to every
// path on u's host (Path="/").
func (i *Interpreter) SetCookie(u *url.URL, name, value string) {
	i.setCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
}

// SetCookieHeader parses a raw "a=1; b=2" Cookie header and injects each
// cookie into the jar for u, scoped to every path on u's host. This is the
// convenient entry point for pasting cookies copied from another tool.
func (i *Interpreter) SetCookieHeader(u *url.URL, header string) {
	if u == nil || i.cookieJar == nil || header == "" {
		return
	}
	req := &http.Request{Header: http.Header{"Cookie": {header}}}
	if cookies := req.Cookies(); len(cookies) > 0 {
		for _, c := range cookies {
			c.Path = "/"
		}
		i.cookieJar.SetCookies(u, cookies)
	}
}

// SetCookies injects the given cookies into the jar for u.
func (i *Interpreter) SetCookies(u *url.URL, cookies []*http.Cookie) {
	i.setCookies(u, cookies)
}

func (i *Interpreter) setCookies(u *url.URL, cookies []*http.Cookie) {
	if u == nil || i.cookieJar == nil || len(cookies) == 0 {
		return
	}
	i.cookieJar.SetCookies(u, cookies)
}

// Cookies returns the cookies the source's jar would send for u.
func (i *Interpreter) Cookies(u *url.URL) []*http.Cookie {
	if u == nil || i.cookieJar == nil {
		return nil
	}
	return i.cookieJar.Cookies(u)
}

// LoadCookiesFile reads a Netscape cookies.txt file and injects every
// non-expired cookie into this source's jar. It returns the number of cookies
// injected.
func (i *Interpreter) LoadCookiesFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return i.LoadNetscapeCookies(data)
}

// LoadNetscapeCookies parses Netscape cookies.txt bytes and injects every
// non-expired cookie into this source's jar. Cookie domain/subdomain/path/
// secure attributes are preserved (unlike SetCookieHeader, which only has the
// raw name/value pairs). It returns the number of cookies injected.
func (i *Interpreter) LoadNetscapeCookies(data []byte) (int, error) {
	if i.cookieJar == nil {
		return 0, nil
	}
	cookies, err := host.ParseNetscapeCookies(data)
	if err != nil {
		return 0, err
	}
	for _, c := range cookies {
		hostName := strings.TrimPrefix(c.Domain, ".")
		if hostName == "" {
			continue
		}
		u := &url.URL{Scheme: "https", Host: hostName, Path: "/"}
		i.cookieJar.SetCookies(u, []*http.Cookie{c})
	}
	return len(cookies), nil
}

// withPartialHandler temporarily installs fn as the env.send_partial_result
// callback for the duration of a single guest call, matching the Swift
// CallbackHandler's register/remove-per-call lifecycle. Since Interpreter
// serializes all guest calls with mu, exactly one such handler is ever
// active at a time.
func (i *Interpreter) withPartialHandler(fn func(data []byte), body func() error) error {
	i.env.OnPartialResult = fn
	defer func() { i.env.OnPartialResult = nil }()
	return body()
}

// --- Store passthroughs (Runner.store/remove) ---

func (i *Interpreter) StoreValue(v any) int32       { return i.store.Store(v) }
func (i *Interpreter) Fetch(descriptor int32) any   { return i.store.Fetch(descriptor) }
func (i *Interpreter) RemoveValue(descriptor int32) { i.store.Remove(descriptor) }

func (i *Interpreter) storeString(s string) int32 { return i.store.Store(s) }
func (i *Interpreter) storeBytes(b []byte) int32  { return i.store.Store(b) }

// --- Guest call protocol ---

// call invokes a guest export and decodes its result per the buffer
// convention documented in Interpreter.swift's handleResult: negative i32
// results are error codes, non-negative results are a pointer into guest
// linear memory holding a [length:u32][pad:u32][payload] buffer (or, when
// length==u32::MAX, an error message).
func (i *Interpreter) call(ctx context.Context, name string, args ...uint64) ([]byte, error) {
	fn := i.module.ExportedFunction(name)
	if fn == nil {
		return nil, models.ErrUnimplemented()
	}
	results, err := fn.Call(ctx, args...)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, models.ErrMissingResult()
	}
	return i.handleResult(ctx, api.DecodeI32(results[0]))
}

func errorForCode(result int32) error {
	switch result {
	case -2:
		return models.ErrUnimplemented()
	case -3:
		return models.ErrNetworkError()
	default:
		return models.ErrMissingResult()
	}
}

func (i *Interpreter) handleResult(ctx context.Context, result int32) ([]byte, error) {
	if result < 0 {
		return nil, errorForCode(result)
	}
	pointer := uint32(result)
	mem := i.module.Memory()
	length, ok := mem.ReadUint32Le(pointer)
	if !ok {
		return nil, models.ErrMissingResult()
	}
	if length == math.MaxUint32 {
		strLenRaw, ok := mem.ReadUint32Le(pointer + 8)
		if !ok {
			return nil, models.ErrMissingResult()
		}
		if strLenRaw < 12 {
			return nil, models.ErrMissingResult()
		}
		msgBytes, ok := mem.Read(pointer+12, strLenRaw-12)
		if !ok {
			return nil, models.ErrMissingResult()
		}
		message := string(msgBytes)
		_ = i.freeResult(ctx, result)
		return nil, models.ErrMessage(message)
	}
	if length < 8 {
		return nil, models.ErrMissingResult()
	}
	data, ok := mem.Read(pointer+8, length-8)
	if !ok {
		return nil, models.ErrMissingResult()
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	_ = i.freeResult(ctx, result)
	return cp, nil
}

func (i *Interpreter) freeResult(ctx context.Context, pointer int32) error {
	fn := i.module.ExportedFunction("free_result")
	if fn == nil {
		return nil
	}
	_, err := fn.Call(ctx, api.EncodeI32(pointer))
	return err
}
