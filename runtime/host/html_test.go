package host

import (
	"strings"
	"testing"

	"github.com/andybalholm/cascadia"
	nethtml "golang.org/x/net/html"
)

func parseTestDoc(t *testing.T, src string) *nethtml.Node {
	t.Helper()
	n, err := nethtml.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return n
}

func selectTestNodes(t *testing.T, root *nethtml.Node, query string) elementList {
	t.Helper()
	sel, err := cascadia.ParseGroup(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	return cascadia.QueryAll(root, sel)
}

func TestHtmlSelectAndText(t *testing.T) {
	doc := parseTestDoc(t, `
		<html><body>
			<div class="manga-list">
				<a class="entry" href="/manga/1">  Title  One  </a>
				<a class="entry" href="/manga/2">Title Two</a>
			</div>
		</body></html>
	`)

	entries := selectTestNodes(t, doc, "a.entry")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if got := nodeText(entries[0], true); got != "Title One" {
		t.Fatalf("trimmed text: got %q", got)
	}
	if got := nodeText(entries[0], false); got != "  Title  One  " {
		t.Fatalf("untrimmed text should preserve exact whitespace, got %q", got)
	}

	href, ok := attrOf(entries[0], "href")
	if !ok || href != "/manga/1" {
		t.Fatalf("href attr: got %q ok=%v", href, ok)
	}
}

func TestHtmlAttrMutation(t *testing.T) {
	doc := parseTestDoc(t, `<html><body><div id="x" class="a b"></div></body></html>`)
	div := selectTestNodes(t, doc, "#x")[0]

	if !hasClass(div, "a") || !hasClass(div, "b") {
		t.Fatalf("expected classes a and b")
	}
	addClass(div, "c")
	if !hasClass(div, "c") {
		t.Fatalf("expected class c after addClass")
	}
	removeClass(div, "a")
	if hasClass(div, "a") {
		t.Fatalf("expected class a removed")
	}
	if got := className(div); got != "b c" {
		t.Fatalf("className: got %q", got)
	}

	setAttr(div, "data-id", "42")
	v, ok := attrOf(div, "data-id")
	if !ok || v != "42" {
		t.Fatalf("data-id attr: got %q ok=%v", v, ok)
	}
	removeAttr(div, "data-id")
	if _, ok := attrOf(div, "data-id"); ok {
		t.Fatalf("expected data-id removed")
	}
}

func TestHtmlChildrenAndSiblings(t *testing.T) {
	doc := parseTestDoc(t, `<html><body><ul id="list"><li>1</li><li>2</li><li>3</li></ul></body></html>`)
	ul := selectTestNodes(t, doc, "#list")[0]

	children := elementChildren(ul)
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}

	second := children[1]
	if next := nextElementSibling(second); next == nil || nodeText(next, true) != "3" {
		t.Fatalf("nextElementSibling mismatch")
	}
	if prev := previousElementSibling(second); prev == nil || nodeText(prev, true) != "1" {
		t.Fatalf("previousElementSibling mismatch")
	}

	sibs := siblingElements(second)
	if len(sibs) != 2 {
		t.Fatalf("expected 2 siblings excluding self, got %d", len(sibs))
	}
}

func TestHtmlSetTextAndHtml(t *testing.T) {
	doc := parseTestDoc(t, `<html><body><div id="x">old</div></body></html>`)
	div := selectTestNodes(t, doc, "#x")[0]

	setText(div, "new text")
	if got := nodeText(div, true); got != "new text" {
		t.Fatalf("setText: got %q", got)
	}

	if err := setInnerHTML(div, "<b>bold</b>"); err != nil {
		t.Fatalf("setInnerHTML: %v", err)
	}
	inner, err := innerHTML(div)
	if err != nil {
		t.Fatalf("innerHTML: %v", err)
	}
	if inner != "<b>bold</b>" {
		t.Fatalf("innerHTML: got %q", inner)
	}

	if err := appendHTML(div, "<i>ital</i>"); err != nil {
		t.Fatalf("appendHTML: %v", err)
	}
	inner, _ = innerHTML(div)
	if inner != "<b>bold</b><i>ital</i>" {
		t.Fatalf("after append: got %q", inner)
	}

	if err := prependHTML(div, "<u>under</u>"); err != nil {
		t.Fatalf("prependHTML: %v", err)
	}
	inner, _ = innerHTML(div)
	if inner != "<u>under</u><b>bold</b><i>ital</i>" {
		t.Fatalf("after prepend: got %q", inner)
	}
}

func TestHtmlDataScriptStyle(t *testing.T) {
	doc := parseTestDoc(t, `<html><head><script id="s">var x = 1;</script></head><body></body></html>`)
	script := selectTestNodes(t, doc, "#s")[0]
	data, ok := dataOf(script)
	if !ok || data != "var x = 1;" {
		t.Fatalf("script data: got %q ok=%v", data, ok)
	}
	// text() should exclude script contents.
	if got := nodeText(doc, true); strings.Contains(got, "var x") {
		t.Fatalf("nodeText should exclude script content, got %q", got)
	}
}

func TestBaseURIWalksAncestors(t *testing.T) {
	doc := parseTestDoc(t, `<html><body><div id="x"><a id="y">link</a></div></body></html>`)
	bases := newBaseURIs()
	bases.set(doc, "https://example.com/page")

	a := selectTestNodes(t, doc, "#y")[0]
	if got := bases.get(a); got != "https://example.com/page" {
		t.Fatalf("baseURI: got %q", got)
	}
}

// The `abs:` pseudo-attribute resolves a raw relative attribute against the
// document's recorded base URL (SwiftSoup/Aidoku behavior). Sources like
// WeebCentral rely on it for absolute cover/url fields; without it every
// entry is dropped.
func TestHtmlAbsAttr(t *testing.T) {
	doc := parseTestDoc(t, `<html><body><a id="l" href="/series/abc/X">X</a>
		<img id="c" src="/cover/x.jpg"></body></html>`)
	h := NewHtml(nil)
	h.bases.set(doc, "https://weebcentral.com/search?q=hi")

	a := selectTestNodes(t, doc, "#l")[0]
	if v, ok := resolveAbsAttr(h, a, "abs:href"); !ok || v != "https://weebcentral.com/series/abc/X" {
		t.Fatalf("abs:href: got %q ok=%v", v, ok)
	}
	img := selectTestNodes(t, doc, "#c")[0]
	if v, ok := resolveAbsAttr(h, img, "abs:src"); !ok || v != "https://weebcentral.com/cover/x.jpg" {
		t.Fatalf("abs:src: got %q ok=%v", v, ok)
	}
	// plain attr is unaffected.
	if v, ok := resolveAbsAttr(h, a, "href"); !ok || v != "/series/abc/X" {
		t.Fatalf("href: got %q ok=%v", v, ok)
	}
	// no base URL recorded => abs: fails.
	doc2 := parseTestDoc(t, `<html><body><a href="/x">x</a></body></html>`)
	a2 := selectTestNodes(t, doc2, "a")[0]
	if _, ok := resolveAbsAttr(h, a2, "abs:href"); ok {
		t.Fatalf("abs:href with no base should fail")
	}
}

// TestParseHTMLPrunesBaseURIOnRemove guards against baseURIs.roots leaking
// every parsed document for the life of the process: root staying as a map
// key there keeps the whole node tree reachable to the GC even after a
// guest calls std.destroy() on its descriptor.
func TestParseHTMLPrunesBaseURIOnRemove(t *testing.T) {
	store := NewStore()
	h := NewHtml(store)

	descriptor, err := h.ParseHTML([]byte(`<html><body><a href="/x">x</a></body></html>`), "https://example.com/page")
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	root, ok := store.Fetch(descriptor).(*nethtml.Node)
	if !ok {
		t.Fatalf("expected stored root, got %T", store.Fetch(descriptor))
	}
	if got := h.bases.get(root); got != "https://example.com/page" {
		t.Fatalf("base URI before removal: got %q", got)
	}

	store.Remove(descriptor)

	if n := len(h.bases.roots); n != 0 {
		t.Fatalf("baseURIs.roots not pruned after Remove: %d entries remain", n)
	}
	if got := h.bases.get(root); got != "" {
		t.Fatalf("base URI after removal: got %q, want empty", got)
	}
}

// TestParseHTMLWithNoBaseURLRegistersNoCleanup checks that a descriptor
// parsed without a base URL doesn't accumulate an OnRemove hook it doesn't
// need -- registerBase's early return on an empty baseURL must actually
// skip Store.OnRemove, not just h.bases.set.
func TestParseHTMLWithNoBaseURLRegistersNoCleanup(t *testing.T) {
	store := NewStore()
	h := NewHtml(store)

	descriptor, err := h.ParseHTML([]byte(`<html><body>x</body></html>`), "")
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	if n := len(store.cleanup[descriptor]); n != 0 {
		t.Fatalf("expected no cleanup hooks for a document with no base URL, got %d", n)
	}
}
