package host

import (
	"context"
	stdhtml "html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	nethtml "golang.org/x/net/html"
)

type htmlResult int32

const (
	htmlSuccess           htmlResult = 0
	htmlInvalidDescriptor htmlResult = -1
	htmlInvalidString     htmlResult = -2
	htmlInvalidHTML       htmlResult = -3
	htmlInvalidQuery      htmlResult = -4
	htmlNoResult          htmlResult = -5
	htmlEngineError       htmlResult = -6
)

type htmlKind int32

const (
	htmlKindUnknown     htmlKind = 0
	htmlKindNode        htmlKind = 1
	htmlKindTextNode    htmlKind = 2
	htmlKindDataNode    htmlKind = 3
	htmlKindComment     htmlKind = 4
	htmlKindElement     htmlKind = 5
	htmlKindElementList htmlKind = 6
	htmlKindDocument    htmlKind = 7
)

// Html implements the `html` namespace (Imports/Html.swift): a
// SwiftSoup/jsoup-equivalent DOM API over golang.org/x/net/html +
// cascadia, sharing the same Store as every other namespace so descriptors
// returned by net.html()/net.get_image() etc. interoperate.
type Html struct {
	Store *Store
	bases *baseURIs
}

func NewHtml(store *Store) *Html {
	return &Html{Store: store, bases: newBaseURIs()}
}

// ParseHTML implements the callback Net expects for its `html` binding
// (net.html(): parse a fetched response body as a document).
func (h *Html) ParseHTML(data []byte, baseURL string) (int32, error) {
	root, err := nethtml.ParseWithOptions(strings.NewReader(string(data)), nethtml.ParseOptionEnableScripting(false))
	if err != nil {
		return 0, err
	}
	descriptor := h.Store.Store(root)
	h.registerBase(descriptor, root, baseURL)
	return descriptor, nil
}

// registerBase records root's base URL (if any) and arranges for that
// entry to be pruned from h.bases when descriptor is later removed from
// the Store -- otherwise root would stay reachable to the GC forever as a
// baseURIs.roots map key. Every site that calls h.bases.set on a
// freshly-stored root must go through this instead, so none of them can
// reintroduce the leak by adding a bases.set without a matching cleanup.
func (h *Html) registerBase(descriptor int32, root *nethtml.Node, baseURL string) {
	if baseURL == "" {
		return
	}
	h.bases.set(root, baseURL)
	h.Store.OnRemove(descriptor, func() { h.bases.remove(root) })
}

func readMemString(m api.Module, offset, length int32) (string, bool) {
	if offset < 0 || length < 0 {
		return "", false
	}
	b, ok := m.Memory().Read(uint32(offset), uint32(length))
	if !ok {
		return "", false
	}
	return string(b), true
}

// LinkHtml registers the `html` namespace onto the given host module
// builder.
func LinkHtml(builder wazero.HostModuleBuilder, h *Html) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, htmlOff, htmlLen, baseOff, baseLen int32) int32 {
			htmlStr, ok := readMemString(m, htmlOff, htmlLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			root, err := nethtml.ParseWithOptions(strings.NewReader(htmlStr), nethtml.ParseOptionEnableScripting(false))
			if err != nil {
				return int32(htmlInvalidHTML)
			}
			descriptor := h.Store.Store(root)
			if baseLen > 0 {
				if baseURL, ok := readMemString(m, baseOff, baseLen); ok {
					h.registerBase(descriptor, root, baseURL)
				}
			}
			return descriptor
		}).
		Export("parse")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, htmlOff, htmlLen, baseOff, baseLen int32) int32 {
			htmlStr, ok := readMemString(m, htmlOff, htmlLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			nodes, err := parseFragmentNodes(htmlStr)
			if err != nil {
				return int32(htmlInvalidHTML)
			}
			root := &nethtml.Node{Type: nethtml.DocumentNode}
			for _, n := range nodes {
				root.AppendChild(n)
			}
			descriptor := h.Store.Store(root)
			if baseLen > 0 {
				if baseURL, ok := readMemString(m, baseOff, baseLen); ok {
					h.registerBase(descriptor, root, baseURL)
				}
			}
			return descriptor
		}).
		Export("parse_fragment")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, textOff, textLen int32) int32 {
			s, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			return h.Store.Store(stdhtml.EscapeString(s))
		}).
		Export("escape")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, textOff, textLen int32) int32 {
			s, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			return h.Store.Store(stdhtml.UnescapeString(s))
		}).
		Export("unescape")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return int32(kindOf(h.Store.Fetch(descriptor)))
		}).
		Export("kind")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			var all elementList
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				all = append(all, c)
			}
			return h.Store.Store(all)
		}).
		Export("child_nodes")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, attrOff, attrLen int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			attr, ok := readMemString(m, attrOff, attrLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if _, has := attrOf(n, attr); has {
				return 1
			}
			return 0
		}).
		Export("has_attr")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, attrOff, attrLen, valOff, valLen int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			attr, ok := readMemString(m, attrOff, attrLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			val, ok := readMemString(m, valOff, valLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if val == "" {
				removeAttr(n, attr)
			} else {
				setAttr(n, attr, val)
			}
			return int32(htmlSuccess)
		}).
		Export("set_attr")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, attrOff, attrLen int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			attr, ok := readMemString(m, attrOff, attrLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			removeAttr(n, attr)
			return int32(htmlSuccess)
		}).
		Export("remove_attr")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, textOff, textLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			text, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			setText(n, text)
			return int32(htmlSuccess)
		}).
		Export("set_text")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, textOff, textLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			text, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if err := setInnerHTML(n, text); err != nil {
				return int32(htmlEngineError)
			}
			return int32(htmlSuccess)
		}).
		Export("set_html")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, textOff, textLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			text, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if err := prependHTML(n, text); err != nil {
				return int32(htmlEngineError)
			}
			return int32(htmlSuccess)
		}).
		Export("prepend")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, textOff, textLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			text, ok := readMemString(m, textOff, textLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if err := appendHTML(n, text); err != nil {
				return int32(htmlEngineError)
			}
			return int32(htmlSuccess)
		}).
		Export("append")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(elementChildren(n))
		}).
		Export("children")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(h.bases.get(n))
		}).
		Export("base_uri")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(ownText(n))
		}).
		Export("own_text")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			data, ok := dataOf(n)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(data)
		}).
		Export("data")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			id, _ := attrOf(n, "id")
			return h.Store.Store(id)
		}).
		Export("id")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(n.Data)
		}).
		Export("tag_name")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(className(n))
		}).
		Export("class_name")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, classOff, classLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			class, ok := readMemString(m, classOff, classLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			if hasClass(n, class) {
				return 1
			}
			return 0
		}).
		Export("has_class")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, classOff, classLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			class, ok := readMemString(m, classOff, classLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			addClass(n, class)
			return int32(htmlSuccess)
		}).
		Export("add_class")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, classOff, classLen int32) int32 {
			n, ok := elementOf(h.Store, descriptor)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			class, ok := readMemString(m, classOff, classLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			removeClass(n, class)
			return int32(htmlSuccess)
		}).
		Export("remove_class")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			list, ok := h.Store.Fetch(descriptor).(elementList)
			if !ok || len(list) == 0 {
				if ok {
					return int32(htmlNoResult)
				}
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(list[0])
		}).
		Export("first")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			list, ok := h.Store.Fetch(descriptor).(elementList)
			if !ok || len(list) == 0 {
				if ok {
					return int32(htmlNoResult)
				}
				return int32(htmlInvalidDescriptor)
			}
			return h.Store.Store(list[len(list)-1])
		}).
		Export("last")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor, index int32) int32 {
			list, ok := h.Store.Fetch(descriptor).(elementList)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			if index < 0 || int(index) >= len(list) {
				return int32(htmlNoResult)
			}
			return h.Store.Store(list[index])
		}).
		Export("get")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			list, ok := h.Store.Fetch(descriptor).(elementList)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			return int32(len(list))
		}).
		Export("size")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			if n.Parent == nil {
				return int32(htmlNoResult)
			}
			return h.Store.Store(n.Parent)
		}).
		Export("parent")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			if n.Type == nethtml.ElementNode {
				return h.Store.Store(siblingElements(n))
			}
			return h.Store.Store(siblingNodes(n))
		}).
		Export("siblings")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			var next *nethtml.Node
			if n.Type == nethtml.ElementNode {
				next = nextElementSibling(n)
			} else {
				next = n.NextSibling
			}
			if next == nil {
				return int32(htmlNoResult)
			}
			return h.Store.Store(next)
		}).
		Export("next")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			n, ok := h.Store.Fetch(descriptor).(*nethtml.Node)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			var prev *nethtml.Node
			if n.Type == nethtml.ElementNode {
				prev = previousElementSibling(n)
			} else {
				prev = n.PrevSibling
			}
			if prev == nil {
				return int32(htmlNoResult)
			}
			return h.Store.Store(prev)
		}).
		Export("previous")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, keyOff, keyLen int32) int32 {
			item := h.Store.Fetch(descriptor)
			key, ok := readMemString(m, keyOff, keyLen)
			if !ok {
				return int32(htmlInvalidString)
			}
			nodes, ok := nodesOf(item)
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			// Elements.attr: first element that has the attribute, else "".
			for _, n := range nodes {
				val := resolveAbsAttr(h, n, key)
				if _, has := attrOf(n, key); has || val != "" {
					return h.Store.Store(val)
				}
			}
			return h.Store.Store("")
		}).
		Export("attr")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return h.storeHTML(descriptor, renderNode)
		}).
		Export("outer_html")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			nodes, ok := nodesOf(h.Store.Fetch(descriptor))
			if !ok {
				return int32(htmlInvalidDescriptor)
			}
			for _, n := range nodes {
				removeNode(n)
			}
			return int32(htmlSuccess)
		}).
		Export("remove")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, queryOff, queryLen int32) int32 {
			return h.selectImpl(m, descriptor, queryOff, queryLen)
		}).
		Export("select")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, descriptor, queryOff, queryLen int32) int32 {
			listDescriptor := h.selectImpl(m, descriptor, queryOff, queryLen)
			if listDescriptor < 0 {
				return listDescriptor
			}
			defer h.Store.Remove(listDescriptor)
			list, ok := h.Store.Fetch(listDescriptor).(elementList)
			if !ok || len(list) == 0 {
				return int32(htmlNoResult)
			}
			return h.Store.Store(list[0])
		}).
		Export("select_first")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return h.textImpl(descriptor, true)
		}).
		Export("text")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return h.textImpl(descriptor, false)
		}).
		Export("untrimmed_text")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			return h.storeHTML(descriptor, innerHTML)
		}).
		Export("html")

	return builder
}

func (h *Html) selectImpl(m api.Module, descriptor, queryOff, queryLen int32) int32 {
	item := h.Store.Fetch(descriptor)
	query, ok := readMemString(m, queryOff, queryLen)
	if !ok {
		return int32(htmlInvalidString)
	}
	roots, ok := nodesOf(item)
	if !ok {
		return int32(htmlInvalidDescriptor)
	}
	out, err := selectFrom(roots, query)
	if err != nil {
		return int32(htmlInvalidQuery)
	}
	return h.Store.Store(out)
}

// selectFrom matches like jsoup's Element.select: each root is itself a
// candidate, which cascadia.QueryAll alone would skip.
func selectFrom(roots []*nethtml.Node, query string) (elementList, error) {
	sel, err := cascadia.ParseGroup(rewriteIndexPseudos(query))
	if err != nil {
		return nil, err
	}
	var out elementList
	seen := make(map[*nethtml.Node]bool)
	add := func(n *nethtml.Node) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, root := range roots {
		if root.Type == nethtml.ElementNode && sel.Match(root) {
			add(root)
		}
		for _, n := range cascadia.QueryAll(root, sel) {
			add(n)
		}
	}
	return out, nil
}

var indexPseudo = regexp.MustCompile(`:(eq|lt|gt)\(\s*(\d+)\s*\)`)

// rewriteIndexPseudos maps jsoup's 0-based :eq/:lt/:gt onto :nth-child, which
// counts the same element siblings; cascadia rejects the jsoup forms.
// shortcut: also rewrites the text inside a quoted attribute value, upgrade to
// a tokenizer if a real selector ever needs that literal.
func rewriteIndexPseudos(query string) string {
	return indexPseudo.ReplaceAllStringFunc(query, func(m string) string {
		p := indexPseudo.FindStringSubmatch(m)
		n, err := strconv.Atoi(p[2])
		if err != nil {
			return m
		}
		switch p[1] {
		case "eq":
			return ":nth-child(" + strconv.Itoa(n+1) + ")"
		case "lt":
			return ":nth-child(-n+" + strconv.Itoa(n) + ")"
		default:
			return ":nth-child(n+" + strconv.Itoa(n+2) + ")"
		}
	})
}

// nodesOf returns a stored node or node list as a list; a lone node behaves
// as a list of one everywhere SwiftSoup's Node and Elements methods agree.
func nodesOf(item any) (elementList, bool) {
	switch v := item.(type) {
	case elementList:
		return v, true
	case *nethtml.Node:
		return elementList{v}, true
	}
	return nil, false
}

// joinNodes renders each node, with sep between results once anything has
// accumulated (Elements.text/html/outerHtml).
func joinNodes(nodes elementList, sep string, render func(*nethtml.Node) (string, error)) (string, error) {
	var sb strings.Builder
	for _, n := range nodes {
		s, err := render(n)
		if err != nil {
			return "", err
		}
		if sb.Len() > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(s)
	}
	return sb.String(), nil
}

// storeHTML implements html/outer_html for a node or list.
func (h *Html) storeHTML(descriptor int32, render func(*nethtml.Node) (string, error)) int32 {
	nodes, ok := nodesOf(h.Store.Fetch(descriptor))
	if !ok {
		return int32(htmlInvalidDescriptor)
	}
	s, err := joinNodes(nodes, "\n", render)
	if err != nil {
		return int32(htmlNoResult)
	}
	return h.Store.Store(s)
}

func (h *Html) textImpl(descriptor int32, trim bool) int32 {
	item := h.Store.Fetch(descriptor)
	switch v := item.(type) {
	case elementList:
		s, _ := joinNodes(v, " ", func(n *nethtml.Node) (string, error) { return elementText(n, trim), nil })
		return h.Store.Store(s)
	case *nethtml.Node:
		switch v.Type {
		case nethtml.TextNode:
			// TextNode.text() collapses whitespace but does not trim.
			if trim {
				return h.Store.Store(string(appendNormalised(nil, v.Data, false)))
			}
			return h.Store.Store(v.Data)
		case nethtml.ElementNode, nethtml.DocumentNode:
			return h.Store.Store(elementText(v, trim))
		}
		return int32(htmlNoResult)
	default:
		return int32(htmlInvalidDescriptor)
	}
}

func kindOf(item any) htmlKind {
	switch v := item.(type) {
	case elementList:
		return htmlKindElementList
	case *nethtml.Node:
		switch v.Type {
		case nethtml.DocumentNode:
			return htmlKindDocument
		case nethtml.ElementNode:
			return htmlKindElement
		case nethtml.TextNode:
			return htmlKindTextNode
		case nethtml.CommentNode:
			return htmlKindComment
		default:
			return htmlKindNode
		}
	default:
		return htmlKindUnknown
	}
}

// elementOf fetches descriptor expecting an ElementNode (the Swift bindings
// use `as? Element` for these; Document nodes don't qualify).
func elementOf(store *Store, descriptor int32) (*nethtml.Node, bool) {
	n, ok := store.Fetch(descriptor).(*nethtml.Node)
	if !ok || n.Type != nethtml.ElementNode {
		return nil, false
	}
	return n, true
}

// absAttrPrefix is the SwiftSoup/Aidoku "absolute URL" pseudo-attribute
// prefix. `.attr("abs:href")` returns the href resolved against the
// document's base URL — used heavily by sources for absolute URLs.
const absAttrPrefix = "abs:"

// resolveAbsAttr mirrors SwiftSoup's Node.attr: the attribute value, or ""
// when missing. An "abs:" key resolves the underlying attribute against the
// node's base URL; without a usable base only an already-absolute value
// survives (StringUtil.resolve).
func resolveAbsAttr(h *Html, n *nethtml.Node, key string) string {
	if v, ok := attrOf(n, key); ok && v != "" {
		return v
	}
	if len(key) < len(absAttrPrefix) || !strings.EqualFold(key[:len(absAttrPrefix)], absAttrPrefix) {
		return ""
	}
	v, ok := attrOf(n, key[len(absAttrPrefix):])
	if !ok {
		return ""
	}
	refURL, err := url.Parse(v)
	if err != nil {
		return ""
	}
	baseURL, err := url.Parse(h.bases.get(n))
	if err != nil || baseURL.Scheme == "" {
		if refURL.Scheme != "" {
			return refURL.String()
		}
		return ""
	}
	return baseURL.ResolveReference(refURL).String()
}
