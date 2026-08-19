package host

import (
	"context"
	stdhtml "html"
	"net/url"
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
	root, err := nethtml.Parse(strings.NewReader(string(data)))
	if err != nil {
		return 0, err
	}
	h.bases.set(root, baseURL)
	return h.Store.Store(root), nil
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
			root, err := nethtml.Parse(strings.NewReader(htmlStr))
			if err != nil {
				return int32(htmlInvalidHTML)
			}
			if baseLen > 0 {
				if baseURL, ok := readMemString(m, baseOff, baseLen); ok {
					h.bases.set(root, baseURL)
				}
			}
			return h.Store.Store(root)
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
			if baseLen > 0 {
				if baseURL, ok := readMemString(m, baseOff, baseLen); ok {
					h.bases.set(root, baseURL)
				}
			}
			return h.Store.Store(root)
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
			switch v := item.(type) {
			case elementList:
				for _, n := range v {
					if val, ok := resolveAbsAttr(h, n, key); ok {
						return h.Store.Store(val)
					}
				}
				return int32(htmlNoResult)
			case *nethtml.Node:
				if val, ok := resolveAbsAttr(h, v, key); ok {
					return h.Store.Store(val)
				}
				return int32(htmlNoResult)
			default:
				return int32(htmlInvalidDescriptor)
			}
		}).
		Export("attr")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			item := h.Store.Fetch(descriptor)
			switch v := item.(type) {
			case elementList:
				if len(v) == 0 {
					return int32(htmlNoResult)
				}
				s, err := renderNode(v[0])
				if err != nil {
					return int32(htmlNoResult)
				}
				return h.Store.Store(s)
			case *nethtml.Node:
				s, err := renderNode(v)
				if err != nil {
					return int32(htmlNoResult)
				}
				return h.Store.Store(s)
			default:
				return int32(htmlInvalidDescriptor)
			}
		}).
		Export("outer_html")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, descriptor int32) int32 {
			item := h.Store.Fetch(descriptor)
			switch v := item.(type) {
			case elementList:
				for _, n := range v {
					removeNode(n)
				}
				return int32(htmlSuccess)
			case *nethtml.Node:
				removeNode(v)
				return int32(htmlSuccess)
			default:
				return int32(htmlInvalidDescriptor)
			}
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
			item := h.Store.Fetch(descriptor)
			switch v := item.(type) {
			case elementList:
				if len(v) == 0 {
					return int32(htmlNoResult)
				}
				s, err := innerHTML(v[0])
				if err != nil {
					return int32(htmlNoResult)
				}
				return h.Store.Store(s)
			case *nethtml.Node:
				s, err := innerHTML(v)
				if err != nil {
					return int32(htmlNoResult)
				}
				return h.Store.Store(s)
			default:
				return int32(htmlInvalidDescriptor)
			}
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
	sel, err := cascadia.ParseGroup(query)
	if err != nil {
		return int32(htmlInvalidQuery)
	}

	var roots []*nethtml.Node
	switch v := item.(type) {
	case elementList:
		roots = v
	case *nethtml.Node:
		roots = []*nethtml.Node{v}
	default:
		return int32(htmlInvalidDescriptor)
	}

	var out elementList
	seen := make(map[*nethtml.Node]bool)
	for _, root := range roots {
		for _, n := range cascadia.QueryAll(root, sel) {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return h.Store.Store(out)
}

func (h *Html) textImpl(descriptor int32, trim bool) int32 {
	item := h.Store.Fetch(descriptor)
	switch v := item.(type) {
	case elementList:
		if len(v) == 0 {
			return int32(htmlNoResult)
		}
		return h.Store.Store(nodeText(v[0], trim))
	case *nethtml.Node:
		if v.Type == nethtml.TextNode {
			if trim {
				return h.Store.Store(strings.TrimSpace(v.Data))
			}
			return h.Store.Store(v.Data)
		}
		return h.Store.Store(nodeText(v, trim))
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

// resolveAbsAttr reads node's attribute. For a key prefixed with "abs:" it
// strips the prefix, reads the underlying attribute, and resolves it against
// the node's parsed base URL (recorded by ParseHTML bases.set) — nil/false
// when there's no base or the value isn't a resolvable URL.
func resolveAbsAttr(h *Html, n *nethtml.Node, key string) (string, bool) {
	if !strings.HasPrefix(key, absAttrPrefix) {
		return attrOf(n, key)
	}
	realKey := key[len(absAttrPrefix):]
	v, ok := attrOf(n, realKey)
	if !ok || v == "" {
		return "", false
	}
	base := h.bases.get(n)
	if base == "" {
		return "", false
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	refURL, err := url.Parse(v)
	if err != nil {
		return "", false
	}
	return baseURL.ResolveReference(refURL).String(), true
}
