package edit

import (
	"context"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// flowRun returns node, a value of the current text, in flow style, as an
// edit writes it in a flow collection. Each anchor, tag, alias, and scalar
// of one line keeps its text, with the origins of its bytes, unless it is a
// plain string that go-yaml's flow encoder writes otherwise. Each mapping
// keeps the order of its entries. A block scalar, a string of several
// lines, and such a plain string take the text of the encoder, as
// [encodedString] gives it in flow style. An empty value becomes null. The
// comments inside node go. The bytes that flowRun adds, such as the braces,
// take origin.
func (d *Doc) flowRun(node ast.Node, origin int) run {
	return d.flowRunOf(node, origin, &flowStyle{})
}

// flowStyle sets how [Doc.flowRunOf] writes the scalars of a node.
type flowStyle struct {
	// Encoded reports whether the node is go-yaml's parse of an encode. A
	// plain string of such a node holds a Go string only where it holds the
	// text that the encoder writes for a Go string, as [Doc.flowScalar]
	// reads it.
	encoded bool
}

// flowRunOf returns node in flow style, as [Doc.flowRun] does with the
// scalars that st writes.
func (d *Doc) flowRunOf(node ast.Node, origin int, st *flowStyle) run {
	return d.flowNode(node, origin, st, false)
}

// flowNode returns node in flow style, as [Doc.flowRunOf] does. A key is
// true for the key of an entry, whose string takes the text of a key.
func (d *Doc) flowNode(node ast.Node, origin int, st *flowStyle, key bool) run {
	var b runBuilder

	d.writeFlow(&b, node, origin, st, key)

	return b.run()
}

// writeFlow writes node in flow style to b, as [Doc.flowNode] returns it.
// The text of a collection grows with each entry or item, so the parts go
// to one builder.
func (d *Doc) writeFlow(b *runBuilder, node ast.Node, origin int, st *flowStyle, key bool) {
	if astnode.IsNil(node) || isImplicitNull(node) {
		b.add(newRun("null", origin))

		return
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		props, ok := d.propsSpan(n)
		if !ok {
			d.writeFlow(b, n.Value, origin, st, key)

			return
		}

		d.writeProps(b, d.keptRun(props.start, props.end), n.Value, st, key)

	case *ast.TagNode:
		props, ok := d.tokenSpan(n.Start)
		if !ok {
			d.writeFlow(b, n.Value, origin, st, key)

			return
		}

		// An empty value keeps its empty text under the tag, which YAML
		// reads as the tag gives it, such as "" under !!str. A null there
		// would read as the text "null".
		if astnode.IsNil(n.Value) || isImplicitNull(n.Value) {
			kept := d.keptRun(props.start, props.end)

			b.add(kept)
			b.add(newRun(" ''", originOf(kept)))

			return
		}

		d.writeProps(b, d.keptRun(props.start, props.end), n.Value, st, key)

	case *ast.AliasNode:
		b.add(d.flowScalar(n, origin, st, key))

	case *ast.MappingKeyNode:
		d.writeFlow(b, n.Value, origin, st, key)

	case *ast.MappingNode, *ast.MappingValueNode:
		entries, _ := astnode.Entries(n)

		b.add(newRun("{", origin))

		for i, e := range entries {
			if i > 0 {
				b.add(newRun(", ", origin))
			}

			// The bytes after the key take the origin of its first byte.
			at := len(b.origins)
			d.writeFlow(b, e.Key, origin, st, true)

			k := -1
			if len(b.origins) > at {
				k = b.origins[at]
			}

			b.add(newRun(": ", k))
			d.writeFlow(b, e.Value, k, st, false)
		}

		b.add(newRun("}", origin))

	case *ast.SequenceNode:
		b.add(newRun("[", origin))

		for i, v := range n.Values {
			if i > 0 {
				b.add(newRun(", ", origin))
			}

			d.writeFlow(b, v, d.itemOrigin(n, i, origin), st, false)
		}

		b.add(newRun("]", origin))

	case *ast.LiteralNode:
		s, ok := d.nodeSpan(n)
		if ok {
			origin = originOf(d.keptRun(s.start, s.end))
		}

		b.add(newRun(encodedString(context.Background(), n.Value.Value, key, true), origin))

	default:
		b.add(d.flowScalar(n, origin, st, key))
	}
}

// flowScalar returns n, a scalar or an alias of the current text, in flow
// style, as [Doc.flowRun] writes it with st. A key is true for the key of an
// entry. A string of one line keeps its text when it is quoted, or when
// go-yaml's flow encoder writes it that way, as [encodedString] tells. Any
// other string takes the text of the encoder. In the parse of an encode, a
// plain string keeps its text too unless go-yaml's encoder writes that text
// for a Go string. A value that is not a Go string, such as a [time.Time],
// thus keeps the text that the encoder gave it. A node without text becomes
// null, whose bytes take origin.
func (d *Doc) flowScalar(n ast.Node, origin int, st *flowStyle, key bool) run {
	s, ok := d.nodeSpan(n)
	if !ok {
		return newRun("null", origin)
	}

	kept := d.keptRun(s.start, s.end)

	str, isString := n.(*ast.StringNode)
	if !isString {
		return kept
	}

	multiline := d.lineOf(s.start) != d.lineOf(max(s.start, s.end-1))
	plain := str.Token != nil && str.Token.Type == token.StringType

	if !multiline && !plain {
		return kept
	}

	text := encodedString(context.Background(), str.Value, key, true)
	if text == kept.text ||
		st.encoded && !multiline && plain && encodedString(context.Background(), str.Value, key, false) != kept.text {
		return kept
	}

	return newRun(text, originOf(kept))
}

// itemOrigin returns the origin of the mark of item i of seq, a sequence
// of the current text. The bytes that [Doc.flowRun] adds for the item,
// such as the null of an empty item or the brackets of a collection, take
// that origin. An error there thus binds at the item, as an error at the
// null of an empty value of a mapping binds at its key. It returns origin,
// the origin of the sequence, for an item without a mark.
func (d *Doc) itemOrigin(seq *ast.SequenceNode, i, origin int) int {
	var (
		s  span
		ok bool
	)

	// A mapping without braces in a flow sequence, as in "[a: 1]", binds at
	// its first key, which thus marks the item. Its text takes braces in a
	// flow collection, and the "{" stands for that key.
	tk := itemMark(seq, i)
	if entries, ok := astnode.Entries(seq.Values[i]); seq.IsFlowStyle && isBareMapping(seq.Values[i]) && ok &&
		len(entries) > 0 {
		tk = astnode.KeyToken(entries[0])
	}

	if tk != nil {
		s, ok = d.tokenSpan(tk)
	}

	if !ok || s.start >= len(d.origin) {
		return origin
	}

	return d.origin[s.start]
}

// itemMark returns the token that marks item i of seq: the "-" of an item
// of a block sequence. An item of a flow sequence has no "-", so a flow
// collection there is marked by its own "[" or "{". It returns nil for
// any other item of a flow sequence.
func itemMark(seq *ast.SequenceNode, i int) *token.Token {
	if seq.IsFlowStyle {
		var tk *token.Token

		switch v := seq.Values[i].(type) {
		case *ast.MappingNode:
			if v.IsFlowStyle {
				tk = v.Start
			}

		case *ast.SequenceNode:
			if v.IsFlowStyle {
				tk = v.Start
			}
		}

		return tk
	}

	if len(seq.Entries) != len(seq.Values) || seq.Entries[i] == nil {
		return nil
	}

	tk := seq.Entries[i].Start
	if tk == nil || tk.Type != token.SequenceEntryType {
		return nil
	}

	return tk
}

// writeProps writes props, the text of an anchor or a tag, to b, with the
// flow text of value after it, as [Doc.writeFlow] writes it with st and
// key.
func (d *Doc) writeProps(b *runBuilder, props run, value ast.Node, st *flowStyle, key bool) {
	origin := originOf(props)

	b.add(props)
	b.add(newRun(" ", origin))
	d.writeFlow(b, value, origin, st, key)
}
