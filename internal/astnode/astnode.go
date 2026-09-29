// Package astnode holds the nil and wrapper rules for go-yaml AST nodes,
// and the rule for which document bodies hold a value.
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
package astnode

import (
	"github.com/goccy/go-yaml/ast"

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
