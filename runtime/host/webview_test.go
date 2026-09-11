package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"modernc.org/quickjs"
)

// These port AidokuRunnerTests.swift's testJavascript scenarios: sync/async
// eval, rule-list image blocking (onload vs onerror), and user-script
// injection at document-start. The original hits a real external URL
// (https://aidoku.app/images/aidoku.svg); this uses a local httptest
// server instead so the test is hermetic and fast while still exercising
// the same real-fetch-plus-rule-list-check code path.

func newTestWebView(t *testing.T) *webviewContext {
	t.Helper()
	loop, err := newEventLoop()
	if err != nil {
		t.Fatalf("newEventLoop: %v", err)
	}
	return &webviewContext{
		loop:         loop,
		client:       http.DefaultClient,
		printHandler: func(s string) { t.Log("[print]", s) },
		handles:      newNodeRegistry(),
	}
}

// globalString reads a global variable out of vm and renders it the same
// way context_get/webview_eval do.
func globalString(t *testing.T, vm *quickjs.VM, name string) string {
	t.Helper()
	atom, err := vm.NewAtom(name)
	if err != nil {
		t.Fatalf("NewAtom(%q): %v", name, err)
	}
	global := vm.GlobalObject()
	defer global.Free()
	v, err := vm.GetProperty(global, atom)
	if err != nil {
		t.Fatalf("GetProperty(%q): %v", name, err)
	}
	return stringify(v)
}

func TestWebViewEvalSync(t *testing.T) {
	wv := newTestWebView(t)
	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		wv.ensureGlobals(vm)
		v, err := vm.Eval("1+1", quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "2" {
		t.Fatalf("expected \"2\", got %q", result)
	}
}

func TestWebViewEvalAsync(t *testing.T) {
	wv := newTestWebView(t)
	var result string
	var evalErr error
	wv.loop.Run(func(vm *quickjs.VM) {
		wv.ensureGlobals(vm)
		result, evalErr = evalAsync(vm, `new Promise((resolve) => { resolve("async test"); })`)
	})
	if evalErr != nil {
		t.Fatalf("evalAsync: %v", evalErr)
	}
	if result != "async test" {
		t.Fatalf("expected \"async test\", got %q", result)
	}
}

func TestWebViewImageRuleListBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<svg></svg>"))
	}))
	defer srv.Close()

	imageURL := srv.URL + "/images/aidoku.svg"
	pageURL, _ := url.Parse(srv.URL + "/")

	html := `<!doctype html><html><body>
		<script>
			window.blockTestResult = "pending";
			var img = new Image();
			img.onload = function() { window.blockTestResult = "loaded"; };
			img.onerror = function() { window.blockTestResult = "blocked-or-failed"; };
			img.src = "` + imageURL + `";
			document.body.appendChild(img);
		</script>
	</body></html>`

	// Without a rule list: the image should load successfully.
	wv := newTestWebView(t)
	wv.pageURL = pageURL
	wv.loadHTML(html, pageURL)

	result := ""
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "blockTestResult")
	})
	if result != "loaded" {
		t.Fatalf("expected \"loaded\" with no rule list, got %q", result)
	}

	// With a rule list blocking that URL: onerror should fire instead.
	wv2 := newTestWebView(t)
	rules, err := parseContentRuleList(`[
		{"trigger": {"url-filter": ".*aidoku\\.svg.*", "resource-type": ["image"]}, "action": {"type": "block"}}
	]`)
	if err != nil {
		t.Fatalf("parseContentRuleList: %v", err)
	}
	wv2.ruleList = rules
	wv2.pageURL = pageURL
	wv2.loadHTML(html, pageURL)

	wv2.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "blockTestResult")
	})
	if result != "blocked-or-failed" {
		t.Fatalf("expected \"blocked-or-failed\" with rule list, got %q", result)
	}
}

func TestWebViewUserScriptInjection(t *testing.T) {
	wv := newTestWebView(t)
	wv.userScriptsStart = append(wv.userScriptsStart, `window.userScriptTestResult = 'injected';`)

	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	result := ""
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "userScriptTestResult")
	})
	if result != "injected" {
		t.Fatalf("expected \"injected\", got %q", result)
	}
}

func TestWebViewDOMTextContent(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body><div id="x">hello</div></body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`document.getElementById('x').textContent`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "hello" {
		t.Fatalf("expected \"hello\", got %q", result)
	}
}

// TestWebViewElementSrcAndHrefResolveAgainstPageURL guards against a
// descrambling source reading a plain, unresolved relative URL back from
// img.src/a.href -- real DOM .src/.href getters return the value resolved
// against the document's base URL, like html.go's "abs:"-prefixed attr()
// already does for the SwiftSoup-style API. getAttribute must still return
// the raw value, matching real DOM semantics.
func TestWebViewElementSrcAndHrefResolveAgainstPageURL(t *testing.T) {
	wv := newTestWebView(t)
	pageURL, err := url.Parse("https://example.com/manga/chapter-1/")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	wv.loadHTML(`<!doctype html><html><body>
		<img id="cover" src="../cover.jpg">
		<a id="next" href="page-2.html">next</a>
	</body></html>`, pageURL)

	cases := []struct {
		expr string
		want string
	}{
		{`document.getElementById('cover').src`, "https://example.com/manga/cover.jpg"},
		{`document.getElementById('next').href`, "https://example.com/manga/chapter-1/page-2.html"},
		{`document.getElementById('cover').getAttribute('src')`, "../cover.jpg"},
		{`document.getElementById('next').getAttribute('href')`, "page-2.html"},
	}
	for _, c := range cases {
		var result string
		wv.loop.Run(func(vm *quickjs.VM) {
			v, err := vm.Eval(c.expr, quickjs.EvalGlobal)
			if err != nil {
				t.Fatalf("Eval(%q): %v", c.expr, err)
			}
			result = stringify(v)
		})
		if result != c.want {
			t.Errorf("%s = %q, want %q", c.expr, result, c.want)
		}
	}
}

// TestWebViewElementSrcSetterStoresRawValue checks that assigning .src
// stores the raw value (like a real DOM setter), not a resolved one --
// resolution only happens on read.
func TestWebViewElementSrcSetterStoresRawValue(t *testing.T) {
	wv := newTestWebView(t)
	pageURL, err := url.Parse("https://example.com/manga/chapter-1/")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	wv.loadHTML(`<!doctype html><html><body><img id="cover"></body></html>`, pageURL)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`
			(() => {
				const el = document.getElementById('cover');
				el.src = "../cover2.jpg";
				return el.getAttribute('src') + "|" + el.src;
			})()
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if want := "../cover2.jpg|https://example.com/manga/cover2.jpg"; result != want {
		t.Fatalf("got %q, want %q", result, want)
	}
}

func TestWebViewQuerySelectorAllLiveElements(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body>
		<p class="item">a</p><p class="item">b</p><p class="item">c</p>
	</body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`
			Array.from(document.querySelectorAll('.item'))
				.map(el => el.textContent)
				.join(",")
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "a,b,c" {
		t.Fatalf("expected \"a,b,c\", got %q", result)
	}
}

func TestWebViewAppendChildMovesBetweenParents(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body>
		<div id="src"><span id="moveme">hi</span></div>
		<div id="dst"></div>
	</body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`
			const moveme = document.getElementById('moveme');
			const dst = document.getElementById('dst');
			dst.appendChild(moveme);
			const src = document.getElementById('src');
			(src.children.length === 0 ? "empty" : "not-empty") + "/" +
				dst.children.length + "/" + dst.children[0].textContent;
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "empty/1/hi" {
		t.Fatalf("expected \"empty/1/hi\", got %q", result)
	}
}

// A page script whose last statement evaluates to `window` (circular, since
// window === globalThis) or `document` must not be misreported as a script
// error — this is exactly the "window = globalThis" bug ensureGlobals hit
// during development, and a plausible thing for a real challenge script to
// end on (e.g. an IIFE returning `window` for later chaining).
func TestWebViewEvalCircularCompletionValue(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	for _, script := range []string{"window", "document", "window.document"} {
		var evalErr error
		wv.loop.Run(func(vm *quickjs.VM) {
			v, err := vm.EvalValue(script, quickjs.EvalGlobal)
			evalErr = err
			if err == nil {
				v.Free()
			}
		})
		if evalErr != nil {
			t.Fatalf("evaluating %q should not error, got: %v", script, evalErr)
		}
	}
}

// Regression coverage for the three comix.to gaps found by dumping its
// actual page scripts: a bare reference to HTMLCanvasElement.prototype
// (fingerprint-token capture), setting arbitrary style.* properties (the
// Cloudflare __CF$cv$params injector), and a <script type="application/json">
// hydration-data island that must never be executed as JS.
func TestWebViewHTMLCanvasElementStub(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`typeof HTMLCanvasElement.prototype.toDataURL`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "function" {
		t.Fatalf("expected HTMLCanvasElement.prototype.toDataURL to be a function, got %q", result)
	}
}

func TestWebViewElementStyleAcceptsArbitraryProperties(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`
			const el = document.createElement('iframe');
			el.style.position = 'absolute';
			el.style.top = 0;
			el.style.position + "/" + el.style.top;
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "absolute/0" {
		t.Fatalf("expected \"absolute/0\", got %q", result)
	}
}

func TestWebViewNonExecutableScriptTypesAreSkipped(t *testing.T) {
	wv := newTestWebView(t)
	html := `<!doctype html><html><body>
		<script type="application/json">{"page":"home","broken": [1,2,</script>
		<script>window.classicRan = true;</script>
	</body></html>`

	var loggedErrors []string
	wv.printHandler = func(s string) { loggedErrors = append(loggedErrors, s) }
	wv.loadHTML(html, nil)

	var classicRan string
	wv.loop.Run(func(vm *quickjs.VM) {
		classicRan = globalString(t, vm, "classicRan")
	})
	if classicRan != "true" {
		t.Fatalf("expected the classic <script> to run, got classicRan=%q", classicRan)
	}
	for _, msg := range loggedErrors {
		t.Errorf("expected no JS errors from a non-executable script type, got: %s", msg)
	}
}

// ES module support: an inline <script type="module"> runs directly under
// module grammar, and a src= module fetches through the same HTTP client/
// rule list as everything else, following relative imports (deduped and
// resolved against the importing module's URL) via registerModuleLoader.
func TestWebViewInlineModuleScript(t *testing.T) {
	wv := newTestWebView(t)
	html := `<!doctype html><html><body>
		<script type="module">
			const double = (x) => x * 2;
			globalThis.moduleResult = double(21);
		</script>
	</body></html>`

	var loggedErrors []string
	wv.printHandler = func(s string) { loggedErrors = append(loggedErrors, s) }
	wv.loadHTML(html, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "moduleResult")
	})
	if result != "42" {
		t.Fatalf("expected \"42\", got %q", result)
	}
	for _, msg := range loggedErrors {
		t.Errorf("expected no JS errors, got: %s", msg)
	}
}

func TestWebViewExternalModuleGraph(t *testing.T) {
	var helperHits, shoutHits int
	files := map[string]string{
		"/helper.js": `
			import {shout} from "./shout.js";
			export function greet(name) { return shout(name); }
		`,
		"/shout.js": `export function shout(s) { return s.toUpperCase(); }`,
		"/entry.js": `
			import {greet} from "./helper.js";
			import {greet as greetAgain} from "./helper.js";
			globalThis.entryResult = greet("world") + "/" + greetAgain("again");
		`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/helper.js":
			helperHits++
		case "/shout.js":
			shoutHits++
		}
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	pageURL, _ := url.Parse(srv.URL + "/")
	wv := newTestWebView(t)
	wv.pageURL = pageURL

	var loggedErrors []string
	wv.printHandler = func(s string) { loggedErrors = append(loggedErrors, s) }
	wv.loadHTML(`<!doctype html><html><body>
		<script type="module" src="/entry.js"></script>
	</body></html>`, pageURL)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "entryResult")
	})
	if result != "WORLD/AGAIN" {
		t.Fatalf("expected \"WORLD/AGAIN\", got %q", result)
	}
	// helper.js is imported twice (with different local bindings) but the
	// ES module spec evaluates each resolved specifier exactly once; the
	// engine's own module registry is what's responsible for that, not any
	// caching on our side.
	if helperHits != 1 {
		t.Fatalf("expected helper.js to be fetched exactly once, got %d", helperHits)
	}
	if shoutHits != 1 {
		t.Fatalf("expected shout.js to be fetched exactly once, got %d", shoutHits)
	}
	for _, msg := range loggedErrors {
		t.Errorf("expected no JS errors, got: %s", msg)
	}
}

func TestWebViewModuleRuleListBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`globalThis.moduleRan = true;`))
	}))
	defer srv.Close()

	pageURL, _ := url.Parse(srv.URL + "/")
	wv := newTestWebView(t)
	wv.pageURL = pageURL
	rules, err := parseContentRuleList(`[
		{"trigger": {"url-filter": ".*blocked\\.js.*", "resource-type": ["script"]}, "action": {"type": "block"}}
	]`)
	if err != nil {
		t.Fatalf("parseContentRuleList: %v", err)
	}
	wv.ruleList = rules

	var loggedErrors []string
	wv.printHandler = func(s string) { loggedErrors = append(loggedErrors, s) }
	wv.loadHTML(`<!doctype html><html><body>
		<script type="module" src="/blocked.js"></script>
	</body></html>`, pageURL)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "moduleRan")
	})
	if result == "true" {
		t.Fatalf("expected the rule-list-blocked module to never run")
	}
}

// Node identity: two references to "the same" element must be the same JS
// object (===), and per-node state set through one wrapper survives reading
// through another — the fix for the historical fresh-handle-per-traversal
// behavior.
func TestWebViewNodeIdentity(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body><div id="x" class="a">hi</div></body></html>`, nil)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.Eval(`
			const a = document.getElementById('x');
			const b = document.getElementById('x');
			const same = (a === b) ? "same" : "diff";
			a.style.color = "red";
			const stylePersists = (b.style.color === "red") ? "persist" : "lost";
			window.clicks = 0;
			const listener = () => window.clicks++;
			a.addEventListener('click', listener);
			b.click();
			same + "/" + stylePersists + "/" + window.clicks;
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		result = stringify(v)
	})
	if result != "same/persist/1" {
		t.Fatalf("expected \"same/persist/1\", got %q", result)
	}
}

// Real timers: a setTimeout scheduled inside a webview must actually fire
// (after the wall-clock delay) when webview_wait_for_load (RunUntilQuiescent)
// drains the loop — the previous zero-delay-only FIFO never would.
func TestWebViewSetTimeoutFires(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	start := time.Now()
	wv.loop.Run(func(vm *quickjs.VM) {
		_, err := vm.Eval(`globalThis.timerResult = 0; setTimeout(function(){ globalThis.timerResult = 42; }, 40);`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
	})
	wv.loop.RunUntilQuiescent(2 * time.Second)
	elapsed := time.Since(start)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "timerResult")
	})
	if result != "42" {
		t.Fatalf("expected timerResult to be 42, got %q", result)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("timer fired too early (elapsed %v): RunUntilQuiescent didn't actually wait", elapsed)
	}
}

// setInterval keeps firing until cleared.
func TestWebViewSetIntervalThenClear(t *testing.T) {
	wv := newTestWebView(t)
	wv.loadHTML(`<!doctype html><html><body></body></html>`, nil)

	wv.loop.Run(func(vm *quickjs.VM) {
		_, err := vm.Eval(`
			globalThis.count = 0;
			const id = setInterval(function(){
				globalThis.count++;
				if (globalThis.count === 3) clearInterval(id);
			}, 15);
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
	})
	wv.loop.RunUntilQuiescent(2 * time.Second)

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "count")
	})
	if result != "3" {
		t.Fatalf("expected count to be 3, got %q", result)
	}
}

// fetch() with a POST method, custom headers, and a body must reach the
// server intact.
func TestWebViewFetchPostHeadersBody(t *testing.T) {
	var gotMethod, gotHeader, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Test")
		gotBody = string(b)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	pageURL, _ := url.Parse(srv.URL + "/")
	wv := newTestWebView(t)
	wv.pageURL = pageURL
	wv.loadHTML(`<!doctype html><html><body></body></html>`, pageURL)

	wv.loop.Run(func(vm *quickjs.VM) {
		v, err := vm.EvalValue(`
			fetch('/echo', {
				method: 'POST',
				headers: { 'X-Test': 'hello' },
				body: 'payload'
			}).then(r => r.text()).then(t => { globalThis.fetchResult = t; });
		`, quickjs.EvalGlobal)
		if err != nil {
			t.Fatalf("Eval: %v", err)
		}
		v.Free()
	})

	var result string
	wv.loop.Run(func(vm *quickjs.VM) {
		result = globalString(t, vm, "fetchResult")
	})
	if result != "ok" {
		t.Fatalf("expected fetchResult \"ok\", got %q", result)
	}
	if gotMethod != "POST" || gotHeader != "hello" || gotBody != "payload" {
		t.Fatalf("unexpected request: method=%q header=%q body=%q", gotMethod, gotHeader, gotBody)
	}
}
