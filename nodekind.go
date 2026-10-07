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
	// of a document that did not parse, and a nil Node. The root of a
	// document that is an alias with no anchor before it has this kind
	// too. [Node.IsEmpty] reports the documents with no content among
	// these, and [Node.Err] tells a document that did not parse apart
	// from an empty one.
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
//		children, err = n.Nodes(paths.Current().ChildAll())
//	case niceyaml.NodeSequence:
//		children, err = n.Nodes(paths.Current().IndexAll())
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

// IsEmpty reports whether the Node is the root of a document that parsed
// and holds no content. Such a document is an empty file, a file of
// whitespace or comments alone, or a "---" header with nothing but
// comments below it. A %YAML or %TAG directive above the header adds no
// content.
//
// IsEmpty describes the document and not a value in it. A document that
// holds any value is not empty, so IsEmpty is false for an empty mapping
// `{}`, an empty sequence `[]`, and an empty string `""`. It is false
// for an explicit null such as `null` or `~`, and for an alias, even one
// with no anchor before it. A caller that asks whether a mapping or a
// sequence holds anything lists its entries with [Node.Nodes].
//
// It is false for a document that did not parse, which [Node.Err]
// reports, for a Node that [Node.At] or [Node.Nodes] scopes below the
// root, and for a nil Node. [Node.Kind] is [NodeNone] for every Node
// IsEmpty reports. Kind is NodeNone too for the root of a document that
// did not parse and for a root that is an alias with no anchor before
// it, so a caller that passes over empty documents asks IsEmpty.
//
// Every document validates and decodes, an empty one included. A file
// of several documents often holds empty ones. A trailing "---" leaves
// one, and so does a Helm template that renders to a comment alone. A
// loop that reads the documents with content passes over the ones
// IsEmpty reports:
//
//	for _, doc := range docs {
//		if doc.IsEmpty() {
//			continue
//		}
//
//		manifest, err := doc.Decode[Manifest](ctx)
//		// ...
//	}
//
// [Source.ValidateDocuments] passes over such a document in a file that
// holds a document with content. [SkipEmpty] wraps a [Validator] so that
// it passes one wherever it runs, such as in a decode.
func (n *Node) IsEmpty() bool {
	if n == nil || n.doc.err != nil || !n.base.IsRoot() {
		return false
	}

	return !astnode.HasContent(n.doc.root.Body)
}
