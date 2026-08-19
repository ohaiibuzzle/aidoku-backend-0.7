package host

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"modernc.org/quickjs"
)

// This file builds a hand-rolled DOM binding over quickjs for the
// `webview_*` namespace: real challenge/page scripts expect property-style
// access (`el.textContent`, `img.src`) that Go-struct reflection can't give
// getter/setter semantics for. Unlike goja, modernc.org/quickjs's Go wrapper
// exposes no accessor-property API, no constructor-hook API, and no
// opaque-Go-value-on-object mechanism (verified against the actual
// quickjs.go source, not just its docs) — so instead of building each DOM
// object's properties by hand from Go, domPrelude defines plain ES classes
// (Element/Document/Image/XMLHttpRequest) once per VM, and every accessor in
// them calls back into one of the small, fixed RegisterHostFunc/RegisterFunc
// dispatchers below. It reuses the same *nethtml.Node tree and engine-
// agnostic helpers html.go/htmlnode.go already have (attrOf/setAttr/
// nodeText/elementChildren/...) — no duplicated HTML logic.
//
// Node identity is preserved: nodeRegistry hands each *nethtml.Node a stable
// handle, and Element.wrap (below) caches one JS wrapper per handle — so two
// references to "the same" element return the *same* wrapper object, making
// `===` true and per-node state (style, listeners) persist across
// references, as in a real browser. The handle/identity map is reset on each
// load (see webview.go's loadHTML).
type domBinder struct {
	// rules/request/print/schedule/cookies are live getters, not snapshots:
	// webview_set_rule_list, the HTTP client's cookie jar, and the current
	// page URL can all change between when these dispatchers are registered
	// (once, in ensureGlobals) and any later call, so each reads the owning
	// webviewContext's current state rather than a value captured at
	// registration time.
	rules func() *contentRuleList
	// request performs an arbitrary-methoded fetch with optional headers and
	// body, used by fetch()/XMLHttpRequest/Image (Image only ever GETs).
	request func(req webviewRequest) (status int, body []byte, err error)
	print   func(string)
	// pageURL returns the currently loaded page's URL (nil if none) so
	// fetch()/XMLHttpRequest can resolve relative URLs, like a real browser.
	pageURL func() *url.URL
	// schedule defers fn to run on the owning event loop after the current
	// synchronous script finishes, matching real async image loading
	// (loop.Run drains it before returning, so it still fires within the
	// same host-function call).
	schedule func(fn func(*quickjs.VM))
	cookies  func() cookieAccessor // nil if no cookie jar is available

	handles *nodeRegistry
	loop    *eventLoop
}

// nodeRegistry hands out stable integer handles for *nethtml.Node values:
// each node keeps the same handle for the life of the registry (so the
// JS-side wrapper cache — see Element.wrap in domPrelude — can key identity
// on it, which is what makes `el === el2` and per-node state persist across
// references). The JS-side prelude round-trips these handles through
// RegisterHostFunc calls in place of goja's Object.DefineAccessorProperty/
// exported-Go-value approach. Reset() is called on every load to drop a
// replaced document's handles.
type nodeRegistry struct {
	nodes  map[int]*nethtml.Node
	byNode map[*nethtml.Node]int
	next   int
}

func newNodeRegistry() *nodeRegistry {
	return &nodeRegistry{
		nodes:  make(map[int]*nethtml.Node),
		byNode: make(map[*nethtml.Node]int),
	}
}

// Reset drops all known nodes/next-id. Called when a webview loads a fresh
// document, whose nodes are all new identities.
func (r *nodeRegistry) Reset() {
	r.nodes = make(map[int]*nethtml.Node)
	r.byNode = make(map[*nethtml.Node]int)
	r.next = 0
}

// handle returns 0 (JS null on the other side of __elGetProp et al.) for a
// nil node, so callers can pass traversal results straight through without
// a separate nil check. For a live node it returns the node's existing
// handle, minting one only the first time it's seen.
func (r *nodeRegistry) handle(n *nethtml.Node) int {
	if n == nil {
		return 0
	}
	if h, ok := r.byNode[n]; ok {
		return h
	}
	r.next++
	r.nodes[r.next] = n
	r.byNode[n] = r.next
	return r.next
}

func (r *nodeRegistry) node(h int) (*nethtml.Node, bool) {
	if h == 0 {
		return nil, false
	}
	n, ok := r.nodes[h]
	return n, ok
}

// domPrelude defines the DOM surface as plain ES classes, evaluated once per
// VM before any user/page script. Every property/method here is a thin
// wrapper around a `__el*`/`__doc*` host dispatcher — see registerDOM below
// for the Go side of each one. Class-private fields (#h) are used instead of
// a public property so user scripts can't forge a handle; they're also
// readable across instances of the *same* class (Element.#handleOf), which
// is what lets appendChild/removeChild pull a handle out of another Element
// instance without going through Go.
const domPrelude = `
(function () {
  // One cached wrapper per handle, so two references to "the same" element
  // are the same JS object (=== is true) and per-node state (style,
  // listeners) persists across references. __resetNodeCache is called by
  // the host on every load, in lockstep with the Go-side registry reset.
  let __nodeCache = new Map();
  globalThis.__resetNodeCache = () => { __nodeCache = new Map(); };

  class ClassList {
    #h;
    constructor(h) { this.#h = h; }
    add(c) { __classListOp(this.#h, "add", c); }
    remove(c) { __classListOp(this.#h, "remove", c); }
    contains(c) { return __classListOp(this.#h, "contains", c); }
    toggle(c) { return __classListOp(this.#h, "toggle", c); }
  }

  class Element {
    #h;
    #listeners = new Map();
    // Plain object, not a Go-backed accessor: CSS property names are
    // arbitrary and real pages only ever read back what they themselves
    // set (never rendering-derived values), so a bare {} already gives
    // correct set/get behavior for style.whatever = v. Because this wrapper
    // is cached per handle (see __nodeCache), style survives re-wrapping.
    style = {};
    constructor(h) { this.#h = h; }
    static wrap(h) {
      if (h === null || h === undefined || h === 0) return null;
      if (__nodeCache.has(h)) return __nodeCache.get(h);
      const el = new Element(h);
      __nodeCache.set(h, el);
      return el;
    }
    static wrapAll(hs) { return (hs || []).map(Element.wrap); }
    static handleOf(el) { return el instanceof Element ? el.#h : 0; }

    get textContent() { return __elGetProp(this.#h, "textContent"); }
    set textContent(v) { __elSetProp(this.#h, "textContent", String(v)); }
    get innerText() { return __elGetProp(this.#h, "innerText"); }
    set innerText(v) { __elSetProp(this.#h, "innerText", String(v)); }
    get innerHTML() { return __elGetProp(this.#h, "innerHTML"); }
    set innerHTML(v) { __elSetProp(this.#h, "innerHTML", String(v)); }
    get outerHTML() { return __elGetProp(this.#h, "outerHTML"); }
    get id() { return __elGetProp(this.#h, "id"); }
    set id(v) { __elSetProp(this.#h, "id", String(v)); }
    get className() { return __elGetProp(this.#h, "className"); }
    set className(v) { __elSetProp(this.#h, "className", String(v)); }
    get tagName() { return __elGetProp(this.#h, "tagName"); }
    get nodeType() { return __elGetProp(this.#h, "nodeType"); }
    get value() { return __elGetProp(this.#h, "value"); }
    set value(v) { __elSetProp(this.#h, "value", String(v)); }
    get href() { return __elGetProp(this.#h, "href"); }
    set href(v) { __elSetProp(this.#h, "href", String(v)); }
    get parentElement() { return Element.wrap(__elGetProp(this.#h, "parentElement")); }
    get children() { return Element.wrapAll(__elGetProp(this.#h, "children")); }
    get nextElementSibling() { return Element.wrap(__elGetProp(this.#h, "nextElementSibling")); }
    get previousElementSibling() { return Element.wrap(__elGetProp(this.#h, "previousElementSibling")); }
    get classList() { return new ClassList(this.#h); }

    getAttribute(name) { return __elGetAttr(this.#h, name); }
    setAttribute(name, value) { __elSetAttr(this.#h, name, String(value)); }
    removeAttribute(name) { __elRemoveAttr(this.#h, name); }
    hasAttribute(name) { return __elHasAttr(this.#h, name); }
    appendChild(child) { __elAppendChild(this.#h, Element.handleOf(child)); return child; }
    removeChild(child) { __elRemoveChild(Element.handleOf(child)); return child; }
    remove() { __elRemoveChild(this.#h); }
    querySelector(sel) { return Element.wrap(__elQuery(this.#h, sel)); }
    querySelectorAll(sel) { return Element.wrapAll(__elQueryAll(this.#h, sel)); }
    addEventListener(type, fn) {
      if (typeof fn !== "function") return;
      if (!this.#listeners.has(type)) this.#listeners.set(type, []);
      this.#listeners.get(type).push(fn);
    }
    removeEventListener(type, fn) {
      const arr = this.#listeners.get(type);
      if (!arr) return;
      this.#listeners.set(type, arr.filter((f) => f !== fn));
    }
    // dispatchEvent runs every listener registered for ev.type bound to this
    // element, passing a synthetic event object (type/target/preventDefault).
    dispatchEvent(ev) {
      const type = ev && ev.type;
      const arr = this.#listeners.get(type);
      if (!arr) return true;
      const synthetic = Object.assign({}, ev, {
        type, target: this, currentTarget: this,
        preventDefault() { this.defaultPrevented = true; }
      });
      for (const fn of arr.slice()) fn.call(this, synthetic);
      return true;
    }
    click() { this.dispatchEvent({ type: "click" }); }
  }

  class Document {
    #root;
    constructor(h) { this.#root = h; }
    get title() { return __docTitleGet(this.#root); }
    set title(v) { __docTitleSet(this.#root, String(v)); }
    get cookie() { return __docCookieGet(); }
    set cookie(v) { __docCookieSet(String(v)); }
    get body() { return Element.wrap(__elQuery(this.#root, "body")); }
    get head() { return Element.wrap(__elQuery(this.#root, "head")); }
    querySelector(sel) { return Element.wrap(__elQuery(this.#root, sel)); }
    querySelectorAll(sel) { return Element.wrapAll(__elQueryAll(this.#root, sel)); }
    getElementById(id) { return Element.wrap(__elQuery(this.#root, "#" + id)); }
    getElementsByClassName(c) { return Element.wrapAll(__elQueryAll(this.#root, "." + c)); }
    getElementsByTagName(t) { return Element.wrapAll(__elQueryAll(this.#root, t)); }
    createElement(tag) { return Element.wrap(__newElement(tag)); }
  }

  class Image {
    onload = null;
    onerror = null;
    #src = "";
    get src() { return this.#src; }
    set src(v) {
      this.#src = v;
      __imgLoadCheck(v).then(
        () => { if (this.onload) this.onload(); },
        () => { if (this.onerror) this.onerror(); }
      );
    }
  }

  class XMLHttpRequest {
    onload = null;
    onerror = null;
    onreadystatechange = null;
    readyState = 0;
    status = 0;
    responseText = "";
    #method = "GET";
    #url = "";
    #headers = {};
    open(method, url) {
      this.#method = String(method).toUpperCase();
      this.#url = url;
      this.#headers = {};
      this.readyState = 1;
      if (this.onreadystatechange) this.onreadystatechange();
    }
    setRequestHeader(name, value) {
      this.#headers[String(name)] = String(value);
    }
    send(body) {
      __xhrSend(JSON.stringify({
        method: this.#method,
        url: this.#url,
        headers: this.#headers,
        body: body == null ? "" : String(body)
      })).then(
        (r) => {
          this.status = r.status;
          this.responseText = r.body;
          this.readyState = 4;
          if (this.onreadystatechange) this.onreadystatechange();
          if (this.status >= 200 && this.status < 400) { if (this.onload) this.onload(); }
          else { if (this.onerror) this.onerror(); }
        },
        () => {
          this.status = 0;
          this.readyState = 4;
          if (this.onreadystatechange) this.onreadystatechange();
          if (this.onerror) this.onerror();
        }
      );
    }
  }

  // Minimal Event for addEventListener/dispatchEvent; no bubbling or capture.
  class Event {
    constructor(type, opts) {
      this.type = String(type);
      this.defaultPrevented = false;
      if (opts && opts.cancelable != null) this.cancelable = opts.cancelable;
    }
    preventDefault() { this.defaultPrevented = true; }
  }

  // Stub: real canvas rendering is out of scope for this headless DOM (see
  // the file-level WebView doc comment), but some sources/pages merely
  // reference HTMLCanvasElement.prototype.toDataURL as a value (e.g. to
  // save off the "real" implementation before patching it for a
  // fingerprinting workaround) without ever calling it — that only needs
  // the global and the method to *exist*, not to produce real pixel data.
  class HTMLCanvasElement {
    toDataURL() { return "data:,"; }
  }

  globalThis.Element = Element;
  globalThis.Document = Document;
  globalThis.Image = Image;
  globalThis.XMLHttpRequest = XMLHttpRequest;
  globalThis.HTMLCanvasElement = HTMLCanvasElement;
  globalThis.Event = Event;
  globalThis.__wrapDocument = (h) => new Document(h);

  // Real page/challenge scripts call these; the host schedules ids on a timer
  // wheel and __fireTimer(id) runs the registered callback. The callbacks
  // live in JS-side Maps, so Go never holds a quickjs.Value across the wait.
  const __timeouts = new Map();
  const __intervals = new Map();
  globalThis.__fireTimer = (id) => {
    const t = __timeouts.get(id);
    if (t) { __timeouts.delete(id); t(); return; }
    const iv = __intervals.get(id);
    if (iv) { iv(); }
  };
  globalThis.setTimeout = (fn, ms) => {
    const id = __setTimeout(ms | 0);
    if (typeof fn === "function") __timeouts.set(id, fn);
    return id;
  };
  globalThis.setInterval = (fn, ms) => {
    const id = __setInterval(ms | 0);
    if (typeof fn === "function") __intervals.set(id, fn);
    return id;
  };
  globalThis.clearTimeout = (id) => { __timeouts.delete(id); __clearTimer(id); };
  globalThis.clearInterval = (id) => { __intervals.delete(id); __clearTimer(id); };

  globalThis.console = {
    log: (...a) => __print(a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ")),
    warn: (...a) => __print(a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ")),
    error: (...a) => __print(a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ")),
  };

  globalThis.fetch = (url, opts) => {
    opts = opts || {};
    const method = opts.method ? String(opts.method).toUpperCase() : "GET";
    return __fetchOp(JSON.stringify({
      method,
      url: String(url),
      headers: opts.headers || {},
      body: opts.body == null ? "" : String(opts.body),
    })).then((r) => ({
      status: r.status,
      ok: r.status >= 200 && r.status < 300,
      text: () => Promise.resolve(r.body),
      json: () => Promise.resolve(JSON.parse(r.body)),
    }));
  };
})();
`

// registerDOM installs domPrelude and every host dispatcher it calls into
// vm. Called once per VM (see ensureGlobals's globalsReady guard) — the
// dispatchers themselves read d's live getters on every call, so they stay
// correct across multiple loadHTML calls on the same VM.
func (d *domBinder) registerDOM(vm *quickjs.VM) error {
	if _, err := vm.Eval(domPrelude, quickjs.EvalGlobal); err != nil {
		return err
	}

	arg := func(args []any, i int) string {
		if i >= len(args) || args[i] == nil {
			return ""
		}
		s, _ := args[i].(string)
		return s
	}
	nodeArg := func(args []any, i int) (*nethtml.Node, bool) {
		if i >= len(args) {
			return nil, false
		}
		h, ok := args[i].(int)
		if !ok {
			return nil, false
		}
		return d.handles.node(h)
	}
	childHandles := func(nodes elementList) []int {
		hs := make([]int, len(nodes))
		for i, n := range nodes {
			hs[i] = d.handles.handle(n)
		}
		return hs
	}

	must := func(err error) error { return err }

	if err := must(vm.RegisterHostFunc("__elGetProp", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		switch arg(args, 1) {
		case "textContent":
			return nodeText(n, false), nil
		case "innerText":
			return nodeText(n, true), nil
		case "innerHTML":
			s, _ := innerHTML(n)
			return s, nil
		case "outerHTML":
			s, _ := renderNode(n)
			return s, nil
		case "id":
			v, _ := attrOf(n, "id")
			return v, nil
		case "className":
			return className(n), nil
		case "tagName":
			return strings.ToUpper(n.Data), nil
		case "nodeType":
			return nodeTypeNumber(n), nil
		case "value":
			v, _ := attrOf(n, "value")
			return v, nil
		case "href":
			v, _ := attrOf(n, "href")
			return v, nil
		case "parentElement":
			if n.Parent == nil || n.Parent.Type != nethtml.ElementNode {
				return nil, nil
			}
			return d.handles.handle(n.Parent), nil
		case "children":
			return childHandles(elementChildren(n)), nil
		case "nextElementSibling":
			return d.handles.handle(nextElementSibling(n)), nil
		case "previousElementSibling":
			return d.handles.handle(previousElementSibling(n)), nil
		}
		return nil, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__elSetProp", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		val := arg(args, 2)
		switch arg(args, 1) {
		case "textContent", "innerText":
			setText(n, val)
		case "innerHTML":
			_ = setInnerHTML(n, val)
		case "id":
			setAttr(n, "id", val)
		case "className":
			setAttr(n, "class", val)
		case "value":
			setAttr(n, "value", val)
		case "href":
			setAttr(n, "href", val)
		}
		return nil, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__elGetAttr", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		v, ok := attrOf(n, arg(args, 1))
		if !ok {
			return nil, nil
		}
		return v, nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__elSetAttr", func(args []any) (any, error) {
		if n, ok := nodeArg(args, 0); ok {
			setAttr(n, arg(args, 1), arg(args, 2))
		}
		return nil, nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__elRemoveAttr", func(args []any) (any, error) {
		if n, ok := nodeArg(args, 0); ok {
			removeAttr(n, arg(args, 1))
		}
		return nil, nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__elHasAttr", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return false, nil
		}
		_, has := attrOf(n, arg(args, 1))
		return has, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__elAppendChild", func(args []any) (any, error) {
		parent, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		child, ok := nodeArg(args, 1)
		if !ok {
			return nil, nil
		}
		if child.Parent != nil {
			child.Parent.RemoveChild(child)
		}
		parent.AppendChild(child)
		return nil, nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__elRemoveChild", func(args []any) (any, error) {
		if n, ok := nodeArg(args, 0); ok {
			removeNode(n)
		}
		return nil, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__elQuery", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		matches, err := queryNodes(n, arg(args, 1))
		if err != nil || len(matches) == 0 {
			return nil, nil
		}
		return d.handles.handle(matches[0]), nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__elQueryAll", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return []int{}, nil
		}
		matches, _ := queryNodes(n, arg(args, 1))
		return childHandles(matches), nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__classListOp", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		cls := arg(args, 2)
		switch arg(args, 1) {
		case "add":
			addClass(n, cls)
		case "remove":
			removeClass(n, cls)
		case "contains":
			return hasClass(n, cls), nil
		case "toggle":
			if hasClass(n, cls) {
				removeClass(n, cls)
			} else {
				addClass(n, cls)
			}
		}
		return nil, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__newElement", func(args []any) (any, error) {
		tag := arg(args, 0)
		if tag == "" {
			tag = "div"
		}
		return d.handles.handle(newBareElement(tag)), nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__docTitleGet", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return "", nil
		}
		matches, _ := queryNodes(n, "title")
		if len(matches) == 0 {
			return "", nil
		}
		return nodeText(matches[0], true), nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__docTitleSet", func(args []any) (any, error) {
		n, ok := nodeArg(args, 0)
		if !ok {
			return nil, nil
		}
		matches, _ := queryNodes(n, "title")
		if len(matches) > 0 {
			setText(matches[0], arg(args, 1))
		}
		return nil, nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__docCookieGet", func(args []any) (any, error) {
		c := d.cookies()
		if c == nil {
			return "", nil
		}
		return c.Get(), nil
	})); err != nil {
		return err
	}
	if err := must(vm.RegisterHostFunc("__docCookieSet", func(args []any) (any, error) {
		if c := d.cookies(); c != nil {
			c.Set(arg(args, 0))
		}
		return nil, nil
	})); err != nil {
		return err
	}

	if err := must(vm.RegisterHostFunc("__print", func(args []any) (any, error) {
		if d.print != nil {
			d.print(arg(args, 0))
		}
		return nil, nil
	})); err != nil {
		return err
	}

	// Promise-returning dispatchers must be registered via RegisterFunc with
	// a concrete `quickjs.Value` return type, not RegisterHostFunc: a
	// RegisterHostFunc's declared return type is always `any`, and this
	// library's Go->JS conversion only special-cases a native Value when
	// the *static* return type is Value — through `any` it falls into the
	// generic JSON-marshal path and the live promise reference is lost
	// (confirmed with a standalone reproduction against the real library
	// before writing this).
	if err := must(vm.RegisterFunc("__imgLoadCheck", func(src string) quickjs.Value {
		pc, err := vm.NewPromiseCapability()
		if err != nil {
			panic(err)
		}
		settle := func(vm2 *quickjs.VM) {
			defer pc.Free()
			if d.rules().Blocks(src, resourceTypeImage) || d.request == nil {
				pc.Reject.Call(quickjs.UndefinedValue, "blocked or unavailable")
				return
			}
			status, _, err := d.request(webviewRequest{Method: http.MethodGet, URL: src})
			if err != nil || status >= 400 {
				pc.Reject.Call(quickjs.UndefinedValue, "load failed")
				return
			}
			pc.Resolve.Call(quickjs.UndefinedValue, true)
		}
		if d.schedule != nil {
			d.schedule(settle)
		} else {
			settle(vm)
		}
		return pc.Promise.Dup()
	}, false)); err != nil {
		return err
	}

	if err := must(vm.RegisterFunc("__xhrSend", func(reqJSON string) quickjs.Value {
		r := parseRequestJSON(reqJSON)
		r.URL = resolveFromBase(d.pageURL, r.URL)
		pc, err := vm.NewPromiseCapability()
		if err != nil {
			panic(err)
		}
		settle := func(vm2 *quickjs.VM) {
			defer pc.Free()
			if d.rules().Blocks(r.URL, resourceTypeFetch) {
				pc.Reject.Call(quickjs.UndefinedValue, "blocked by content rule list: "+r.URL)
				return
			}
			status, body, err := d.request(r)
			if err != nil {
				pc.Reject.Call(quickjs.UndefinedValue, err.Error())
				return
			}
			obj, err := vm2.NewObjectValue()
			if err != nil {
				pc.Reject.Call(quickjs.UndefinedValue, err.Error())
				return
			}
			statusAtom, _ := vm2.NewAtom("status")
			bodyAtom, _ := vm2.NewAtom("body")
			_ = vm2.SetProperty(obj, statusAtom, status)
			_ = vm2.SetProperty(obj, bodyAtom, string(body))
			pc.Resolve.Call(quickjs.UndefinedValue, obj)
		}
		if d.schedule != nil {
			d.schedule(settle)
		} else {
			settle(vm)
		}
		return pc.Promise.Dup()
	}, false)); err != nil {
		return err
	}

	if err := must(vm.RegisterFunc("__fetchOp", func(reqJSON string) quickjs.Value {
		r := parseRequestJSON(reqJSON)
		r.URL = resolveFromBase(d.pageURL, r.URL)
		pc, err := vm.NewPromiseCapability()
		if err != nil {
			panic(err)
		}
		if d.rules().Blocks(r.URL, resourceTypeFetch) {
			pc.Reject.Call(quickjs.UndefinedValue, "blocked by content rule list: "+r.URL)
			promise := pc.Promise.Dup()
			pc.Free()
			return promise
		}
		status, body, err := d.request(r)
		if err != nil {
			pc.Reject.Call(quickjs.UndefinedValue, err.Error())
			promise := pc.Promise.Dup()
			pc.Free()
			return promise
		}
		obj, err := vm.NewObjectValue()
		if err == nil {
			statusAtom, _ := vm.NewAtom("status")
			bodyAtom, _ := vm.NewAtom("body")
			_ = vm.SetProperty(obj, statusAtom, status)
			_ = vm.SetProperty(obj, bodyAtom, string(body))
			pc.Resolve.Call(quickjs.UndefinedValue, obj)
		} else {
			pc.Reject.Call(quickjs.UndefinedValue, err.Error())
		}
		// The PromiseCapability must be freed here (unlike the scheduled
		// __imgLoadCheck/__xhrSend, there's no deferred settle closure to
		// do it), but the returned promise needs its own reference — free
		// the capability, hand back a Dup'd live promise.
		promise := pc.Promise.Dup()
		pc.Free()
		return promise
	}, false)); err != nil {
		return err
	}

	// Timers: Go only stores one-shot/repeating ids; the JS prelude keeps
	// the actual callbacks in its own Maps (__timeouts/__intervals) and
	// __fireTimer(id) runs them. So no quickjs.Value is ever held by Go, and
	// there's no native-value lifetime bookkeeping here.
	if err := must(vm.RegisterFunc("__setTimeout", func(ms int) int {
		return d.loop.AddTimer(time.Duration(ms)*time.Millisecond, 0, func(id int, vm *quickjs.VM) {
			_, _ = vm.Call("__fireTimer", id)
		})
	}, false)); err != nil {
		return err
	}
	if err := must(vm.RegisterFunc("__setInterval", func(ms int) int {
		return d.loop.AddTimer(time.Duration(ms)*time.Millisecond, time.Duration(ms)*time.Millisecond, func(id int, vm *quickjs.VM) {
			_, _ = vm.Call("__fireTimer", id)
		})
	}, false)); err != nil {
		return err
	}
	if err := must(vm.RegisterFunc("__clearTimer", func(id int) int {
		if d.loop != nil {
			d.loop.ClearTimer(id)
		}
		return 0
	}, false)); err != nil {
		return err
	}

	return nil
}

// parseRequestJSON decodes the JSON string that fetch()/XMLHttpRequest hand
// to __fetchOp/__xhrSend: {"method","url","headers":{name:value},"body"}.
func parseRequestJSON(s string) webviewRequest {
	var raw struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	_ = json.Unmarshal([]byte(s), &raw)
	var req webviewRequest
	req.Method = raw.Method
	req.URL = raw.URL
	req.Body = []byte(raw.Body)
	names := make([]string, 0, len(raw.Headers))
	for k := range raw.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		req.Headers = append(req.Headers, [2]string{k, raw.Headers[k]})
	}
	return req
}

// resolveFromBase resolves ref against the current page URL (if any) before
// a fetch()/XMLHttpRequest, matching how a browser resolves relative URLs.
func resolveFromBase(base func() *url.URL, ref string) string {
	if base == nil || base() == nil {
		return ref
	}
	return resolveURL(base(), ref)
}

func queryNodes(root *nethtml.Node, query string) (elementList, error) {
	sel, err := cascadia.ParseGroup(query)
	if err != nil {
		return nil, err
	}
	return cascadia.QueryAll(root, sel), nil
}

func nodeTypeNumber(n *nethtml.Node) int {
	switch n.Type {
	case nethtml.ElementNode:
		return 1
	case nethtml.TextNode:
		return 3
	case nethtml.CommentNode:
		return 8
	case nethtml.DocumentNode:
		return 9
	default:
		return 0
	}
}

func newBareElement(tag string) *nethtml.Node {
	a := atom.Lookup([]byte(strings.ToLower(tag)))
	return &nethtml.Node{Type: nethtml.ElementNode, Data: strings.ToLower(tag), DataAtom: a}
}

// cookieAccessor lets `document.cookie` read/write through the shared
// http.CookieJar for the page's current URL.
type cookieAccessor interface {
	Get() string
	Set(setCookieHeader string)
}
