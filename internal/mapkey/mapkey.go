// Package mapkey names the keys of the mappings of a YAML document as
// niceyaml's decode names them in a decode into a map.
//
// The decoder names an entry by the value that it decodes for the key. That
// name is the string of a string, "null" for a null, and the print of any
// other value, such as "map[a:1]" for a mapping. A key that holds no alias
// reads no anchor, so its decode does not depend on its place. The keys of
// one text share their decodes.
//
// An alias in a key reads the anchor that
// [go.jacobcolvin.com/niceyaml/paths.Resolver] binds it to. The decode of
// niceyaml reads each alias that way, through the decode tree of the
// document. That tree gives each anchor that shares its name a name of its
// own. An alias inside the value of the anchor that it reads reads null. The
// binding of an alias does not depend on where a decode reads the key. So a
// key that a `<<` merge key brings in reads the anchors that it reads at its
// own place. The decoder of go-yaml binds some reused anchor names
// otherwise, as the documentation of the paths package describes. In
// "b: &b {*b : 1}" and "m: {<<: *b}", niceyaml names the key of m "null",
// and go-yaml names it "map[null:1]".
//
// A [Keys] hands each key that holds an alias to the go-yaml decoder in
// the form that niceyaml's decode reads it, after the anchors that the
// decode reads before the key. A [Viewer] gives that form and those
// anchors. A key of a reference document, which a `<<` merge key brings
// in, reads the anchors of the reference documents alone, as the decode
// reads the anchor that holds it. A key that a program writes from decoded
// values holds no alias of the document, and [Keys.KeyOf] names it. A
// panic of the decoder on a key makes the key one that does not decode,
// and [Keys.Panic] gives that panic.
package mapkey

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// Viewer returns node, a node that a decode of a document reads, as
// niceyaml's decode of the document reads it, with the anchors that the
// decoder reads before node, in order. Those are the anchors of the
// reference documents first and then the anchors of the document that the
// aliases of node need. Viewer reports false for a node outside the
// document, such as a node of a reference document, and returns no
// anchors for it.
type Viewer func(node ast.Node) (ast.Node, []ast.Node, bool)

// Primer returns the anchors of the reference documents that a decoder
// reads, in order, before it decodes a node whose aliases read names, as
// the Source of the document primes a decode. It returns nil when the
// document does not read reference documents.
type Primer func(names []string) []ast.Node

// Keys names the keys of one parse of a document. It keeps the decode of
// each key that it reads, so a parse decodes each key once. A Keys is not
// safe for concurrent use.
//
// Create instances with [NewKeys].
type Keys struct {
	// The body of the document, the view of its nodes, and the reference
	// anchors of its Source.
	body ast.Node
	view Viewer
	refs Primer
	// The decodes of the scalar keys that hold no alias, which [Keys.Share]
	// can give other Keys too.
	plain *Plain
	// The decode of each key that holds no alias and is not a scalar, and
	// of each key that holds an alias, by its node.
	nodes map[ast.Node]result
	// The panic of the decoder on the key of the last read, or nil.
	panic *astnode.PanicError
}

// Plain holds the decodes of the scalar keys that hold no alias, by their
// text. Such a key reads no anchor, so its decode does not depend on the
// parse that holds it, and the [Keys] of several parses of a document can
// share one Plain. A key whose tag names a directive of its document
// decodes by that directive, so each parse keeps its own decode of such a
// key. A Plain is not safe for concurrent use.
//
// Create instances with [NewPlain].
type Plain struct {
	// The decoder of the keys, which reads no anchor.
	dec   *yaml.Decoder
	texts map[scalarKey]result
	// The value that a decode into any reads from each scalar value that
	// decodes, by its text.
	values map[scalarKey]any
}

// NewPlain creates a new [*Plain].
func NewPlain() *Plain {
	return &Plain{texts: make(map[scalarKey]result), values: make(map[scalarKey]any)}
}

// Value returns the value that a decode into any reads from node, a value
// that holds no alias, or the error of that decode, as
// [astnode.NodeToValue] gives it. A scalar without an alias reads no
// anchor, so its decode does not depend on the parse that holds it, as for
// a key, and p keeps the value of each scalar that decodes. A scalar whose
// tag names a directive of its document decodes on each call, and so does
// a node that does not hold a scalar. A nil Plain keeps no value.
func (p *Plain) Value(node ast.Node) (any, error) {
	text, scalar := textOf(node)
	kept := p != nil && scalar && !text.directive()

	if kept {
		if v, ok := p.values[text]; ok {
			return v, nil
		}
	}

	var v any

	err := astnode.NodeToValue(node, &v)
	if err == nil && kept {
		p.values[text] = v
	}

	return v, err
}

// key returns the decode of key, a scalar key without an alias whose text
// is text.
func (p *Plain) key(key ast.Node, text scalarKey) result {
	if got, ok := p.texts[text]; ok {
		return got
	}

	if p.dec == nil {
		p.dec = yaml.NewDecoder(&bytes.Buffer{})
	}

	got := decoded(decodeKey(p.dec, key))
	p.texts[text] = got

	return got
}

// result is the key that a decode names, whether the key decodes, and the
// panic of the decoder on the key, or nil.
type result struct {
	panic *astnode.PanicError
	key   string
	ok    bool
}

// decoded returns the result of a decode of a key that gave k and err.
func decoded(k string, err error) result {
	got := result{key: k, ok: err == nil}
	got.panic, _ = errors.AsType[*astnode.PanicError](err)

	return got
}

// NewKeys creates a new [*Keys] for body, the body of a document. The
// argument view gives the nodes of the document as niceyaml's decode reads
// them, and refs gives the anchors of the reference documents. With a nil
// view, a key that holds an alias does not decode. A nil refs reads no
// reference documents.
func NewKeys(body ast.Node, view Viewer, refs Primer) *Keys {
	return &Keys{
		body:  body,
		view:  view,
		refs:  refs,
		plain: NewPlain(),
		nodes: make(map[ast.Node]result),
	}
}

// Share makes r read and keep the decodes of its scalar keys that hold no
// alias in p, as [Plain] describes, in place of a Plain of its own.
func (r *Keys) Share(p *Plain) {
	r.plain = p
}

// Plain returns the [*Plain] that r reads and keeps its decodes in.
func (r *Keys) Plain() *Plain {
	return r.plain
}

// Panic returns the panic of the go-yaml decoder on the key that the last
// call of [Keys.Key] or [Keys.KeyOf] read, or nil when the decoder did not
// panic on it.
func (r *Keys) Panic() *astnode.PanicError {
	return r.panic
}

// Body returns the body of the document of r.
func (r *Keys) Body() ast.Node {
	return r.body
}

// Key returns the key that a decode into a map gives an entry whose key
// node is key, a node that a decode of the document reads. That node can
// lie in a reference document, whose aliases read the anchors of the
// reference documents alone, as [Keys.KeyOf] reads them. Key reports false
// for a key that does not decode, such as an alias that does not find an
// anchor.
func (r *Keys) Key(key ast.Node) (string, bool) {
	got := r.key(key)
	r.panic = got.panic

	return got.key, got.ok
}

// key returns the decode of key, as [Keys.Key] describes.
func (r *Keys) key(key ast.Node) result {
	if astnode.IsNil(key) {
		return result{}
	}

	if !HoldsAlias(key) {
		return r.plainKey(key)
	}

	if got, ok := r.nodes[key]; ok {
		return got
	}

	got := r.viewedKey(key)
	r.nodes[key] = got

	return got
}

// KeyOf returns the key that a decode into a map gives an entry whose key
// is node, a node outside the document. The aliases of node read the
// anchors inside it, such as those of [NewAnchor]. They also read the
// anchors of the reference documents. KeyOf reports false for a key that
// does not decode.
func (r *Keys) KeyOf(node ast.Node) (string, bool) {
	got := r.keyOf(node)
	r.panic = got.panic

	return got.key, got.ok
}

// keyOf returns the decode of node, as [Keys.KeyOf] describes.
func (r *Keys) keyOf(node ast.Node) result {
	if astnode.IsNil(node) {
		return result{}
	}

	dec, err := r.outside(node)
	if err != nil {
		return decoded("", err)
	}

	return decoded(decodeKey(dec, node))
}

// ValueOf returns what a decode into any reads for node, a node outside
// the document. The aliases of node read the anchors inside it and the
// anchors of the reference documents, as [Keys.KeyOf] reads them. ValueOf
// returns the error of a decode that fails, and a panic of the decoder
// comes back as an [*astnode.PanicError].
func (r *Keys) ValueOf(node ast.Node) (any, error) {
	dec, err := r.outside(node)
	if err != nil {
		return nil, err
	}

	var v any

	err = astnode.DecodeNode(dec, node, &v)

	return v, err //nolint:wrapcheck // The caller names the node.
}

// outside returns a decoder for node, a node outside the document, that
// holds the anchors of the reference documents that the aliases of node
// read. An alias of an anchor inside node reads that anchor.
func (r *Keys) outside(node ast.Node) (*yaml.Decoder, error) {
	dec := yaml.NewDecoder(&bytes.Buffer{})
	if r.refs == nil {
		return dec, nil
	}

	own := anchorNames(node)

	var names []string

	for _, name := range aliasNames(node) {
		if !slices.Contains(own, name) {
			names = append(names, name)
		}
	}

	return dec, primed(dec, r.refs(names))
}

// plainKey returns the key of key, a key without an alias, which reads no
// anchor.
func (r *Keys) plainKey(key ast.Node) result {
	text, scalar := textOf(key)
	if scalar && !text.directive() {
		return r.plain.key(key, text)
	}

	if got, ok := r.nodes[key]; ok {
		return got
	}

	if r.plain.dec == nil {
		r.plain.dec = yaml.NewDecoder(&bytes.Buffer{})
	}

	got := decoded(decodeKey(r.plain.dec, key))
	r.nodes[key] = got

	return got
}

// viewedKey decodes key, a key that holds an alias, as niceyaml's decode
// reads it: the view of the key, after the anchors that the decode reads
// before it.
func (r *Keys) viewedKey(key ast.Node) result {
	if r.view == nil {
		return result{}
	}

	view, anchors, inside := r.view(key)
	if !inside {
		return r.keyOf(view)
	}

	dec := yaml.NewDecoder(&bytes.Buffer{})

	err := primed(dec, anchors)
	if err != nil {
		return decoded("", err)
	}

	return decoded(decodeKey(dec, view))
}

// primed decodes each of nodes with dec, in order, so that dec holds their
// anchors. It returns the error of the first node that does not decode.
func primed(dec *yaml.Decoder, nodes []ast.Node) error {
	for _, n := range nodes {
		var sink any

		err := astnode.DecodeNode(dec, n, &sink)
		if err != nil {
			return err
		}
	}

	return nil
}

// StringOf returns the string that go-yaml's decoder reads from node into
// a string, under the tags of node, and reports false when the decoder
// reads node as null. So "!!null x" reads as null, and the number 2 reads
// "2". It returns the error of a decode that does not read a string, such
// as the decode of a mapping or of a timestamp.
func StringOf(node ast.Node) (string, bool, error) {
	var v any

	err := astnode.NodeToValue(node, &v)
	if err != nil || v == nil {
		return "", false, err
	}

	var s string

	err = astnode.NodeToValue(node, &s)
	if err != nil {
		return "", false, err
	}

	return s, true, nil
}

// decodeKey decodes a mapping of key to null with dec, and returns the key
// of the entry of the decode.
func decodeKey(dec *yaml.Decoder, key ast.Node) (string, error) {
	mk, ok := key.(ast.MapKeyNode)
	if !ok {
		explicit := ast.MappingKey(token.New("?", "?", &token.Position{}))
		explicit.Value = key
		mk = explicit
	}

	var v any

	err := astnode.DecodeNode(dec, ast.MappingValue(colon, mk, null), &v)
	if dp, ok := errors.AsType[*astnode.PanicError](err); ok {
		dp.Node = key
	}

	if err != nil {
		return "", fmt.Errorf("decode the key: %w", err)
	}

	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", fmt.Errorf("%w: %T", errNotKey, v)
	}

	for k := range m {
		return k, nil
	}

	return "", errNotKey
}

var (
	// The error of a node that does not decode as a key.
	errNotKey = errors.New("not a key")

	// The ":" and the null value of the mapping of each decode of a key. The
	// decoder does not write to the nodes that it reads, so the decodes
	// share them.
	colon = token.New(":", ":", &token.Position{})
	null  = ast.Null(token.New("null", "null", &token.Position{}))
)

// Name returns a name of an anchor that no anchor of a parsed text spells,
// because it holds a space. A key that [Keys.KeyOf] names can give its
// values such names and alias them. The names of distinct n differ.
func Name(n int) string {
	return fmt.Sprintf("mapkey %d", n)
}

// NewAlias returns an alias of the anchor name.
func NewAlias(name string) *ast.AliasNode {
	pos := &token.Position{}
	alias := ast.Alias(token.New("*", "*", pos))
	alias.Value = ast.String(&token.Token{Type: token.StringType, Value: name, Origin: name, Position: pos})

	return alias
}

// NewAnchor returns value under an anchor of the name name.
func NewAnchor(name string, value ast.Node) *ast.AnchorNode {
	pos := &token.Position{}
	anchor := ast.Anchor(token.New("&", "&", pos))
	anchor.Name = ast.String(&token.Token{Type: token.StringType, Value: name, Origin: name, Position: pos})
	anchor.Value = value

	return anchor
}

// HoldsAlias reports whether an alias lies under node.
func HoldsAlias(node ast.Node) bool {
	for {
		switch x := node.(type) {
		case *ast.MappingKeyNode:
			node = x.Value
		case *ast.AnchorNode:
			node = x.Value
		case *ast.TagNode:
			node = x.Value
		case *ast.AliasNode:
			return true
		case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
			found := false

			astnode.Inspect(x, func(n ast.Node) bool {
				_, alias := n.(*ast.AliasNode)
				found = found || alias

				return !found
			})

			return found

		default:
			return false
		}
	}
}

// aliasNames returns the names that the aliases under node read, each
// once, in the order of the text.
func aliasNames(node ast.Node) []string {
	var names []string

	astnode.Inspect(node, func(n ast.Node) bool {
		if alias, ok := n.(*ast.AliasNode); ok {
			if name, ok := astnode.Name(alias.Value); ok && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}

		return true
	})

	return names
}

// anchorNames returns the names of the anchors under node.
func anchorNames(node ast.Node) []string {
	var names []string

	astnode.Inspect(node, func(n ast.Node) bool {
		if anchor, ok := n.(*ast.AnchorNode); ok {
			if name, ok := astnode.Name(anchor.Name); ok {
				names = append(names, name)
			}
		}

		return true
	})

	return names
}

// scalarKey is what the decode of a scalar key reads. It holds the text of
// each tag on the key, then the kind and the text of the scalar under its
// tags and its anchors.
type scalarKey [3]string

// directive reports whether a tag of k names a directive of its document.
func (k scalarKey) directive() bool {
	return strings.Contains(k[0], directiveMark)
}

// directiveMark follows the text of a tag of a [scalarKey] that names a
// directive.
const directiveMark = " %"

// textOf returns the [scalarKey] of key. It reports false for a key that
// does not hold a scalar.
func textOf(key ast.Node) (scalarKey, bool) {
	var (
		k    scalarKey
		tags strings.Builder
		text string
	)

	node := key

	for {
		switch x := node.(type) {
		case *ast.MappingKeyNode:
			node = x.Value

			continue

		case *ast.AnchorNode:
			node = x.Value

			continue

		case *ast.TagNode:
			if x == nil || x.Start == nil {
				return k, false
			}

			tags.WriteString(x.Start.Value)

			if x.Directive != nil {
				tags.WriteString(directiveMark)
			}

			tags.WriteByte(0)

			node = x.Value

			continue

		case *ast.StringNode:
			if x == nil {
				return k, false
			}

			text = x.Value

		case *ast.LiteralNode:
			if x == nil || x.Value == nil {
				return k, false
			}

			text = x.Value.Value

		case *ast.NullNode, *ast.BoolNode, *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
			tk := astnode.Token(x)
			if tk == nil {
				return k, false
			}

			text = tk.Value

		default:
			return k, false
		}

		return scalarKey{tags.String(), node.Type().String(), text}, true
	}
}
