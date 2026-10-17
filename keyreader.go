package niceyaml

import (
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
)

// init sets [hook.Keys], as the package [hook] describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	hook.Keys = func(n any) *mapkey.Keys {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return node.keyReader()
	}
}

// keyReader returns a new [*mapkey.Keys] of the keys of the document of
// n. It names each key as a decode of the document reads it, through the
// [decodetree.Tree] of the document and the anchors of the reference
// documents.
func (n *Node) keyReader() *mapkey.Keys {
	var body ast.Node

	if n.doc.root != nil {
		body = n.doc.root.Body
	}

	refs := func(names []string) []ast.Node {
		return n.source.referenceAnchors().Prime(names)
	}

	return mapkey.NewKeys(body, n.decodeView, refs)
}

// decodeView is the [mapkey.Viewer] of the keys of n. It returns the node of
// the [decodetree.Tree] that stands for node, a node of the document of n,
// with the anchors that a decode of node reads before it, in order. The
// anchors of the reference documents that the aliases reach come first. They
// include those that the anchors of the document reach, because the decoder
// reads those anchors too. The anchors of the document before node that its
// aliases need come next. The body reads no anchor of the document before
// it. A document that did not parse has no tree, so its nodes read no
// anchor. A node whose token the parse of the document does not hold lies in
// a reference document, so decodeView returns the [decodetree.DecodeView] of
// the node and reports false.
func (n *Node) decodeView(node ast.Node) (ast.Node, []ast.Node, bool) {
	if n.doc.root == nil {
		return node, nil, true
	}

	if !n.holdsToken(astnode.Token(node)) {
		return decodetree.DecodeView(node), nil, false
	}

	tree := n.doc.tree()
	view := tree.View(node)

	var anchors []*ast.AnchorNode

	if node != n.doc.root.Body {
		anchors = tree.Prime(view)
	}

	var nodes []ast.Node

	if refs := n.source.referenceAnchors(); refs != nil {
		names := tree.ReferenceNames(view)
		for _, anchor := range anchors {
			names = append(names, tree.ReferenceNames(anchor)...)
		}

		nodes = append(nodes, refs.Prime(names)...)
	}

	for _, anchor := range anchors {
		nodes = append(nodes, anchor)
	}

	return view, nodes, true
}
