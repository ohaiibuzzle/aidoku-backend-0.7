package host

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

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
