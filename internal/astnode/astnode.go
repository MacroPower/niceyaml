// Package astnode holds the nil and wrapper rules for go-yaml AST nodes,
// the rule for which document bodies hold a value, and the rule for which
// token a path to a node points at.
//
// A tree built by hand may hold a typed nil behind a non-nil [ast.Node],
// where the parser always puts a node. Code that walks a tree calls
// [IsNil] rather than comparing a node with nil, and calls [Content] to
// reach the value behind a document, an anchor, a tag, or the `?` of an
// explicit key. Every walk then agrees on what a node holds.
//
//	if m, ok := astnode.Content(node).(*ast.MappingNode); ok {
//		// node is a mapping, or wraps one.
//	}
//
// The parser gives some documents a body that holds no value: an empty
// document, one of comments alone, one of a directive alone, and one of
// whitespace alone. Code that reads a document checks its body with
// [HasContent], so a decode and a path lookup agree on which documents
// are empty.
//
// A path points at one token of the document, which [PathToken] returns
// for the node the path selects and the [Parent] that holds the node. A
// scalar is its own token. A mapping or a sequence spans many lines, so
// it points at the token that introduces it: the key of the entry it is
// the value of, or the "-" of the block sequence element it is. Code that
// points at a node it reached without a path calls PathToken too, so both
// name the same place for one node.
package astnode

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Content looks through the nodes that wrap a value and returns the
// value behind a document, an anchor, a tag, or the `?` of an explicit
// key. It returns any other node unchanged. Content returns nil for
// a nil node, including a typed nil, whether that node wraps a value or
// is one.
func Content(node ast.Node) ast.Node {
	for !IsNil(node) {
		switch n := node.(type) {
		case *ast.DocumentNode:
			node = n.Body
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		case *ast.MappingKeyNode:
			node = n.Value
		default:
			return node
		}
	}

	return nil
}

// IsNil reports whether node is nil, including a typed nil behind a
// non-nil interface. A node that is not a pointer, such as a struct that
// embeds one, is never nil.
func IsNil(node ast.Node) bool {
	return nilness.IsNil(node)
}

// HasContent reports whether node holds a YAML value. It returns false
// for a nil node, including a typed nil, and for a comment group or a
// directive. Those are the bodies of an empty document, a document of
// comments alone, and a document of a %YAML directive alone. It also
// returns false for a scalar holding the placeholder token that
// [tokens.Tokenize] makes for text the lexer emits nothing for, such as
// a file of whitespace alone.
func HasContent(node ast.Node) bool {
	if IsNil(node) {
		return false
	}

	if scalar, ok := node.(*ast.StringNode); ok && tokens.IsPlaceholder(scalar.Token) {
		return false
	}

	switch node.Type() {
	case ast.CommentType, ast.DirectiveType:
		return false

	default:
		return true
	}
}

// Parent names what holds a node in a tree: the mapping entry the node
// is the value of, or the sequence the node is an element of. The zero
// Parent holds the root of a document, the key of an entry, and any
// node whose place in the tree the caller does not know.
type Parent struct {
	// The entry the node is the value of, or nil.
	Entry *ast.MappingValueNode
	// The sequence the node is an element of, or nil.
	Sequence *ast.SequenceNode
	// The index of the node in Sequence.
	Index int
}

// PathToken returns the token a path to node points at, where in holds
// node. A scalar is its own token, and so is an alias, whatever its
// anchor holds. A mapping or a sequence points at the token that
// introduces it, under the anchors and tags on node. The first of these
// rules that fits the node gives that token.
//
//   - The value of a mapping entry points at the key of the entry, as
//     [KeyToken] gives it, whether the value is in block or flow style.
//   - An element of a block sequence points at its "-".
//   - A flow mapping or a flow sequence points at its own "{" or "[".
//     One at the root of a document does, and so does an element of a
//     flow sequence or the key of an entry.
//   - A block mapping or a block sequence points at the token that
//     starts its content, which is its first key or its first element.
//     One at the root of a document does.
//
// An entry with no key introduces nothing, so its value points where it
// would with no entry. A nil node, including a typed nil, has no token.
func PathToken(node ast.Node, in Parent) *token.Token {
	if IsNil(node) {
		return nil
	}

	var start *token.Token

	switch n := unwrapped(node).(type) {
	case *ast.MappingNode:
		start = flowStart(n.IsFlowStyle, n.Start, token.MappingStartType)
	case *ast.SequenceNode:
		start = flowStart(n.IsFlowStyle, n.Start, token.SequenceStartType)
	default:
		return firstToken(node)
	}

	if tk := KeyToken(in.Entry); tk != nil {
		return tk
	}

	if tk := entryToken(in.Sequence, in.Index); tk != nil {
		return tk
	}

	if start != nil {
		return start
	}

	return firstToken(node)
}

// KeyToken returns the token a path to the key of entry points at, as a
// path that ends in a `~` selector resolves it. It leaves out the `?` of
// an explicit key, and looks through the anchors and tags on the key. It
// returns nil for a nil entry and for an entry with no key.
func KeyToken(entry *ast.MappingValueNode) *token.Token {
	if entry == nil || Content(entry.Key) == nil {
		return nil
	}

	key := ast.Node(entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return PathToken(key, Parent{})
}

// unwrapped returns the node under the anchors and tags on node. It
// stops at an alias, which is a token of its own, where [Content] also
// looks through a document and the `?` of an explicit key.
func unwrapped(node ast.Node) ast.Node {
	for !IsNil(node) {
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		default:
			return node
		}
	}

	return nil
}

// flowStart returns start when it is the token of kind that opens a flow
// collection, and nil for a block collection. A tree built by hand may
// hold any token there, or none.
func flowStart(flow bool, start *token.Token, kind token.Type) *token.Token {
	if !flow || start == nil || start.Type != kind {
		return nil
	}

	return start
}

// entryToken returns the "-" of element index of seq, or nil when seq
// writes none for it. The parser gives a flow sequence no token for its
// first element and the "," for each later one. A tree built by hand may
// hold no entries at all.
func entryToken(seq *ast.SequenceNode, index int) *token.Token {
	if seq == nil || index < 0 || index >= len(seq.Entries) {
		return nil
	}

	entry := seq.Entries[index]
	if entry == nil || entry.Start == nil || entry.Start.Type != token.SequenceEntryType {
		return nil
	}

	return entry.Start
}

// firstToken returns the token that starts the content of node: the first
// key of a mapping, the first element of a sequence, or the scalar itself.
// It looks through anchors and tags, and through the `?` indicator,
// anchors, and tags of the first key of a mapping. An alias is its own
// token. An entry with no key starts at its own token. A nil node,
// including a typed nil, has no token.
func firstToken(node ast.Node) *token.Token {
	for {
		if IsNil(node) {
			return nil
		}

		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		case *ast.MappingNode:
			if len(n.Values) == 0 || n.Values[0] == nil {
				return n.GetToken()
			}

			key := Content(n.Values[0].Key)
			if key == nil {
				return n.Values[0].GetToken()
			}

			node = key

		case *ast.SequenceNode:
			if len(n.Values) == 0 {
				return n.GetToken()
			}

			node = n.Values[0]

		default:
			return node.GetToken()
		}
	}
}
