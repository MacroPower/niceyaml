// Package astnode holds the nil and wrapper rules for go-yaml abstract
// syntax tree (AST) nodes. It also holds the rule for which document
// bodies hold a value and the rule for which token a path to a node
// points at.
//
// A tree built by hand might hold a typed nil behind a non-nil
// [ast.Node], where the parser always puts a node. Code that walks a tree
// calls [IsNil] rather than comparing a node with nil. It calls [Content]
// to reach the value behind a document, an anchor, a tag, or the `?` of
// an explicit key. Every walk then agrees on what a node holds.
//
//	if m, ok := astnode.Content(node).(*ast.MappingNode); ok {
//		// node is a mapping, or wraps one.
//	}
//
// The parser gives some documents a body that does not hold a value. Such
// a document is empty or holds only comments, only a directive, or only
// whitespace. Code that reads a document checks its body with
// [HasContent], so a decode and a path lookup agree on which documents
// are empty.
//
// A path points at one token of the document. [PathToken] returns that
// token for the node that the path selects and the [Parent] that holds
// the node. A scalar is its own token. A mapping or a sequence spans many
// lines, so it points at the token that introduces it. That token is the
// key of the entry whose value it is, or the "-" of the block sequence
// element that it is. Code that points at a node that it reached without
// a path calls PathToken too, so both name the same place for one node.
//
// A walk of a tree goes through [Inspect], which skips the typed nils
// that a hand-built tree might hold. The offsets that a node spans come
// from [ExtentOf], and the tokens at its ends from [Bounds]. Both count the
// token that closes a flow collection and the "-" of each block sequence
// element, even though the walk does not visit a node for either token.
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
// returns false for a scalar that holds the placeholder token of
// [tokens.Tokenize]. Tokenize makes that token for text that the lexer
// does not emit a token for, such as a file of whitespace alone.
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

// Parent names what holds a node in a tree: the mapping entry whose value
// is the node, or the sequence that holds the node as an element. The
// zero Parent holds the root of a document, the key of an entry, and any
// node whose place in the tree the caller does not know.
type Parent struct {
	// The entry whose value is the node, or nil.
	Entry *ast.MappingValueNode
	// The sequence that holds the node as an element, or nil.
	Sequence *ast.SequenceNode
	// The index of the node in Sequence.
	Index int
}

// PathToken returns the token that a path to node points at, where in
// holds node. A scalar is its own token, and so is an alias, whatever its
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
// An entry without a key does not introduce a token, so PathToken handles
// its value as it handles a value outside an entry. PathToken returns nil
// for a nil node, including a typed nil.
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

// KeyToken returns the token that a path to the key of entry points at,
// as a path that ends in a `~` selector resolves it. It leaves out the
// `?` of an explicit key, and looks through the anchors and tags on the
// key. It returns nil for a nil entry and for an entry without a key.
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
// stops at an alias, which is a token of its own. It does not look
// through a document or the `?` of an explicit key, but [Content] does.
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
// collection, and nil for a block collection. A tree built by hand might
// hold any token there, or nil.
func flowStart(flow bool, start *token.Token, kind token.Type) *token.Token {
	if !flow || start == nil || start.Type != kind {
		return nil
	}

	return start
}

// entryToken returns the "-" of element index of seq, or nil when seq
// does not write one for it. The parser does not give a flow sequence a
// token for its first element, and it gives the "," for each later one. A
// tree built by hand might not hold any entries.
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
// token. An entry without a key starts at its own token. It returns nil
// for a nil node, including a typed nil.
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

// Inspect calls f for node and for each node below it, in the order that
// [ast.Walk] visits them. That order puts a node before the nodes that it
// holds and the comment group of a node before its children. It goes
// below a node only when f returns true for it. It skips a nil node,
// including a typed nil, and everything below one.
func Inspect(node ast.Node, f func(ast.Node) bool) {
	ast.Walk(inspector(f), node)
}

// inspector is the [ast.Visitor] of [Inspect].
type inspector func(ast.Node) bool

// Visit implements [ast.Visitor].
func (f inspector) Visit(node ast.Node) ast.Visitor {
	if IsNil(node) || !f(node) {
		return nil
	}

	return f
}

// Nodes returns the nodes under node in the order that [Inspect] visits
// them, with node first. It leaves out comments.
func Nodes(node ast.Node) []ast.Node {
	var nodes []ast.Node

	Inspect(node, func(n ast.Node) bool {
		if n.Type() == ast.CommentType {
			return false
		}

		nodes = append(nodes, n)

		return true
	})

	return nodes
}

// Token returns the token of node, or nil for a nil node, including a
// typed nil.
func Token(node ast.Node) *token.Token {
	if IsNil(node) {
		return nil
	}

	return node.GetToken()
}

// Name returns the value of the token of node, which is the name that the
// decoder gives an anchor or resolves an alias by. It reports false for a
// node without a token.
func Name(node ast.Node) (string, bool) {
	tk := Token(node)
	if tk == nil {
		return "", false
	}

	return tk.Value, true
}

// Extent holds the offsets of the first and the last of some tokens. The
// zero Extent does not hold a token.
type Extent struct {
	// The offset of the first token.
	Lo int
	// The offset of the last token.
	Hi int
	// Whether the Extent holds a token.
	OK bool
}

// With returns e widened to hold the offset of tk. A nil token, or one
// without a position, does not change e.
func (e Extent) With(tk *token.Token) Extent {
	if tk == nil || tk.Position == nil {
		return e
	}

	off := tk.Position.Offset

	return e.Union(Extent{Lo: off, Hi: off, OK: true})
}

// Union returns the Extent that holds both e and o.
func (e Extent) Union(o Extent) Extent {
	switch {
	case !o.OK:
		return e
	case !e.OK:
		return o
	}

	return Extent{Lo: min(e.Lo, o.Lo), Hi: max(e.Hi, o.Hi), OK: true}
}

// Holds reports whether off lies in e.
func (e Extent) Holds(off int) bool {
	return e.OK && e.Lo <= off && off <= e.Hi
}

// ExtentOf returns the offsets of the first and the last token under
// node, as [Bounds] finds them. With comments unset, it leaves out the
// comments, as [StartToken] does. A comment group counts its first
// token.
func ExtentOf(node ast.Node, comments bool) Extent {
	return Extent{}.of(node, comments)
}

// of returns e widened to hold the tokens under node, as [ExtentOf]
// describes. It reads the nodes that [ast.Walk] reaches, in its own
// switch rather than through a visitor, so it does not allocate.
func (e Extent) of(node ast.Node, comments bool) Extent {
	if IsNil(node) {
		return e
	}

	if node.Type() == ast.CommentType {
		if comments {
			e = e.With(node.GetToken())
		}

		return e
	}

	e = e.With(node.GetToken())

	var base *ast.BaseNode

	switch n := node.(type) {
	case *ast.NullNode:
		base = n.BaseNode
	case *ast.IntegerNode:
		base = n.BaseNode
	case *ast.FloatNode:
		base = n.BaseNode
	case *ast.StringNode:
		base = n.BaseNode
	case *ast.MergeKeyNode:
		base = n.BaseNode
	case *ast.BoolNode:
		base = n.BaseNode
	case *ast.InfinityNode:
		base = n.BaseNode
	case *ast.NanNode:
		base = n.BaseNode
	case *ast.LiteralNode:
		base = n.BaseNode
		e = e.of(n.Value, comments)

	case *ast.DirectiveNode:
		base = n.BaseNode
		e = e.of(n.Name, comments)

		for _, value := range n.Values {
			e = e.of(value, comments)
		}

	case *ast.TagNode:
		base = n.BaseNode
		e = e.of(n.Value, comments)

	case *ast.DocumentNode:
		base = n.BaseNode
		e = e.of(n.Body, comments)

	case *ast.MappingNode:
		base = n.BaseNode
		e = e.With(n.End)

		for _, value := range n.Values {
			e = e.of(value, comments)
		}

	case *ast.MappingKeyNode:
		base = n.BaseNode
		e = e.of(n.Value, comments)

	case *ast.MappingValueNode:
		base = n.BaseNode
		e = e.of(n.Key, comments).of(n.Value, comments)

	case *ast.SequenceNode:
		base = n.BaseNode
		e = e.With(n.End)

		for _, entry := range n.Entries {
			if entry != nil {
				e = e.With(entry.Start)
			}
		}

		for _, value := range n.Values {
			e = e.of(value, comments)
		}

	case *ast.AnchorNode:
		base = n.BaseNode
		e = e.of(n.Name, comments).of(n.Value, comments)

	case *ast.AliasNode:
		base = n.BaseNode
		e = e.of(n.Value, comments)
	}

	if comments && base != nil && base.Comment != nil {
		e = e.With(base.Comment.GetToken())
	}

	return e
}

// Bounds returns the tokens under node with the lowest and the highest
// offset, comments included, each in walk order, or nil when none of the
// tokens under node carries a position. Several tokens can share an
// offset, so each bound can hold more than one.
func Bounds(node ast.Node) (token.Tokens, token.Tokens) {
	var b boundsFinder

	Inspect(node, b.visit)

	return b.first, b.last
}

// StartToken returns the token with the lowest offset under node that is
// not a comment, or nil when none of those tokens carries a position.
func StartToken(node ast.Node) *token.Token {
	b := boundsFinder{skipComments: true}

	Inspect(node, b.visit)

	if len(b.first) == 0 {
		return nil
	}

	return b.first[0]
}

// boundsFinder records the tokens with the lowest and the highest offset
// among the nodes that it visits, all of them when several share the
// offset.
// With skipComments set, it leaves out the comments.
type boundsFinder struct {
	first, last  token.Tokens
	skipComments bool
}

// visit is the function that [Inspect] calls for each node.
func (b *boundsFinder) visit(node ast.Node) bool {
	if b.skipComments && node.Type() == ast.CommentType {
		return false
	}

	b.consider(node.GetToken())

	// The walk visits the nodes of a collection, not the token that closes
	// a flow collection or the "-" that opens each entry of a block
	// sequence, which the node holds beside them. An entry whose value is
	// an implicit null shares the offset of its "-", which then ends the
	// sequence.
	switch n := node.(type) {
	case *ast.SequenceNode:
		b.consider(n.End)

		for _, entry := range n.Entries {
			if entry != nil {
				b.consider(entry.Start)
			}
		}

	case *ast.MappingNode:
		b.consider(n.End)
	}

	return true
}

// consider widens the bounds to tk, or adds tk to the tokens at a bound
// that it shares the offset of. A nil token, or one without a position,
// does not change the bounds.
func (b *boundsFinder) consider(tk *token.Token) {
	if tk == nil || tk.Position == nil {
		return
	}

	off := tk.Position.Offset

	switch {
	case len(b.first) == 0 || off < b.first[0].Position.Offset:
		b.first = append(b.first[:0], tk)
	case off == b.first[0].Position.Offset:
		b.first = append(b.first, tk)
	}

	switch {
	case len(b.last) == 0 || off > b.last[0].Position.Offset:
		b.last = append(b.last[:0], tk)
	case off == b.last[0].Position.Offset:
		b.last = append(b.last, tk)
	}
}

// IsMergeKey reports whether key is a `<<` merge key. It looks through
// the `?` indicator, anchors, and tags. A nil key, including a typed nil,
// is not a merge key.
func IsMergeKey(key ast.Node) bool {
	_, ok := Content(key).(*ast.MergeKeyNode)

	return ok
}

// MergeValues returns the nodes that the `<<` merge key of entry merges,
// the value of the entry or each element of a sequence there, under their
// anchors and tags. An alias stays an alias. It returns nil when entry
// does not hold a merge key.
func MergeValues(entry *ast.MappingValueNode) []ast.Node {
	if entry == nil || !IsMergeKey(entry.Key) {
		return nil
	}

	value := Content(entry.Value)

	seq, ok := value.(*ast.SequenceNode)
	if !ok {
		return []ast.Node{value}
	}

	values := make([]ast.Node, 0, len(seq.Values))
	for _, elem := range seq.Values {
		values = append(values, Content(elem))
	}

	return values
}

// Entries returns the entries of the mapping that node holds, under its
// anchors and tags, and reports whether node holds a mapping. A
// [*ast.MappingValueNode] is a mapping of one entry.
func Entries(node ast.Node) ([]*ast.MappingValueNode, bool) {
	switch n := Content(node).(type) {
	case *ast.MappingNode:
		return n.Values, true
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}, true
	default:
		return nil, false
	}
}

// IsTaggedNull reports whether node, or the value that an anchor on node
// names, is a tag that the go-yaml decoder reads as null. A "!!null" tag
// over any value is such a tag. So is a tag without a value, such as
// "!!seq", "!!map", or a local tag. The go-yaml Unmarshal reads a document
// whose body is a tagged null as empty. A tag that converts its value,
// such as "!!str" or "!!int", reads as a value even when it does not wrap
// a value. So does any tag after a %TAG directive that redefines the "!!"
// handle.
func IsTaggedNull(node ast.Node) bool {
	if anchor, ok := node.(*ast.AnchorNode); ok {
		node = anchor.Value
	}

	tag, ok := node.(*ast.TagNode)
	if !ok || tag.Start == nil || tag.Directive != nil {
		return false
	}

	switch token.ReservedTagKeyword(tag.Start.Value) {
	case token.NullTag:
		return true

	case token.StringTag, token.IntegerTag, token.FloatTag, token.BooleanTag,
		token.TimestampTag, token.BinaryTag:
		return false

	default:
		return IsNil(tag.Value) || tag.Value.Type() == ast.NullType
	}
}

// ScalarText returns the string that a decode reads from node, a scalar
// under its anchors and tags, before a tag converts it. That string is the
// value of the token of the scalar, with its quotes, its escapes, and its
// folding resolved. The token of a block scalar is its header, such as
// "|-". The string of a block scalar is its content instead, with the
// indentation, the folding, and the chomping of the block applied.
// ScalarText reports false for a node that does not hold a scalar: nil, a
// mapping, a sequence, an alias, and the null of an entry or an item
// without a value.
func ScalarText(node ast.Node) (string, bool) {
	switch n := Content(node).(type) {
	case nil, *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode, *ast.AliasNode:
		return "", false

	case *ast.LiteralNode:
		if n.Value == nil {
			return "", true
		}

		return n.Value.Value, true

	default:
		tk := Token(n)
		if tk == nil || tk.Type == token.ImplicitNullType {
			return "", false
		}

		return tk.Value, true
	}
}

// IsScalar reports whether node is any node but a mapping, a sequence, or
// nil. A node that wraps another, such as an anchor or a tag, counts as
// a scalar whatever it holds.
func IsScalar(node ast.Node) bool {
	switch node.(type) {
	case nil, *ast.MappingNode, *ast.SequenceNode:
		return false
	default:
		return true
	}
}
