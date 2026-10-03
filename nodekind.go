package niceyaml

import (
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// NodeKind is the structural kind of the value a [Node] holds, as
// [Node.Kind] reports it.
type NodeKind int

// [NodeKind] constants.
const (
	// NodeNone is the kind of a Node that holds no value: the root of an
	// empty document, of a document of comments or directives alone, or
	// of a document that did not parse, and a nil Node. [Node.Err] tells a
	// document that did not parse apart from an empty one.
	NodeNone NodeKind = iota
	// NodeMapping is the kind of a mapping, block or flow.
	NodeMapping
	// NodeSequence is the kind of a sequence, block or flow.
	NodeSequence
	// NodeScalar is the kind of a scalar, a null included, whatever its
	// tag.
	NodeScalar
)

// Kind reports whether the Node holds a mapping, a sequence, or a
// scalar, or [NodeNone] when it holds no value. A caller that walks a
// document picks the selector that lists the children of the Node from
// it:
//
//	var children []*niceyaml.Node
//
//	switch n.Kind() {
//	case niceyaml.NodeMapping:
//		children, err = n.Nodes(paths.Root().ChildAll())
//	case niceyaml.NodeSequence:
//		children, err = n.Nodes(paths.Root().IndexAll())
//	}
//
// Kind looks through the anchor and the tag on the node, an alias and
// the anchor it refers to, and the `?` of an explicit key. The root of
// `&r {a: 1}` and of `!!map {a: 1}` is thus a mapping, where [Node.AST]
// gives the anchor or the tag that wraps it. A Node that [Node.At] or
// [Node.Nodes] scopes at an alias holds the content of its anchor, so no
// kind stands for an alias. The root of a document that is an alias with
// no anchor before it holds no value.
//
// Every scalar has the one kind. The type a scalar decodes to depends on
// its tag, on its text, and on the Go type a decode fills, so a caller
// that needs it decodes the Node.
func (n *Node) Kind() NodeKind {
	if n == nil || n.doc.err != nil {
		return NodeNone
	}

	node := n.AST()
	if !astnode.HasContent(node) {
		return NodeNone
	}

	switch contentNode(n.doc.pathResolver(), node).(type) {
	case nil:
		return NodeNone
	case *ast.MappingNode:
		return NodeMapping
	case *ast.SequenceNode:
		return NodeSequence
	default:
		return NodeScalar
	}
}
