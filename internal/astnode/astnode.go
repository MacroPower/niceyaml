// Package astnode holds the nil and wrapper rules for go-yaml AST nodes.
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
package astnode

import (
	"reflect"

	"github.com/goccy/go-yaml/ast"
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
// non-nil interface.
func IsNil(node ast.Node) bool {
	return node == nil || reflect.ValueOf(node).IsNil()
}
