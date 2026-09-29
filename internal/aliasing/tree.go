package aliasing

import (
	"reflect"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/docstate"
	"go.jacobcolvin.com/niceyaml/paths"
)

// CheckDecode returns [ErrExcessiveAliasing] when n holds an alias and
// aliases make up too much of what a decode of its document reads. A
// decode of a node that holds an alias reads the whole document to find
// its anchors, so the count covers the whole document, with each alias
// reading its content in full. A node without an alias decodes on its own
// and reads nothing twice, so it passes, as does a nil Node.
//
// The count depends on the document alone, so the document keeps it, and
// a check of each item of a list counts the document once.
func CheckDecode(n *niceyaml.Node) error {
	if n == nil || !holdsAlias(n.AST()) {
		return nil
	}

	state := docstate.Of(n)
	doc := n.DocumentAST()

	excessive := state.ExcessiveAliasing(func() bool {
		c := treeCounter{
			resolver: state.Resolver(),
			sizes:    map[ast.Node]int{},
			open:     map[ast.Node]bool{},
		}

		c.count(doc.Body, true)

		return Excessive(c.distinct, c.aliased)
	})
	if excessive {
		return ErrExcessiveAliasing
	}

	return nil
}

// holdsAlias reports whether node or any node below it is an alias.
func holdsAlias(node ast.Node) bool {
	if isNilNode(node) {
		return false
	}

	var found aliasFinder

	ast.Walk(&found, node)

	return bool(found)
}

// aliasFinder is an [ast.Visitor] that records whether it visited an
// alias and stops the walk once it has.
type aliasFinder bool

// Visit implements [ast.Visitor].
func (f *aliasFinder) Visit(node ast.Node) ast.Visitor {
	if bool(*f) || isNilNode(node) {
		return nil
	}

	if _, ok := node.(*ast.AliasNode); ok {
		*f = true

		return nil
	}

	return f
}

// treeCounter counts the nodes a decode of a document tree reads, the way
// gopkg.in/yaml.v3 counts them for its alias limit, where each alias
// reads the content it refers to in full. That covers an alias the
// decoder writes out as text, such as a key, and a mapping a merge key
// brings in, which the decoder reads again at every merge. The resolver
// binds each alias to its content. An alias to a scalar counts as one
// unaliased node, as a shared scalar does in a decoded value, and so does
// an alias that does not resolve and one inside its own content, which
// the decoder reads as null.
//
// The distinct field counts the nodes of the tree once each, and the
// aliased field counts the nodes the aliases in the tree repeat. The
// sizes map holds the size of each alias's content once the counter has
// read it, so a chain of nested aliases costs one read per anchor, and
// the open map holds the content the counter is reading.
type treeCounter struct {
	resolver *paths.Resolver
	sizes    map[ast.Node]int
	open     map[ast.Node]bool
	distinct int
	aliased  int
}

// count returns the number of nodes a decode of node reads: one for a
// scalar, and one for a mapping or sequence plus the count of each key,
// value, or element in it. An anchor, a tag, or the `?` of an explicit
// key counts what it wraps. With top set, node lies outside the content
// of every alias, and count adds its nodes to distinct and the nodes its
// aliases repeat to aliased.
func (c *treeCounter) count(node ast.Node, top bool) int {
	if isNilNode(node) {
		return 0
	}

	switch n := node.(type) {
	case *ast.AliasNode:
		return c.alias(n, top)
	case *ast.AnchorNode:
		return c.anchor(n, top)
	case *ast.TagNode:
		return c.count(n.Value, top)
	case *ast.MappingKeyNode:
		return c.count(n.Value, top)
	case *ast.CommentGroupNode:
		return 0
	}

	if top {
		c.distinct = AddCapped(c.distinct, 1)
	}

	size := 1

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, entry := range n.Values {
			size = AddCapped(size, c.entry(entry, top))
		}

	case *ast.MappingValueNode:
		size = AddCapped(size, c.entry(n, top))

	case *ast.SequenceNode:
		for _, elem := range n.Values {
			size = AddCapped(size, c.count(elem, top))
		}
	}

	return size
}

// entry returns the number of nodes a decode of the key and the value of
// a mapping entry reads, as [treeCounter.count] counts them.
func (c *treeCounter) entry(entry *ast.MappingValueNode, top bool) int {
	if entry == nil {
		return 0
	}

	return AddCapped(c.count(entry.Key, top), c.count(entry.Value, top))
}

// anchor returns the number of nodes a decode of the content of the
// anchor reads, as [treeCounter.count] counts them. The decoder reads an
// alias inside the content of its own anchor as null, so anchor marks
// the content open while it counts it, under the node [treeCounter.alias]
// looks the content up by, and such an alias counts as one node. The
// anchor marks open only content it defines itself. An anchor on an
// alias defines no content of its own, so [treeCounter.alias] counts
// that alias in full.
func (c *treeCounter) anchor(anchor *ast.AnchorNode, top bool) int {
	if _, ok := contentNode(anchor.Value).(*ast.AliasNode); ok {
		return c.count(anchor.Value, top)
	}

	content, err := c.resolver.Deref(anchor)
	if err != nil || c.open[content] {
		return c.count(anchor.Value, top)
	}

	c.open[content] = true
	size := c.count(anchor.Value, top)
	delete(c.open, content)

	return size
}

// alias returns the number of nodes a decode of the alias reads, which is
// the size of the mapping or sequence it refers to, or one for any other
// alias. With top set, the alias lies outside the content of every other
// alias, and alias adds that size to aliased, or the one node to
// distinct.
func (c *treeCounter) alias(alias *ast.AliasNode, top bool) int {
	target, err := c.resolver.Deref(alias)
	if err != nil || c.open[target] || !isCollection(target) {
		if top {
			c.distinct = AddCapped(c.distinct, 1)
		}

		return 1
	}

	size, ok := c.sizes[target]
	if !ok {
		c.open[target] = true
		size = c.count(target, false)
		delete(c.open, target)

		c.sizes[target] = size
	}

	if top {
		c.aliased = AddCapped(c.aliased, size)
	}

	return size
}

// isCollection reports whether node holds a mapping or a sequence under
// its anchors and tags.
func isCollection(node ast.Node) bool {
	switch contentNode(node).(type) {
	case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
		return true
	default:
		return false
	}
}

// contentNode looks through the nodes that wrap a value, so the counter
// sees the mapping or sequence behind a document, an anchor, a tag, or
// the `?` of an explicit key. It returns nil for a nil node, including a
// typed nil a tree built by hand may hold where the parser always puts a
// node.
func contentNode(node ast.Node) ast.Node {
	for !isNilNode(node) {
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

// isNilNode reports whether node is nil, including a typed nil a tree
// built by hand may hold behind a non-nil interface.
func isNilNode(node ast.Node) bool {
	return node == nil || reflect.ValueOf(node).IsNil()
}
