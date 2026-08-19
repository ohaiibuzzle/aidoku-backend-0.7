package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	nethtml "golang.org/x/net/html"
	"modernc.org/quickjs"
)

// WebView implements the `js` namespace's webview_* functions (the rest of
// `js` — context_create/eval/eval_async/get — lives in js.go). Each
// webview_create call gets its own *eventLoop (and therefore its own
// quickjs.VM), matching IsolatedJSContext's one-runtime-per-context model.
// Every operation runs via loop.Run(func(vm){...}), which blocks until the
// closure *and* any pending timers/microtasks drain — this is what gives
// setTimeout-driven challenge scripts a chance to finish before a host call
// returns, with no extra bridging beyond what already made
// context_eval_async's Promise handling work synchronously.
//
// See webviewdom.go for the DOM binding and webviewrules.go for the
// content-blocking rule list.
type WebView struct {
	Store        *Store
	Client       *http.Client
	PrintHandler func(string)
}

func (w *WebView) client() *http.Client {
	if w.Client != nil {
		return w.Client
	}
	return SharedHTTPClient()
}

type webviewContext struct {
	loop     *eventLoop
	ruleList *contentRuleList

	userScriptsStart []string
	userScriptsEnd   []string

	docRoot *nethtml.Node
	pageURL *url.URL

	globalsReady bool
	handles      *nodeRegistry

	client       *http.Client
	printHandler func(string)
}

// LinkWebView registers webview_* onto the same host module builder as
// js.go's context_* functions (they share the `js` wasm import namespace).
func LinkWebView(builder wazero.HostModuleBuilder, w *WebView) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context) int32 {
			loop, err := newEventLoop()
			if err != nil {
				return int32(jsInvalidHandler)
			}
			wv := &webviewContext{
				loop:         loop,
				client:       w.client(),
				printHandler: w.PrintHandler,
				handles:      newNodeRegistry(),
			}
			return w.Store.Store(wv)
		}).
		Export("webview_create")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			jsonStr, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			rules, err := parseContentRuleList(jsonStr)
			if err != nil {
				return int32(jsInvalidRuleList)
			}
			wv.ruleList = rules
			return int32(jsSuccess)
		}).
		Export("webview_set_rule_list")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor, requestDescriptor int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			req, ok := w.Store.Fetch(requestDescriptor).(*NetRequest)
			if !ok {
				return int32(jsInvalidRequest)
			}
			httpReq, err := req.ToHTTPRequest(ctx)
			if err != nil || httpReq == nil {
				return int32(jsInvalidRequest)
			}
			resp, err := wv.client.Do(httpReq)
			if err != nil {
				return int32(jsInvalidRequest)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return int32(jsInvalidRequest)
			}
			wv.loadHTML(string(body), resp.Request.URL)
			return int32(jsSuccess)
		}).
		Export("webview_load")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length, urlStringPointer, urlLength int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			htmlStr, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			var pageURL *url.URL
			if urlLength > 0 && urlStringPointer >= 0 {
				if s, ok := readMemString(m, urlStringPointer, urlLength); ok {
					pageURL, _ = url.Parse(s)
				}
			}
			wv.loadHTML(htmlStr, pageURL)
			return int32(jsSuccess)
		}).
		Export("webview_load_html")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			// Unlike Swift's async WKNavigationDelegate dance, webview_load/
			// webview_load_html already ran page scripts synchronously to
			// completion. What's left is whatever the page scheduled on the
			// event loop (setTimeout/setInterval continuations), so this
			// blocks (up to loadQuiesceTimeout) until those drain — that's
			// what lets a timed challenge script finish before a later
			// webview_eval reads its result.
			wv.loop.RunUntilQuiescent(loadQuiesceTimeout)
			return int32(jsSuccess)
		}).
		Export("webview_wait_for_load")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			script, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			var result string
			var evalErr error
			wv.loop.Run(func(vm *quickjs.VM) {
				wv.ensureGlobals(vm)
				// EvalValue, not Eval — see context_eval's comment in
				// js.go: Eval's any-conversion eagerly JSON-serializes an
				// object completion value and throws on a circular one
				// (e.g. a challenge script ending in `window`/`document`),
				// even though the script itself ran fine.
				v, err := vm.EvalValue(script, quickjs.EvalGlobal)
				if err != nil {
					evalErr = err
					return
				}
				defer v.Free()
				if v.IsUndefined() {
					return
				}
				result = safeStringify(v)
			})
			if evalErr != nil {
				return int32(jsMissingResult)
			}
			return w.Store.Store(result)
		}).
		Export("webview_eval")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, stringPointer, length int32) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			script, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			var result string
			var evalErr error
			wv.loop.Run(func(vm *quickjs.VM) {
				wv.ensureGlobals(vm)
				result, evalErr = evalAsync(vm, script)
			})
			if evalErr != nil {
				return int32(jsMissingResult)
			}
			return w.Store.Store(result)
		}).
		Export("webview_eval_async")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(
			ctx context.Context, m api.Module,
			descriptor, stringPointer, length, atDocumentEnd, forMainFrameOnly int32,
		) int32 {
			wv, ok := w.Store.Fetch(descriptor).(*webviewContext)
			if !ok {
				return int32(jsInvalidHandler)
			}
			script, ok := readMemString(m, stringPointer, length)
			if !ok {
				return int32(jsInvalidString)
			}
			if atDocumentEnd != 0 {
				wv.userScriptsEnd = append(wv.userScriptsEnd, script)
			} else {
				wv.userScriptsStart = append(wv.userScriptsStart, script)
			}
			return int32(jsSuccess)
		}).
		Export("webview_add_user_script")

	return builder
}

// loadHTML parses html, binds a fresh DOM to it, and runs document-start
// user scripts, the page's own <script> tags (in document order, fetching
// external src= scripts through the same client + rule list), then
// document-end user scripts — all synchronously, within one loop.Run so any
// setTimeout-scheduled continuations fire before this returns.
func (wv *webviewContext) loadHTML(htmlStr string, pageURL *url.URL) {
	root, err := nethtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		root = newBareElement("html")
	}
	wv.docRoot = root
	wv.pageURL = pageURL
	// A new document has a whole new node tree — drop the old node->handle
	// identity map. The JS-side wrapper cache must be discarded in lockstep
	// (see resetNodeCache below), or stale wrappers with dead handles would
	// survive.
	wv.handles.Reset()

	wv.loop.Run(func(vm *quickjs.VM) {
		wv.ensureGlobals(vm)
		resetNodeCache(vm)
		wv.bindDocument(vm)

		for _, script := range wv.userScriptsStart {
			wv.runScript(vm, script)
		}

		for _, scriptNode := range findScriptNodes(root) {
			switch classifyScript(scriptNode) {
			case scriptKindClassic:
				src, hasSrc := attrOf(scriptNode, "src")
				if hasSrc && src != "" {
					resolved := resolveURL(wv.pageURL, src)
					if wv.ruleList.Blocks(resolved, resourceTypeScript) {
						continue
					}
					_, body, err := wv.fetchBody(resolved)
					if err != nil {
						continue
					}
					wv.runScript(vm, string(body))
				} else if text, ok := dataOf(scriptNode); ok {
					// dataOf, not nodeText: nodeText deliberately skips
					// descending into <script>/<style> content when called
					// on some *other* element that merely contains one,
					// which would make this always see an empty string
					// when called directly on the script node itself.
					wv.runScript(vm, text)
				}
			case scriptKindModule:
				wv.runModuleScript(vm, scriptNode)
			default:
				// A real browser only executes a <script> whose type is
				// absent or a recognized JS MIME type — everything else
				// (type="application/json" hydration-data islands,
				// type="text/x-*" client-template blobs, an unknown
				// value, ...) is inert markup a page reads back via
				// textContent, never runs.
			}
		}

		for _, script := range wv.userScriptsEnd {
			wv.runScript(vm, script)
		}
	})
}

func (wv *webviewContext) runScript(vm *quickjs.VM, script string) {
	wv.evalForSideEffect(vm, script, quickjs.EvalGlobal)
}

// runModuleScript handles a <script type="module">: an inline module is
// eval'd as-is (it *is* the module), while a src= module is turned into a
// side-effect-only `import "url";` — the loader/normalizer registered by
// registerModuleLoader (see ensureGlobals) does the rest, including
// fetching and resolving whatever it in turn imports.
func (wv *webviewContext) runModuleScript(vm *quickjs.VM, scriptNode *nethtml.Node) {
	if src, hasSrc := attrOf(scriptNode, "src"); hasSrc && src != "" {
		resolved := resolveURL(wv.pageURL, src)
		if wv.ruleList.Blocks(resolved, resourceTypeScript) {
			return
		}
		wv.evalForSideEffect(vm, fmt.Sprintf("import %q;", resolved), quickjs.EvalModule)
		return
	}
	if text, ok := dataOf(scriptNode); ok {
		wv.evalForSideEffect(vm, text, quickjs.EvalModule)
	}
}

// evalForSideEffect runs script for whatever it does to global state —
// nobody wants its completion value — and logs a non-nil error the same way
// for both classic and module scripts.
//
// EvalValue, not Eval: page/user scripts often end in an expression whose
// value nobody uses (e.g. a bare DOM call), and Eval's eager JSON-based
// any-conversion would misreport a circular completion value (window,
// document, ...) as "JS Error" even though the script ran fine.
func (wv *webviewContext) evalForSideEffect(vm *quickjs.VM, script string, flags int) {
	v, err := vm.EvalValue(script, flags)
	if err == nil {
		v.Free()
	}
	if err != nil && wv.printHandler != nil {
		wv.printHandler(fmt.Sprintf("JS Error: %v", err))
	}
}

func (wv *webviewContext) fetchBody(rawURL string) (int, []byte, error) {
	return wv.doRequest(webviewRequest{Method: http.MethodGet, URL: rawURL})
}

func findScriptNodes(root *nethtml.Node) []*nethtml.Node {
	var out []*nethtml.Node
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.Data == "script" {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// classicScriptTypes are the "type" attribute values (per the WHATWG HTML
// spec's JavaScript MIME type list, minus long-dead legacy aliases) a real
// browser treats as classic JS and executes with script (not module)
// grammar. Anything not in here and not "module" — "application/json",
// "text/x-handlebars-template", an unknown value, ... — is inert markup the
// browser never runs.
var classicScriptTypes = map[string]bool{
	"":                         true,
	"text/javascript":          true,
	"text/ecmascript":          true,
	"application/javascript":   true,
	"application/ecmascript":   true,
	"application/x-javascript": true,
	"text/x-javascript":        true,
}

type scriptKind int

const (
	scriptKindNone scriptKind = iota
	scriptKindClassic
	scriptKindModule
)

// classifyScript reports how, if at all, scriptNode should be run, based on
// its type attribute — matching how a real browser decides between classic-
// script grammar (EvalGlobal), module grammar (EvalModule), and not
// executing it at all.
func classifyScript(scriptNode *nethtml.Node) scriptKind {
	t, _ := attrOf(scriptNode, "type")
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "module":
		return scriptKindModule
	case classicScriptTypes[t]:
		return scriptKindClassic
	default:
		return scriptKindNone
	}
}

func resolveURL(base *url.URL, ref string) string {
	if base == nil {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(refURL).String()
}

// ensureGlobals sets up the DOM prelude/console/fetch/navigator once per
// webview (idempotent — safe to call on every load/eval). window is the
// runtime's actual global object, same as a real browser, so bare globals
// and `window.foo` refer to the same thing.
func (wv *webviewContext) ensureGlobals(vm *quickjs.VM) {
	if wv.globalsReady {
		return
	}
	wv.globalsReady = true

	// Set directly via the property API rather than eval'ing
	// `globalThis.window = globalThis`: that assignment's completion value
	// is the circular globalThis object itself, and Eval's any-conversion
	// eagerly JSON-serializes object results, which throws "circular
	// reference" on it.
	global := vm.GlobalObject()
	defer global.Free()
	windowAtom, atomErr := vm.NewAtom("window")
	if atomErr == nil {
		_ = vm.SetPropertyValue(global, windowAtom, global.Dup())
	}

	binder := wv.newDOMBinder()
	if err := binder.registerDOM(vm); err != nil {
		return
	}

	wv.registerModuleLoader(vm)

	navAtom, _ := vm.NewAtom("navigator")
	_ = vm.SetProperty(global, navAtom, map[string]any{
		"userAgent": "Mozilla/5.0 (iPad; CPU iPad OS 26_5_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.5.2 Mobile/15E148 Safari/605.1.15",
	})
}

// newDOMBinder builds a domBinder wired to this webview's live state. The
// rule-list/cookie getters read wv's fields at call time rather than a
// snapshot, since webview_set_rule_list can be called after the globals are
// already built, and the cookie jar/page URL change on every load.
func (wv *webviewContext) newDOMBinder() *domBinder {
	return &domBinder{
		rules:   func() *contentRuleList { return wv.ruleList },
		request: wv.doRequest,
		print:   wv.printHandler,
		pageURL: func() *url.URL { return wv.pageURL },
		schedule: func(fn func(*quickjs.VM)) {
			wv.loop.SetTimeout(fn)
		},
		cookies: func() cookieAccessor {
			if wv.client.Jar == nil || wv.pageURL == nil {
				return nil
			}
			return &jarCookieAccessor{jar: wv.client.Jar, pageURL: wv.pageURL}
		},
		handles: wv.handles,
		loop:    wv.loop,
	}
}

// registerModuleLoader wires quickjs's ES module loader/normalizer to wv's
// HTTP client and rule list, so `<script type="module">` (and whatever it
// transitively imports) fetches the same way `<script src=...>` already
// does. Registered once per VM, in ensureGlobals — the loader/normalizer
// closures read wv's fields live, same as everything else set up there, so
// they stay correct across reloads.
//
// The engine calls normalize(base, name) for every import specifier before
// loader(resolvedName): base is the *importing* module's already-normalized
// name, or the literal string "<eval>" for the top-level Eval(EvalModule)
// call itself. Resolving "<eval>" against wv.pageURL handles both shapes
// runModuleScript produces — an inline module's relative imports resolve
// against the page URL, and a src= module's entry specifier (already
// absolute, from resolveURL in runModuleScript) is unaffected by resolving
// it again against any base.
func (wv *webviewContext) registerModuleLoader(vm *quickjs.VM) {
	vm.SetModuleLoader(
		func(vm *quickjs.VM, moduleName string) (string, error) {
			if wv.ruleList.Blocks(moduleName, resourceTypeScript) {
				return "", fmt.Errorf("blocked by content rule list: %s", moduleName)
			}
			status, body, err := wv.doRequest(webviewRequest{Method: http.MethodGet, URL: moduleName})
			if err != nil {
				return "", err
			}
			if status < 200 || status >= 300 {
				return "", fmt.Errorf("module fetch failed: HTTP %d", status)
			}
			return string(body), nil
		},
		func(vm *quickjs.VM, base, name string) (string, error) {
			baseURL := wv.pageURL
			if base != "<eval>" {
				if u, err := url.Parse(base); err == nil {
					baseURL = u
				}
			}
			return resolveURL(baseURL, name), nil
		},
	)
}

// bindDocument (re)binds `document`/`window.document` and `window.location`
// to the currently loaded page — called on every load, since the DOM
// changes even though the rest of the globals persist across loads.
func (wv *webviewContext) bindDocument(vm *quickjs.VM) {
	root := wv.docRoot
	if root == nil {
		root = newBareElement("html")
	}
	rootHandle := wv.handles.handle(root)

	doc, err := vm.CallValue("__wrapDocument", rootHandle)
	if err == nil {
		global := vm.GlobalObject()
		docAtom, _ := vm.NewAtom("document")
		_ = vm.SetPropertyValue(global, docAtom, doc)
		global.Free()
	}

	location := map[string]any{"href": ""}
	if wv.pageURL != nil {
		location["href"] = wv.pageURL.String()
		location["hostname"] = wv.pageURL.Hostname()
		location["protocol"] = wv.pageURL.Scheme + ":"
		location["pathname"] = wv.pageURL.Path
	}
	global := vm.GlobalObject()
	locAtom, _ := vm.NewAtom("location")
	_ = vm.SetProperty(global, locAtom, location)
	global.Free()
}

type jarCookieAccessor struct {
	jar     http.CookieJar
	pageURL *url.URL
}

func (c *jarCookieAccessor) Get() string {
	cookies := c.jar.Cookies(c.pageURL)
	parts := make([]string, len(cookies))
	for i, ck := range cookies {
		parts[i] = ck.Name + "=" + ck.Value
	}
	return strings.Join(parts, "; ")
}

func (c *jarCookieAccessor) Set(raw string) {
	resp := http.Response{Header: http.Header{"Set-Cookie": {raw}}}
	if cookies := resp.Cookies(); len(cookies) > 0 {
		c.jar.SetCookies(c.pageURL, cookies)
	}
}

// webviewRequest is everything the DOM's fetch()/XMLHttpRequest/Image can
// send: a method (defaulting to GET), a URL, optional request headers, and
// an optional body. Redirect following is handled by net/http's default
// client behavior.
type webviewRequest struct {
	Method  string
	URL     string
	Headers [][2]string // each pair is (name, value), in order
	Body    []byte
}

// doRequest performs a webviewRequest. When r.Body is nil a GET is sent
// with no body; HEAD/POST/etc. use the given method and optional body.
func (wv *webviewContext) doRequest(r webviewRequest) (int, []byte, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequest(method, r.URL, body)
	if err != nil {
		return 0, nil, err
	}
	for _, h := range r.Headers {
		req.Header.Add(h[0], h[1])
	}
	resp, err := wv.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// eventLoop hand-rolls the piece goja_nodejs/eventloop gave us for free:
// a FIFO of zero-delay continuations (Image/XHR/fetch "async" completions,
// all resolved synchronously against the local HTTP client) drained
// alongside the VM's promise/job queue, plus — since this tier now also
// honors real page setTimeout/setInterval — a wall-clock timer wheel.
//
// Run executes a closure and drains only jobs + zero-delay continuations
// (matching real webview_load/evaluateJavaScript, which don't block on a
// page's timers); RunUntilQuiescent additionally sleeps to and fires due
// callbacks until nothing is scheduled (that's webview_wait_for_load). All
// of it runs on one goroutine holding the loop's VM, so no locking is
// needed; timers fire on the owning goroutine within a Run call.
type eventLoop struct {
	vm      *quickjs.VM
	pending []func(*quickjs.VM)

	timers    []*timer
	nextTimer int
}

// timer is a single scheduled callback: a one-shot setTimeout or a repeating
// setInterval.
type timer struct {
	id       int
	at       time.Time
	interval time.Duration // 0 means a one-shot timer
	fn       func(id int, vm *quickjs.VM)
}

// loadQuiesceTimeout bounds how long webview_wait_for_load will block
// waiting for a page's timers to drain before giving up. A challenge script
// that genuinely needs longer than this won't resolve in the headless
// sandbox regardless, so bailing out (leaving the result unset) is better
// than hanging the caller forever.
const loadQuiesceTimeout = 30 * time.Second

func newEventLoop() (*eventLoop, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, err
	}
	return &eventLoop{vm: vm}, nil
}

// Close releases the event loop's VM. Satisfies io.Closer so Store.Remove/
// Close (see store.go) can release it without knowing anything
// webview-specific.
func (l *eventLoop) Close() error {
	return l.vm.Close()
}

// Close satisfies io.Closer so Store.Remove/Close release wv's VM — see
// eventLoop.Close.
func (wv *webviewContext) Close() error {
	return wv.loop.Close()
}

// SetTimeout defers fn to run on the next drain, before any wall-clock
// timer — this is the "zero-delay continuation" slot Image/XHR/fetch use to
// settle their promises after the current synchronous script finishes.
func (l *eventLoop) SetTimeout(fn func(*quickjs.VM)) {
	l.pending = append(l.pending, fn)
}

// AddTimer schedules fn to fire after delay (repeating every interval, or
// once when interval is 0) on the loop's owning goroutine, returning its id
// for ClearTimer. fn receives its own id so the caller can route the firing
// back to JS without holding any quickjs.Value in Go (see __setTimeout in
// webviewdom.go).
func (l *eventLoop) AddTimer(delay, interval time.Duration, fn func(id int, vm *quickjs.VM)) int {
	l.nextTimer++
	l.timers = append(l.timers, &timer{
		id: l.nextTimer, at: time.Now().Add(delay), interval: interval, fn: fn,
	})
	return l.nextTimer
}

// ClearTimer cancels a previously AddTimer'd id (no-op if already fired/
// never scheduled).
func (l *eventLoop) ClearTimer(id int) {
	for i, t := range l.timers {
		if t.id == id {
			l.timers = append(l.timers[:i], l.timers[i+1:]...)
			return
		}
	}
}

// Run executes fn and then drains the VM's promise job queue and zero-delay
// continuations — but does NOT block on wall-clock timers. This matches the
// real webview_load / evaluateJavaScript contract: the load runs page
// scripts and settles microtasks, while a page's setTimeout is left for
// webview_wait_for_load to drain.
func (l *eventLoop) Run(fn func(*quickjs.VM)) {
	fn(l.vm)
	l.drainNow()
}

// drainNow processes pending jobs and zero-delay continuations until quiet.
func (l *eventLoop) drainNow() {
	for {
		_, _ = l.vm.ExecutePendingJobs()
		if len(l.pending) == 0 {
			return
		}
		next := l.pending[0]
		l.pending = l.pending[1:]
		next(l.vm)
	}
}

// RunUntilQuiescent keeps draining jobs, zero-delay continuations, and
// wall-clock timers — sleeping to the earliest due timer — until nothing is
// left scheduled or maxWait elapses.
func (l *eventLoop) RunUntilQuiescent(maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for {
		l.drainNow()
		if len(l.timers) == 0 || time.Now().After(deadline) {
			return
		}
		earliest := l.timers[0]
		for _, t := range l.timers[1:] {
			if t.at.Before(earliest.at) {
				earliest = t
			}
		}
		now := time.Now()
		if earliest.at.After(now) {
			if earliest.at.Sub(now) > time.Until(deadline) {
				return
			}
			time.Sleep(earliest.at.Sub(now))
		}
		due := l.timers[:0]
		var fire []*timer
		for _, t := range l.timers {
			if !t.at.After(time.Now()) {
				fire = append(fire, t)
			} else {
				due = append(due, t)
			}
		}
		l.timers = due
		for _, t := range fire {
			if t.interval > 0 {
				t.at = t.at.Add(t.interval)
				l.timers = append(l.timers, t)
			}
			t.fn(t.id, l.vm)
		}
	}
}

// resetNodeCache discards the JS-side handle->Element wrapper cache, called
// at the top of every loadHTML in lockstep with the Go-side nodeRegistry
// reset. Defined by domPrelude; missing until ensureGlobals runs (an error
// only before the prelude is installed, which we ignore).
func resetNodeCache(vm *quickjs.VM) {
	_, _ = vm.Call("__resetNodeCache")
}
