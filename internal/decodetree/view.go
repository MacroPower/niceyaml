package decodetree

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// DecodeView returns node as the go-yaml decoder reads it: the same tree,
// with the name of each alias free of comments. The parser attaches a
// comment on the line of an alias to its name, and the decoder looks the
// anchor up by the text of the name, comment included, so `*x # note`
// would name no anchor. The view copies each alias and the nodes above
// it, and shares every other node and every token with node. The tree the
// Source shares thus stays as the parser built it, and every error the
// decoder reports names a token of the source.
func DecodeView(node ast.Node) ast.Node {
	view, _ := viewOf(node)

	return view
}

// viewOf returns the [DecodeView] of node, and whether it differs from
// node.
func viewOf(node ast.Node) (ast.Node, bool) {
	if astnode.IsNil(node) {
		return node, false
	}

	switch n := node.(type) {
	case *ast.AliasNode:
		if astnode.IsNil(n.Value) || n.Value.GetToken() == nil {
			return n, false
		}

		// The decoder registers an anchor under the value of its name
		// token, which a plain string node of the same token spells.
		c := *n
		c.Value = ast.String(n.Value.GetToken())

		return &c, true

	case *ast.AnchorNode:
		value, changed := viewOf(n.Value)
		if !changed {
			return n, false
		}

		c := *n
		c.Value = value

		return &c, true

	case *ast.TagNode:
		value, changed := viewOf(n.Value)
		if !changed {
			return n, false
		}

		c := *n
		c.Value = value

		return &c, true

	case *ast.MappingKeyNode:
		value, changed := viewOf(n.Value)
		if !changed {
			return n, false
		}

		c := *n
		c.Value = value

		return &c, true

	case *ast.MappingValueNode:
		return mappingValueView(n)

	case *ast.MappingNode:
		values, changed := viewsOf(n.Values, mappingValueView)
		if !changed {
			return n, false
		}

		c := *n
		c.Values = values

		return &c, true

	case *ast.SequenceNode:
		values, changed := viewsOf(n.Values, viewOf)
		if !changed {
			return n, false
		}

		c := *n
		c.Values = values

		return &c, true

	default:
		return node, false
	}
}

// mappingValueView is [viewOf] for an entry of a mapping.
func mappingValueView(n *ast.MappingValueNode) (*ast.MappingValueNode, bool) {
	if n == nil {
		return nil, false
	}

	key, keyChanged := viewOf(n.Key)
	value, valueChanged := viewOf(n.Value)

	if !keyChanged && !valueChanged {
		return n, false
	}

	c := *n
	c.Value = value

	// A copy has the type of its original, so a key stays a key.
	if k, ok := key.(ast.MapKeyNode); ok {
		c.Key = k
	}

	return &c, true
}

// viewsOf applies view to each of nodes, and returns a new slice when any
// of them changed, or nodes itself when none did.
func viewsOf[T ast.Node](nodes []T, view func(T) (T, bool)) ([]T, bool) {
	var views []T

	for i, node := range nodes {
		v, changed := view(node)
		if !changed {
			if views != nil {
				views[i] = node
			}

			continue
		}

		if views == nil {
			views = make([]T, len(nodes))
			copy(views, nodes[:i])
		}

		views[i] = v
	}

	if views == nil {
		return nodes, false
	}

	return views, true
}

// verbatimIntegerTag is the verbatim form of the !!int tag, which the
// [Tree] gives the token of each tag [isIntegerTag] reports. The go-yaml
// decoder rejects an integer under the short form for every integer type,
// but it reserves only short forms, so it reads an integer under this one
// as it reads the integer alone.
const verbatimIntegerTag = "!<tag:yaml.org,2002:int>"

// isIntegerTag reports whether node is a !!int tag on an integer, or on
// an anchor on one. A tag after a %TAG directive that redefines the "!!"
// handle does not count, as the go-yaml decoder reads it as text.
func isIntegerTag(node ast.Node) bool {
	tag, ok := node.(*ast.TagNode)
	if !ok || tag.Start == nil || tag.Directive != nil ||
		token.ReservedTagKeyword(tag.Start.Value) != token.IntegerTag {
		return false
	}

	value := tag.Value
	if anchor, ok := value.(*ast.AnchorNode); ok {
		value = anchor.Value
	}

	_, ok = value.(*ast.IntegerNode)

	return ok
}

// holdsIntegerTag reports whether node or any node below it is a tag
// [isIntegerTag] reports.
func holdsIntegerTag(node ast.Node) bool {
	found := false

	astnode.Inspect(node, func(n ast.Node) bool {
		found = found || isIntegerTag(n)

		return !found
	})

	return found
}
