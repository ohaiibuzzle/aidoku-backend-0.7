package host

import (
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// elementList represents SwiftSoup's `Elements` — a group of matched nodes,
// as returned by select() or children(). A lone *nethtml.Node in the Store
// represents a single Node/Element/Document/TextNode/Comment, matching
// AidokuRunner's `kind` discriminator (Imports/Html.swift).
type elementList []*nethtml.Node

// baseURIs records the base URL each parsed document/fragment root was
// parsed with, since golang.org/x/net/html doesn't track this per node the
// way SwiftSoup does. baseURI() walks a node's ancestor chain looking for
// an entry, so any node under a recorded root resolves correctly.
type baseURIs struct {
	roots map[*nethtml.Node]string
}

func newBaseURIs() *baseURIs { return &baseURIs{roots: make(map[*nethtml.Node]string)} }

func (b *baseURIs) set(root *nethtml.Node, uri string) {
	if uri != "" {
		b.roots[root] = uri
	}
}

// remove drops root's entry. root staying as a map key would otherwise
// keep the whole node tree reachable to the GC for the life of the
// process, even after its Store descriptor is gone -- see Store.cleanup's
// doc comment.
func (b *baseURIs) remove(root *nethtml.Node) {
	delete(b.roots, root)
}

func (b *baseURIs) get(n *nethtml.Node) string {
	for cur := n; cur != nil; cur = cur.Parent {
		if uri, ok := b.roots[cur]; ok {
			return uri
		}
	}
	return ""
}

func isTextLikeContainer(n *nethtml.Node) bool {
	return n != nil && n.Type == nethtml.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style)
}

// nodeText mirrors SwiftSoup's Element.text()/untrimmed text: the
// concatenation of descendant text nodes, excluding <script>/<style>
// contents, optionally whitespace-trimmed/collapsed.
func nodeText(n *nethtml.Node, trim bool) string {
	var sb strings.Builder
	var walk func(*nethtml.Node)
	walk = func(node *nethtml.Node) {
		if node == nil || isTextLikeContainer(node) {
			return
		}
		if node.Type == nethtml.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	s := sb.String()
	if !trim {
		return s
	}
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// ownText mirrors Element.ownText(): direct text-node children only, not
// descending into child elements.
func ownText(n *nethtml.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == nethtml.TextNode {
			sb.WriteString(c.Data)
		}
	}
	fields := strings.Fields(sb.String())
	return strings.Join(fields, " ")
}

func attrOf(n *nethtml.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *nethtml.Node, key, value string) {
	for i, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, nethtml.Attribute{Key: key, Val: value})
}

func removeAttr(n *nethtml.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func hasClass(n *nethtml.Node, class string) bool {
	v, _ := attrOf(n, "class")
	for _, c := range strings.Fields(v) {
		if c == class {
			return true
		}
	}
	return false
}

func addClass(n *nethtml.Node, class string) {
	if hasClass(n, class) {
		return
	}
	v, _ := attrOf(n, "class")
	if v == "" {
		setAttr(n, "class", class)
		return
	}
	setAttr(n, "class", v+" "+class)
}

func removeClass(n *nethtml.Node, class string) {
	v, ok := attrOf(n, "class")
	if !ok {
		return
	}
	fields := strings.Fields(v)
	out := fields[:0]
	for _, c := range fields {
		if c != class {
			out = append(out, c)
		}
	}
	setAttr(n, "class", strings.Join(out, " "))
}

func className(n *nethtml.Node) string {
	v, _ := attrOf(n, "class")
	return v
}

func renderNode(n *nethtml.Node) (string, error) {
	var sb strings.Builder
	if err := nethtml.Render(&sb, n); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// innerHTML renders n's children (not n itself).
func innerHTML(n *nethtml.Node) (string, error) {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := nethtml.Render(&sb, c); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

func removeAllChildren(n *nethtml.Node) {
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
}

// parseFragmentNodes parses htmlStr as a body fragment and returns the
// resulting sibling nodes, suitable for use as new children/insertions.
func parseFragmentNodes(htmlStr string) ([]*nethtml.Node, error) {
	context := &nethtml.Node{Type: nethtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := nethtml.ParseFragment(strings.NewReader(htmlStr), context)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func setInnerHTML(n *nethtml.Node, htmlStr string) error {
	nodes, err := parseFragmentNodes(htmlStr)
	if err != nil {
		return err
	}
	removeAllChildren(n)
	for _, c := range nodes {
		if c.Parent != nil {
			c.Parent.RemoveChild(c)
		}
		n.AppendChild(c)
	}
	return nil
}

func setText(n *nethtml.Node, text string) {
	removeAllChildren(n)
	n.AppendChild(&nethtml.Node{Type: nethtml.TextNode, Data: text})
}

func prependHTML(n *nethtml.Node, htmlStr string) error {
	nodes, err := parseFragmentNodes(htmlStr)
	if err != nil {
		return err
	}
	first := n.FirstChild
	for _, c := range nodes {
		if c.Parent != nil {
			c.Parent.RemoveChild(c)
		}
		if first != nil {
			n.InsertBefore(c, first)
		} else {
			n.AppendChild(c)
		}
	}
	return nil
}

func appendHTML(n *nethtml.Node, htmlStr string) error {
	nodes, err := parseFragmentNodes(htmlStr)
	if err != nil {
		return err
	}
	for _, c := range nodes {
		if c.Parent != nil {
			c.Parent.RemoveChild(c)
		}
		n.AppendChild(c)
	}
	return nil
}

func removeNode(n *nethtml.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// elementChildren returns n's element (not text/comment) children, mirror
// of Element.children().
func elementChildren(n *nethtml.Node) elementList {
	var out elementList
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == nethtml.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// siblingElements returns n's sibling elements (excluding n itself),
// mirror of Element.siblingElements().
func siblingElements(n *nethtml.Node) elementList {
	if n.Parent == nil {
		return nil
	}
	var out elementList
	for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
		if c != n && c.Type == nethtml.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// siblingNodes returns all of n's sibling nodes (any type, excluding n).
func siblingNodes(n *nethtml.Node) elementList {
	if n.Parent == nil {
		return nil
	}
	var out elementList
	for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
		if c != n {
			out = append(out, c)
		}
	}
	return out
}

func nextElementSibling(n *nethtml.Node) *nethtml.Node {
	for c := n.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == nethtml.ElementNode {
			return c
		}
	}
	return nil
}

func previousElementSibling(n *nethtml.Node) *nethtml.Node {
	for c := n.PrevSibling; c != nil; c = c.PrevSibling {
		if c.Type == nethtml.ElementNode {
			return c
		}
	}
	return nil
}

func dataOf(n *nethtml.Node) (string, bool) {
	switch n.Type {
	case nethtml.CommentNode:
		return n.Data, true
	case nethtml.ElementNode:
		if n.DataAtom == atom.Script || n.DataAtom == atom.Style {
			var sb strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == nethtml.TextNode {
					sb.WriteString(c.Data)
				}
			}
			return sb.String(), true
		}
		return "", false
	default:
		return "", false
	}
}
