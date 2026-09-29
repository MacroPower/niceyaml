package aliasing

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/docstate"
	"go.jacobcolvin.com/niceyaml/paths"
)

// CheckDecode returns [ErrExcessiveAliasing] when n holds an alias and
// aliases make up too much of what a decode of its document reads. The
// count covers the whole document, with each alias reading its content in
// full. A decode of a node below the root reads only the anchors the node
// needs, but CheckDecode applies one limit per document, so every node
// that holds an alias gets the verdict of its document. A node without an
// alias decodes on its own and reads nothing twice, so it passes, as does
// a nil Node.
//
// The count depends on the document alone, so the document keeps it, and
// a check of each item of a list counts the document once.
func CheckDecode(n *niceyaml.Node) error {
	if n == nil || !holdsAlias(n.AST()) {
		return nil
	}

	excessive := docstate.Of(n).ExcessiveAliasing(func() bool {
		return excessiveRead(n, readValue)
	})
	if excessive {
		return ErrExcessiveAliasing
	}

	return nil
}

// CheckDecodeText returns [ErrExcessiveAliasing] when n holds an alias
// and aliases make up too much of the text the decoder writes out for
// the node. To decode a type with an UnmarshalText method, or one that
// reads YAML bytes, the decoder writes the node out as text with each
// alias in full. The count covers the whole document as [CheckDecode]
// does. It reads every node as text, so each scalar counts one node per
// byte of its text. A node without an alias passes, as does a nil Node.
//
// A caller that decodes n into such a type runs CheckDecodeText as well
// as CheckDecode.
func CheckDecodeText(n *niceyaml.Node) error {
	if n == nil || !holdsAlias(n.AST()) {
		return nil
	}

	if excessiveRead(n, readText) {
		return ErrExcessiveAliasing
	}

	return nil
}

// excessiveRead reports whether aliases make up too much of what a
// decode of the document of n reads, with the document read as mode.
func excessiveRead(n *niceyaml.Node, mode readMode) bool {
	c := treeCounter{
		resolver: docstate.Of(n).Resolver(),
		sizes:    [readModes]map[ast.Node]int{{}, {}},
		open:     map[ast.Node]bool{},
	}

	c.count(n.DocumentAST().Body, true, mode)

	return Excessive(c.distinct, c.aliased)
}

// holdsAlias reports whether node or any node below it is an alias.
func holdsAlias(node ast.Node) bool {
	if astnode.IsNil(node) {
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
	if bool(*f) || astnode.IsNil(node) {
		return nil
	}

	if _, ok := node.(*ast.AliasNode); ok {
		*f = true

		return nil
	}

	return f
}

// readMode is how the decoder reads a node, which decides what a read of
// an alias to a scalar costs.
type readMode int

const (
	// A read into a value shares a scalar between its aliases.
	readValue readMode = iota

	// A read as text copies the text of a scalar at each alias to it.
	readText

	// The number of read modes.
	readModes
)

// treeCounter counts the nodes a decode of a document tree reads, the way
// gopkg.in/yaml.v3 counts them for its alias limit, where each alias
// reads the content it refers to in full. That covers a mapping a merge
// key brings in, which the decoder reads again at every merge. The
// resolver binds each alias to its content. An alias that does not
// resolve counts as one unaliased node, as does one inside its own
// content, which the decoder reads as null.
//
// Where the decoder reads a node into a value, an alias to a scalar
// counts as one unaliased node, as a shared scalar does in a decoded
// value. The decoder writes some nodes out as text instead: a key that
// holds a mapping or a sequence, a key that is an alias to a !!binary
// scalar, and the value under a !!str, !!int, !!bool, or !!binary tag.
// Each copy of a scalar in that text costs its length, so there a scalar
// counts one node per byte of its text, and an alias to one adds that
// count to the aliased nodes. The content of an anchor on a scalar counts
// its bytes too, so a scalar written out once weighs what its anchor
// does.
//
// The distinct field counts the nodes of the tree once each, and the
// aliased field counts the nodes the aliases in the tree repeat. The
// sizes maps hold the size of each alias's content once the counter has
// read it, one map for each read mode, so a chain of nested aliases costs
// one read per anchor. The open map holds the content the counter is
// reading.
type treeCounter struct {
	resolver *paths.Resolver
	sizes    [readModes]map[ast.Node]int
	open     map[ast.Node]bool
	distinct int
	aliased  int
}

// count returns the number of nodes a decode of node reads as mode. A
// scalar counts one, or its length in bytes when read as text. A mapping
// or a sequence counts one plus the count of each key, value, or element
// in it. An anchor, a tag, or the `?` of an explicit key counts what it
// wraps. With top set, node lies outside the content of every alias, and
// count adds its nodes to distinct and the nodes its aliases repeat to
// aliased.
func (c *treeCounter) count(node ast.Node, top bool, mode readMode) int {
	if astnode.IsNil(node) {
		return 0
	}

	switch n := node.(type) {
	case *ast.AliasNode:
		return c.alias(n, top, mode)
	case *ast.AnchorNode:
		return c.anchor(n, top, mode)
	case *ast.TagNode:
		return c.count(n.Value, top, tagMode(n, mode))
	case *ast.MappingKeyNode:
		return c.count(n.Value, top, mode)
	case *ast.CommentGroupNode:
		return 0
	}

	size := 1
	if mode == readText && !isCollection(node) {
		size = textSize(node)
	}

	if top {
		c.distinct = AddCapped(c.distinct, size)
	}

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, entry := range n.Values {
			size = AddCapped(size, c.entry(entry, top, mode))
		}

	case *ast.MappingValueNode:
		size = AddCapped(size, c.entry(n, top, mode))

	case *ast.SequenceNode:
		for _, elem := range n.Values {
			size = AddCapped(size, c.count(elem, top, mode))
		}
	}

	return size
}

// entry returns the number of nodes a decode of the key and the value of
// a mapping entry reads, as [treeCounter.count] counts them. The key
// counts as text where the decoder writes it out as text.
func (c *treeCounter) entry(entry *ast.MappingValueNode, top bool, mode readMode) int {
	if entry == nil {
		return 0
	}

	keyMode := mode
	if c.keyAsText(entry.Key) {
		keyMode = readText
	}

	return AddCapped(c.count(entry.Key, top, keyMode), c.count(entry.Value, top, mode))
}

// keyAsText reports whether the decoder writes key out as text. It does
// so for a key that decodes to neither a string nor a number, which is a
// mapping or a sequence, or an alias to one or to a !!binary scalar.
func (c *treeCounter) keyAsText(key ast.Node) bool {
	if astnode.IsNil(key) {
		return false
	}

	if isCollection(key) {
		return true
	}

	alias, ok := astnode.Content(key).(*ast.AliasNode)
	if !ok {
		return false
	}

	target, err := c.resolver.Deref(alias)
	if err != nil {
		return false
	}

	if isCollection(target) {
		return true
	}

	tag, ok := target.(*ast.TagNode)

	return ok && tag.Start != nil && token.ReservedTagKeyword(tag.Start.Value) == token.BinaryTag
}

// tagMode returns how the decoder reads the value under tag when it reads
// the tagged node as mode. The decoder writes out the value under a
// !!str, !!int, !!bool, or !!binary tag as text to convert it.
func tagMode(tag *ast.TagNode, mode readMode) readMode {
	if tag.Start == nil {
		return mode
	}

	switch token.ReservedTagKeyword(tag.Start.Value) {
	case token.StringTag, token.IntegerTag, token.BooleanTag, token.BinaryTag:
		return readText
	default:
		return mode
	}
}

// anchor returns the number of nodes a decode of the content of the
// anchor reads, as [treeCounter.count] counts them. The decoder reads an
// alias inside the content of its own anchor as null, so anchor marks
// the content open while it counts it, under the node [treeCounter.alias]
// looks the content up by, and such an alias counts as one node. The
// anchor marks open only content it defines itself. An anchor on an
// alias defines no content of its own, so [treeCounter.alias] counts
// that alias in full. The content of an anchor on a scalar counts as
// text, since an alias to it may copy that text.
func (c *treeCounter) anchor(anchor *ast.AnchorNode, top bool, mode readMode) int {
	if _, ok := astnode.Content(anchor.Value).(*ast.AliasNode); ok {
		return c.count(anchor.Value, top, mode)
	}

	if !isCollection(anchor.Value) {
		mode = readText
	}

	content, err := c.resolver.Deref(anchor)
	if err != nil || c.open[content] {
		return c.count(anchor.Value, top, mode)
	}

	c.open[content] = true
	size := c.count(anchor.Value, top, mode)
	delete(c.open, content)

	return size
}

// alias returns the number of nodes a decode of the alias reads as mode.
// That is the size of the mapping or sequence it refers to, the length
// of the text of a scalar it refers to when read as text, or one for any
// other alias. With top set, the alias lies outside the content of every
// other alias, and alias adds that size to aliased, or the one node to
// distinct.
func (c *treeCounter) alias(alias *ast.AliasNode, top bool, mode readMode) int {
	target, err := c.resolver.Deref(alias)
	if err != nil || c.open[target] || (mode == readValue && !isCollection(target)) {
		if top {
			c.distinct = AddCapped(c.distinct, 1)
		}

		return 1
	}

	size, ok := c.sizes[mode][target]
	if !ok {
		c.open[target] = true
		size = c.count(target, false, mode)
		delete(c.open, target)

		c.sizes[mode][target] = size
	}

	if top {
		c.aliased = AddCapped(c.aliased, size)
	}

	return size
}

// textSize returns the length in bytes of the text of the scalar node
// holds, and at least one.
func textSize(node ast.Node) int {
	size := 0

	switch n := astnode.Content(node).(type) {
	case *ast.StringNode:
		size = len(n.Value)

	case *ast.LiteralNode:
		if n.Value != nil {
			size = len(n.Value.Value)
		}

	case ast.ScalarNode:
		if tk := n.GetToken(); tk != nil {
			size = len(tk.Value)
		}
	}

	return max(size, 1)
}

// isCollection reports whether node holds a mapping or a sequence under
// its anchors and tags.
func isCollection(node ast.Node) bool {
	switch astnode.Content(node).(type) {
	case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
		return true
	default:
		return false
	}
}
