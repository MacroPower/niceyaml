package astnode

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// Misnested calls found with the token of each block entry or item under
// node that go-yaml's parse nests too deep. The calls come in the order of
// the text. An entry or an item lies too deep when its indentation does
// not let YAML nest it there. Misnested also passes props, the anchor or
// the tag that holds the collection of that entry or item, or nil. It
// stops when found returns false, and it reports whether it stopped.
//
// YAML nests a collection below an entry or an item only when the
// collection starts past the column of that entry or that "-". A block
// sequence can also start at the column of the key whose value it is.
// Go-yaml's parser nests the lines after a tag on an empty value in that
// value anyway. So it reads b of "a: !t\nb: 1" as an entry of the value
// of a. It does the same after an anchor on an empty item, as it reads r1
// of "r0:\n- &x0\nr1: 1" as an entry of the item. A flow collection does
// not count, because YAML reads it by its brackets.
func Misnested(node ast.Node, found func(tk *token.Token, props ast.Node) bool) bool {
	return misnested(node, 0, 0, nil, found)
}

// misnested is [Misnested] for node, whose block entries start at a
// column of keyMin or more and whose block items start at a column of
// seqMin or more. The props are the anchor or the tag that holds node.
func misnested(node ast.Node, keyMin, seqMin int, props ast.Node, found func(*token.Token, ast.Node) bool) bool {
	switch n := node.(type) {
	case *ast.AnchorNode:
		return misnested(n.Value, keyMin, seqMin, n, found)

	case *ast.TagNode:
		return misnested(n.Value, keyMin, seqMin, n, found)

	case *ast.MappingValueNode:
		return misnestedEntry(n, keyMin, props, found)

	case *ast.MappingNode:
		if n.IsFlowStyle {
			return false
		}

		for _, e := range n.Values {
			if misnestedEntry(e, keyMin, props, found) {
				return true
			}
		}

	case *ast.SequenceNode:
		if n.IsFlowStyle {
			return false
		}

		for i, v := range n.Values {
			tk := entryToken(n, i)
			if tk == nil || tk.Position == nil {
				continue
			}

			col := tk.Position.Column - 1
			if col < seqMin && !found(tk, props) {
				return true
			}

			if misnested(v, col+1, col+1, nil, found) {
				return true
			}
		}
	}

	return false
}

// misnestedEntry calls found with the token of e, an entry of a block
// mapping, when it starts at a column less than keyMin. It then calls
// found for the entries and the items in the key and the value of e. It
// reports whether found stopped it.
func misnestedEntry(e *ast.MappingValueNode, keyMin int, props ast.Node, found func(*token.Token, ast.Node) bool) bool {
	// The token of a key is its first token: the "?" of an explicit key,
	// the first property of a key with properties, or the key itself.
	tk := Token(e.Key)
	if tk == nil || tk.Position == nil {
		return false
	}

	col := tk.Position.Column - 1
	if col < keyMin && !found(tk, props) {
		return true
	}

	if misnested(e.Key, col+1, col+1, nil, found) {
		return true
	}

	return misnested(e.Value, col+1, col, nil, found)
}
