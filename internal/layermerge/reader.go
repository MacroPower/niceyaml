package layermerge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/reftable"
	"go.jacobcolvin.com/niceyaml/paths"
)

// errNoScalarText is the error of a node that a merge cannot write into
// the merged document, because it is not a collection and does not hold
// text.
var errNoScalarText = errors.New("node has no text to merge")

// Reader reads the value of one layer as the go-yaml decoder reads it,
// into a [Value] without aliases or `<<` merge keys.
//
// An alias reads the anchor that the [paths.Resolver] of the document
// binds it to. A decode of the layer reads the same anchor. When the
// resolver does not bind an alias to an anchor, the alias reads the
// anchor that the [*reftable.Table] of the reference documents of the
// layer holds for its name. Every alias inside a reference document reads
// its anchor the same way. An alias inside the anchor it refers to reads
// as null, as the decoder reads it.
type Reader struct {
	// Ctx cancels the read. The read can run long, because it copies the
	// anchor of each alias in full.
	Ctx context.Context
	// Resolver resolves the aliases and the key names of the layer.
	Resolver *paths.Resolver
	// Enclosed holds each alias of the layer that lies inside the anchor
	// it refers to. Each such alias reads as null.
	Enclosed map[*ast.AliasNode]bool
	// UnnamedKey is the error of a key without a name. The read wraps it
	// in a [NodeError].
	UnnamedKey error
	// The anchors whose content the reader is inside.
	open map[*ast.AnchorNode]bool
	// References holds the anchors of the reference documents of the
	// layer, or nil when the layer does not have any.
	References *reftable.Table
	// Layer is the index of the layer among the layers the merge reads.
	// Each value the read returns holds it as [Value.Layer].
	Layer int
}

// NodeError is the error of a node the reader cannot merge: an alias
// without an anchor, a scalar without text, or a key without a name.
type NodeError struct {
	// Err is the reason the reader cannot merge the node.
	Err error
	// Token is the token the error lies at. It is nil inside a reference
	// document, because a node there does not have a position in the
	// layer.
	Token *token.Token
}

// Error returns the message of Err.
func (e *NodeError) Error() string {
	return e.Err.Error()
}

// Unwrap returns Err.
func (e *NodeError) Unwrap() error {
	return e.Err
}

// Read returns the value of node, the body of the document of the layer.
// It returns a [*NodeError] for a node it cannot merge, and the error of
// Ctx when Ctx ends.
func (r *Reader) Read(node ast.Node) (*Value, error) {
	if r.open == nil {
		r.open = map[*ast.AnchorNode]bool{}
	}

	return r.value(node, false)
}

// value returns the value under node. The ref flag says that node lies in
// a reference document, where an alias reads only the reference
// documents. It returns the error of Ctx when Ctx is done. It checks Ctx
// for each value it reads, because the read copies the anchor of each
// alias in full.
func (r *Reader) value(node ast.Node, ref bool) (*Value, error) {
	err := r.Ctx.Err()
	if err != nil {
		return nil, err
	}

	var v *Value

	err = r.follow(node, ref, "", func(content ast.Node, tag string, ref bool) error {
		var err error

		v, err = r.content(content, ref)
		if err != nil {
			return err
		}

		v.tag = tag

		return nil
	})

	return v, err
}

// follow calls visit with the content behind the anchors, aliases, tags,
// and `?` indicators under node. It also passes whether that content lies
// in a reference document, and a tag. The tag is outer, or else the first
// tag on the way that [tagsNoValue] does not report. Each anchor on the
// way stays open while visit runs. An alias inside its own anchor gives
// nil content.
func (r *Reader) follow(
	node ast.Node, ref bool, outer string, visit func(content ast.Node, tag string, ref bool) error,
) error {
	if astnode.IsNil(node) {
		return visit(nil, outer, ref)
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		if r.open[n] {
			return visit(nil, outer, ref)
		}

		r.open[n] = true
		defer delete(r.open, n)

		return r.follow(n.Value, ref, outer, visit)

	case *ast.AliasNode:
		// An alias that lies inside the anchor it refers to reads as null
		// wherever the reader reaches it from. The open anchors alone miss
		// one that a `<<` merge key brought in, since mapping reads that
		// value once the anchor has closed.
		if !ref && r.Enclosed[n] {
			return visit(nil, outer, ref)
		}

		anchor, inRef, err := r.anchor(n, ref)
		if err != nil {
			return err
		}

		return r.follow(anchor, inRef, outer, visit)

	case *ast.TagNode:
		if outer == "" && !tagsNoValue(n) {
			outer = n.GetToken().Value
		}

		return r.follow(n.Value, ref, outer, visit)

	case *ast.MappingKeyNode:
		return r.follow(n.Value, ref, outer, visit)
	case *ast.DocumentNode:
		return r.follow(n.Body, ref, outer, visit)
	default:
		return visit(node, outer, ref)
	}
}

// tagsNoValue reports whether tag stands over a missing or null value and
// the go-yaml decoder reads it as null, as [astnode.IsTaggedNull]
// describes. The merged document writes that null as text. Behind a local
// tag the text reads as a string, and behind a tag such as "!!map" it
// does not parse.
func tagsNoValue(tag *ast.TagNode) bool {
	return astnode.IsTaggedNull(tag) && (astnode.IsNil(tag.Value) || tag.Value.Type() == ast.NullType)
}

// anchor returns the anchor alias reads, and whether that anchor lies in
// a reference document. An alias of the document reads the anchor the
// resolver binds it to. An alias that the resolver does not bind to an
// anchor reads the anchor that References holds for its name. Every alias
// of a reference document reads that anchor too.
func (r *Reader) anchor(alias *ast.AliasNode, ref bool) (*ast.AnchorNode, bool, error) {
	if !ref {
		found, err := r.Resolver.Anchor(alias)
		if anchor, ok := found.(*ast.AnchorNode); err == nil && ok && anchor != nil {
			return anchor, false, nil
		}
	}

	name, _ := astnode.Name(alias.Value)

	anchor, ok := r.References.Anchor(name)
	if !ok {
		err := fmt.Errorf("%w: *%s has no anchor to read", paths.ErrAlias, name)

		return nil, false, r.invalid(err, alias, ref)
	}

	return anchor, true, nil
}

// content returns the value of content, a node [Reader.follow] found.
func (r *Reader) content(content ast.Node, ref bool) (*Value, error) {
	v := &Value{Layer: r.Layer}

	switch n := content.(type) {
	case nil:
		v.text = "null"
	case *ast.NullNode:
		tk := n.GetToken()

		v.text, v.written = tk.Value, tk.Type == token.NullType

	case *ast.MappingNode, *ast.MappingValueNode:
		entries, _ := astnode.Entries(content)

		return v, r.mapping(v, entries, ref)

	case *ast.SequenceNode:
		v.kind = kindSequence

		for _, elem := range n.Values {
			item, err := r.value(elem, ref)
			if err != nil {
				return nil, err
			}

			v.items = append(v.items, item)
		}

	default:
		text, ok := scalarText(content)
		if !ok {
			return nil, r.invalid(errNoScalarText, content, ref)
		}

		v.kind, v.text = kindScalar, text
	}

	return v, nil
}

// invalid returns err as the [*NodeError] of node, a node the layer
// cannot merge: an alias, a scalar, or the key of an entry. The error lies
// at the token that [astnode.PathToken] gives node without a parent. A
// node of a reference document does not have a position in the layer, so
// its error does not lie at a token.
func (*Reader) invalid(err error, node ast.Node, ref bool) error {
	if ref {
		return &NodeError{Err: err}
	}

	return &NodeError{Err: err, Token: astnode.PathToken(astnode.Content(node), astnode.Parent{})}
}

// flatEntry is an entry of a mapping as [Reader.flatten] finds it:
// the key and the value, and whether they lie in a reference document.
type flatEntry struct {
	key   ast.Node
	value ast.Node
	ref   bool
}

// mapping fills v with the entries of a mapping: its own entries and the
// entries that its `<<` merge keys bring in. It reads the values of only
// the entries that the mapping keeps.
func (r *Reader) mapping(v *Value, entries []*ast.MappingValueNode, ref bool) error {
	v.kind, v.names = kindMapping, map[string]int{}

	var order []string

	kept := map[string]flatEntry{}

	err := r.flatten(entries, ref, func(name string, entry flatEntry) {
		if _, ok := kept[name]; !ok {
			order = append(order, name)
		}

		kept[name] = entry
	})
	if err != nil {
		return err
	}

	for _, name := range order {
		entry := kept[name]

		key, err := r.key(entry.key, entry.ref)
		if err != nil {
			return err
		}

		value, err := r.value(entry.value, entry.ref)
		if err != nil {
			return err
		}

		v.names[name] = len(v.entries)
		v.entries = append(v.entries, &mapEntry{name: name, key: key, value: value})
	}

	return nil
}

// flatten calls keep with each entry of entries under its key name, in
// document order. It calls keep with the entries of each `<<` merge key
// at the place where that key stands. The go-yaml decoder sets the
// entries of a mapping in that order. So the last call for a name holds
// the entry that the decoder keeps, as [paths.Resolver.Entry] finds it.
// It returns an error for a key without a name.
func (r *Reader) flatten(entries []*ast.MappingValueNode, ref bool, keep func(string, flatEntry)) error {
	for _, entry := range entries {
		if entry == nil {
			continue
		}

		if astnode.IsMergeKey(entry.Key) {
			err := r.merged(entry.Value, ref, keep)
			if err != nil {
				return err
			}

			continue
		}

		name, ok := r.Resolver.KeyName(entry.Key)
		if !ok {
			return r.invalid(r.UnnamedKey, entry.Key, ref)
		}

		keep(name, flatEntry{key: entry.Key, value: entry.Value, ref: ref})
	}

	return nil
}

// merged calls keep with the entries the value of a `<<` merge key brings
// in. The value is a mapping, or a sequence of mappings that apply in
// order. It skips any other value, and each element of the sequence
// that is not a mapping.
func (r *Reader) merged(value ast.Node, ref bool, keep func(string, flatEntry)) error {
	source := func(content ast.Node, _ string, ref bool) error {
		if entries, ok := astnode.Entries(content); ok {
			return r.flatten(entries, ref, keep)
		}

		return nil
	}

	return r.follow(value, ref, "", func(content ast.Node, tag string, ref bool) error {
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return source(content, tag, ref)
		}

		for _, elem := range seq.Values {
			err := r.follow(elem, ref, "", source)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// key returns the YAML text of key, the key of a mapping entry, with its
// tag in front.
func (r *Reader) key(key ast.Node, ref bool) (string, error) {
	var text string

	err := r.follow(key, ref, "", func(content ast.Node, tag string, _ bool) error {
		scalar, ok := scalarText(content)
		if !ok {
			return r.invalid(r.UnnamedKey, key, ref)
		}

		text = scalar
		if tag != "" {
			text = tag + " " + scalar
		}

		return nil
	})

	return text, err
}

// scalarText returns the YAML text that reads as the scalar node on one
// line of a block collection. It reports false for a node that is not a
// scalar. A scalar that is not a string keeps the text of the source,
// such as 0x10 or 1.50. A string keeps its text and its quotes where they
// read the same on one line, and takes double quotes otherwise.
func scalarText(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case nil:
		return "", false

	case *ast.LiteralNode:
		if n.Value == nil {
			return `""`, true
		}

		return strconv.Quote(n.Value.Value), true

	case *ast.StringNode:
		return stringText(n), true

	case ast.ScalarNode:
		if tk := n.GetToken(); tk != nil {
			return tk.Value, true
		}
	}

	return "", false
}

// stringText returns the YAML text of the string n, as [scalarText]
// describes.
func stringText(n *ast.StringNode) string {
	tk := n.GetToken()

	switch {
	case tk == nil, tk.Type == token.DoubleQuoteType, strings.ContainsFunc(n.Value, notPrintable):
		return strconv.Quote(n.Value)
	case tk.Type == token.SingleQuoteType:
		return "'" + strings.ReplaceAll(n.Value, "'", "''") + "'"
	case !plainSafe(n.Value):
		return strconv.Quote(n.Value)
	default:
		return n.Value
	}
}

// notPrintable reports whether r is a character a quoted scalar writes as
// an escape, such as a line break or a tab.
func notPrintable(r rune) bool {
	return !unicode.IsPrint(r)
}

// plainSafe reports whether the text s of a plain scalar reads as that
// scalar on a line of a block collection, as a key or as a value. A flow
// collection accepts some plain scalars that a block collection reads as
// something else. The root of a document reads "---" and "..." as
// markers.
func plainSafe(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}

	if strings.ContainsAny(s[:1], "[]{},#&*!|>'\"%@`") {
		return false
	}

	for _, indicator := range []string{"-", "?", ":"} {
		if s == indicator || strings.HasPrefix(s, indicator+" ") {
			return false
		}
	}

	return !strings.HasPrefix(s, "---") && !strings.HasPrefix(s, "...") &&
		!strings.Contains(s, ": ") && !strings.Contains(s, " #") && !strings.HasSuffix(s, ":")
}
