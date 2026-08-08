package host

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
			if _, ok := w.Store.Fetch(descriptor).(*webviewContext); !ok {
				return int32(jsInvalidHandler)
			}
			// Loading already ran synchronously to completion inside
			// webview_load/webview_load_html (including draining the
			// event loop), unlike Swift's async WKNavigationDelegate
			// dance — so there's nothing left to wait for.
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

	wv.loop.Run(func(vm *quickjs.VM) {
		wv.ensureGlobals(vm)
		wv.bindDocument(vm)

		for _, script := range wv.userScriptsStart {
			wv.runScript(vm, script)
		}

		for _, scriptNode := range findScriptNodes(root) {
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
				// descending into <script>/<style> content when called on
				// some *other* element that merely contains one, which
				// would make this always see an empty string when called
				// directly on the script node itself.
				wv.runScript(vm, text)
			}
		}

		for _, script := range wv.userScriptsEnd {
			wv.runScript(vm, script)
		}
	})
}

func (wv *webviewContext) runScript(vm *quickjs.VM, script string) {
	// EvalValue, not Eval: page/user scripts often end in an expression
	// whose value nobody uses (e.g. a bare DOM call), and Eval's eager
	// JSON-based any-conversion would misreport a circular completion value
	// (window, document, ...) as "JS Error" even though the script ran
	// fine. The result itself is discarded either way.
	v, err := vm.EvalValue(script, quickjs.EvalGlobal)
	if err == nil {
		v.Free()
	}
	if err != nil && wv.printHandler != nil {
		wv.printHandler(fmt.Sprintf("JS Error: %v", err))
	}
}

func (wv *webviewContext) fetchBody(rawURL string) (int, []byte, error) {
	return wv.doRequest(http.MethodGet, rawURL)
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

	navAtom, _ := vm.NewAtom("navigator")
	_ = vm.SetProperty(global, navAtom, map[string]any{
		"userAgent": "Mozilla/5.0 (X11; U; Linux armv71 like Android; en-us) AppleWebKit/531.2+ (KHTML; like Gecko) Version/5.0 Safari/533.2+ Kindle/3.0+",
	})
}

// newDOMBinder builds a domBinder wired to this webview's live state. The
// rule-list/cookie getters read wv's fields at call time rather than a
// snapshot, since webview_set_rule_list can be called after the globals are
// already built, and the cookie jar/page URL change on every load.
func (wv *webviewContext) newDOMBinder() *domBinder {
	return &domBinder{
		rules:   func() *contentRuleList { return wv.ruleList },
		fetch:   wv.fetchBody,
		request: wv.doRequest,
		print:   wv.printHandler,
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
	}
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

// doRequest performs a simple GET/HEAD/POST-with-no-body request. Custom
// headers/request bodies aren't modeled — a deliberate simplification:
// challenge-verification requests are typically simple GETs, and a full
// NetRequest-equivalent pipeline here would duplicate net.go for uncertain
// benefit at this tier.
func (wv *webviewContext) doRequest(method, rawURL string) (int, []byte, error) {
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := wv.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// eventLoop hand-rolls the piece goja_nodejs/eventloop gave us for free:
// this sandbox only ever schedules zero-delay continuations (Image/XHR/
// fetch "async" completions, all resolved synchronously against the local
// HTTP client), so there's no real timer wheel — just a FIFO of deferred
// callbacks drained alongside the VM's promise/job queue. Run blocks until
// the closure *and* every pending job/timer it (transitively) spawns have
// finished, matching goja_nodejs's Loop.Run contract.
type eventLoop struct {
	vm      *quickjs.VM
	pending []func(*quickjs.VM)
}

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

func (l *eventLoop) SetTimeout(fn func(*quickjs.VM)) {
	l.pending = append(l.pending, fn)
}

func (l *eventLoop) Run(fn func(*quickjs.VM)) {
	fn(l.vm)
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
