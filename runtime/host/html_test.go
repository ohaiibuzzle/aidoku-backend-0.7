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

func TestSelectFromJsoupForms(t *testing.T) {
	doc := parseTestDoc(t, `<div id="a"><p>0</p><p>1</p><p>2</p></div>`)
	div := selectTestNodes(t, doc, "#a")
	tests := []struct {
		query string
		want  []string
	}{
		{"p:eq(1)", []string{"1"}},
		{"p:eq( 1 )", []string{"1"}},
		{"p:lt(2)", []string{"0", "1"}},
		{"p:lt(0)", nil},
		{"p:gt(0)", []string{"1", "2"}},
		{"div#a", []string{"012"}}, // the receiver matches itself
	}
	for _, tt := range tests {
		got, err := selectFrom(div, tt.query)
		if err != nil {
			t.Fatalf("%q: %v", tt.query, err)
		}
		var texts []string
		for _, n := range got {
			texts = append(texts, domText(n, true))
		}
		if strings.Join(texts, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%q = %v, want %v", tt.query, texts, tt.want)
		}
	}
	if _, err := selectFrom(div, "p:nope(1)"); err == nil {
		t.Error("unknown pseudo must still be rejected")
	}
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

	if got := domText(entries[0], true); got != "Title One" {
		t.Fatalf("trimmed text: got %q", got)
	}
	if got := domText(entries[0], false); got != "  Title  One  " {
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
	if next := nextElementSibling(second); next == nil || domText(next, true) != "3" {
		t.Fatalf("nextElementSibling mismatch")
	}
	if prev := previousElementSibling(second); prev == nil || domText(prev, true) != "1" {
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
	if got := domText(div, true); got != "new text" {
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
	if got := domText(doc, true); strings.Contains(got, "var x") {
		t.Fatalf("domText should exclude script content, got %q", got)
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
	if v := resolveAbsAttr(h, a, "abs:href"); v != "https://weebcentral.com/series/abc/X" {
		t.Fatalf("abs:href: got %q", v)
	}
	img := selectTestNodes(t, doc, "#c")[0]
	if v := resolveAbsAttr(h, img, "abs:src"); v != "https://weebcentral.com/cover/x.jpg" {
		t.Fatalf("abs:src: got %q", v)
	}
	// plain attr is unaffected.
	if v := resolveAbsAttr(h, a, "href"); v != "/series/abc/X" {
		t.Fatalf("href: got %q", v)
	}
}

// SwiftSoup's whitespace is narrower than Go's unicode.IsSpace.
func TestTextKeepsNonHTMLSpaces(t *testing.T) {
	for _, c := range []struct{ query, src, want string }{
		{"p", "<p>ワン\u3000ピース</p>", "ワン\u3000ピース"},
		{"p", "<p>\u3000Foo\u3000</p>", "\u3000Foo\u3000"},
		{"p", "<p> a&nbsp;\n b </p>", "a b"},
		{"title", "<title>\u00a0X\u00a0</title>", "\u00a0X\u00a0"},
	} {
		h := NewHtml(NewStore())
		d, err := h.ParseHTML([]byte(c.src), "")
		if err != nil {
			t.Fatal(err)
		}
		el := selectTestNodes(t, h.Store.Fetch(d).(*nethtml.Node), c.query)[0]
		if got := elementText(el, true); got != c.want {
			t.Errorf("text %q: got %q, want %q", c.src, got, c.want)
		}
		if got := ownText(el); got != c.want {
			t.Errorf("ownText %q: got %q, want %q", c.src, got, c.want)
		}
	}
}

// Whitespace-preserving tags still get SwiftSoup's trim.
func TestElementTextTrimsPreservedTags(t *testing.T) {
	for _, c := range []struct{ query, src, want string }{
		{"title", "<title>\n  Foo - Site\n</title>", "Foo - Site"},
		{"title", "<title>Foo\n</title>", "Foo"},
		{"pre", "<pre> a\n</pre>", "a"},
		{"pre", "<pre><b>a</b>b\n</pre>", "ab\n"},
	} {
		h := NewHtml(NewStore())
		d, err := h.ParseHTML([]byte(c.src), "")
		if err != nil {
			t.Fatal(err)
		}
		doc := h.Store.Fetch(d).(*nethtml.Node)
		if got := elementText(selectTestNodes(t, doc, c.query)[0], true); got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}

// SwiftSoup parity: text/html/attr on lists and nodes, noscript, br/block spacing.
func TestHtmlSwiftSoupParity(t *testing.T) {
	h := NewHtml(NewStore())
	const src = `<html><body><div id="a">x<br>y</div><div id="b">  z  </div>
		<p id="c">1</p><p>2</p><a id="l" href="/p" data-e="">l</a><a href="http://o/q">m</a>
		<noscript><i id="ns">hid</i></noscript><pre id="p">  a  b </pre><div id="o">t <b>u</b><br>v</div></body></html>`
	d, err := h.ParseHTML([]byte(src), "")
	if err != nil {
		t.Fatal(err)
	}
	doc := h.Store.Fetch(d).(*nethtml.Node)
	sel := func(q string) elementList { return selectTestNodes(t, doc, q) }

	if got := elementText(sel("#a")[0], true); got != "x y" {
		t.Errorf("br text: %q", got)
	}
	if got := elementText(sel("#a")[0], false); got != "x y" {
		t.Errorf("br untrimmed text: %q", got)
	}
	if got := elementText(sel("#b")[0], false); got != "  z  " {
		t.Errorf("untrimmed: %q", got)
	}
	if got := elementText(sel("#p")[0], true); got != "a  b" {
		t.Errorf("pre text: %q", got)
	}
	if got := ownText(sel("#o")[0]); got != "t v" {
		t.Errorf("ownText: %q", got)
	}
	if len(sel("#ns")) != 1 {
		t.Errorf("noscript content should be parsed as elements")
	}

	str := func(desc int32) string { return h.Store.Fetch(desc).(string) }
	ps := h.Store.Store(sel("p"))
	if got := str(h.textImpl(ps, true)); got != "1 2" {
		t.Errorf("list text: %q", got)
	}
	empty := h.Store.Store(elementList{})
	if got := str(h.textImpl(empty, true)); got != "" {
		t.Errorf("empty list text: %q", got)
	}
	if got := str(h.storeHTML(h.Store.Store(elementList{}), renderNode)); got != "" {
		t.Errorf("empty list html: %q", got)
	}
	if got := str(h.storeHTML(h.Store.Store(sel("p")), renderNode)); got != "<p id=\"c\">1</p>\n<p>2</p>" {
		t.Errorf("list outer_html: %q", got)
	}

	as := sel("a")
	if got := resolveAbsAttr(h, as[0], "data-missing"); got != "" {
		t.Errorf("missing attr: %q", got)
	}
	// no base: relative abs: is "", already-absolute survives.
	if got := resolveAbsAttr(h, as[0], "abs:href"); got != "" {
		t.Errorf("abs relative, no base: %q", got)
	}
	if got := resolveAbsAttr(h, as[1], "abs:href"); got != "http://o/q" {
		t.Errorf("abs absolute, no base: %q", got)
	}
}

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
