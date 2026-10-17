package edit

import (
	"errors"
	"fmt"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
)

// vkind is the kind of a [value].
type vkind uint8

const (
	// A scalar, which decodes to one value.
	vScalar vkind = iota
	vMapping
	vSequence
	// An alias of a reference document. The text of the document does not
	// hold the anchor of such an alias, so the value stands for the alias.
	// It reads the value that a decode of the alias reads with the
	// reference documents.
	vReference
)

// value is one value of a value graph: the graph of a text, or the model
// of the calls of an Apply. An alias of the text points at the value that
// [paths.Resolver.Deref] reads, so the places that an alias shares hold
// one value. A `<<` merge key is an entry like any other, so a graph does
// not evaluate a merge. Two graphs that compare equal thus decode to equal
// values.
//
// The model starts as the graph of the text that the Apply found, so one
// type serves both. The fields that only one of the two uses say so.
type value struct {
	// The scalar of a scalar or a reference, or nil.
	sc *scalar
	// In a text graph, the node of the text under its anchors, and the
	// value of the model that the alignment pairs with it.
	node ast.Node
	pair *value
	// In the model, the edge where the text writes the value, rather than
	// read it through an alias, as the last alignment found it.
	own *edge
	// The entries of a mapping in the order of the text, merge keys
	// included, or the items of a sequence.
	edges []*edge
	// In the model, the number of the alignment that last paired the value.
	epoch int
	kind  vkind
	// In a text graph, whether the alignment keeps the model on another
	// value at the place. The text then writes a copy of a shared value of
	// the model, which is a difference.
	kept bool
}

// edge is an entry of a mapping or an item of a sequence in a value graph.
// As with [value], the fields that only the model or only a text graph
// uses say so.
type edge struct {
	val *value
	// In a text graph, the entry of the text and the value that holds the
	// edge. The alignment pairs the edge with the edge of the model in pair.
	entry *ast.MappingValueNode
	owner *value
	pair  *edge
	// In the model, the alignment numbered seen paired the edge with the
	// edge of the text in mate.
	mate *edge
	// The value of the text as the text writes it at the edge, such as an
	// alias, or nil.
	at ast.Node
	// The key of an entry as a decode reads it, as [keyOf] gives it, which
	// names the entry in the model and in each text graph, or "" for an
	// item.
	key string
	// The name that a path selects the entry by, as
	// [paths.Resolver.KeyName] gives it, or "". It names the entry in a
	// path, and in nothing else.
	name string
	// The tag of a collection at the edge, as [graph.placeTag] reads it, or
	// "".
	tag  string
	seen int
	// In the model, the number of the last call that renamed the entry or
	// put the edge in its value, or 0.
	changed int
	// Whether the entry is a `<<` merge key.
	merge bool
	// In a text graph, whether the text writes the value as an alias.
	alias bool
}

// scalar is the value of a scalar. Two scalars compare by their decodes.
type scalar struct {
	// The value that a decode into any reads, once decoded is true.
	val any
	// The node of the text that decodes to the value, or nil. For a
	// reference, it is the alias under the tags that a decode applies, which
	// decodes with the reference documents alone.
	node ast.Node
	// The decodes of the scalars that hold no alias, which the graphs of an
	// Apply share, as [mapkey.Plain.Value] keeps them.
	plain *mapkey.Plain
	// The reader of the keys of the text, which decodes a reference with
	// the reference documents, as [mapkey.Keys.ValueOf] reads it, or nil.
	refs    *mapkey.Keys
	decoded bool
	// Whether the scalar is a type field of the model, which holds the
	// string val, as [fieldScalar] makes it.
	field bool
}

// scalarValue holds a value of a scalar with its scalar, which
// [newScalarValue] makes in one allocation.
type scalarValue struct {
	sc scalar
	v  value
}

// newScalarValue returns a new value of the kind kind at node, which holds
// sc.
func newScalarValue(kind vkind, node ast.Node, sc scalar) *value {
	x := &scalarValue{v: value{kind: kind, node: node}, sc: sc}
	x.v.sc = &x.sc

	return &x.v
}

// get returns the value of s as a decode into any reads it.
func (s *scalar) get() any {
	if !s.decoded {
		var (
			v   any
			err error
		)

		if s.refs != nil {
			v, err = s.refs.ValueOf(decodetree.DecodeView(s.node))
		} else {
			v, err = s.plain.Value(s.node)
		}

		if err != nil {
			v = "\x00" + err.Error()
		}

		s.val, s.decoded = v, true
	}

	return s.val
}

// sameScalar reports whether a and b decode to equal values. A scalar
// decodes to a value equal to itself. A type field holds its string. A
// scalar of a text holds that string when go-yaml's decoder reads its node
// into the string, as a load of the field reads it.
func sameScalar(a, b *scalar) bool {
	if b.field {
		a, b = b, a
	}

	switch {
	case a == b:
		return true
	case b.field:
		return a.val == b.val
	case a.field:
		return b.node != nil && readsAs(b.node, fmt.Sprint(a.val))
	default:
		return sameValue(a.get(), b.get())
	}
}

// nullValue returns a new null scalar. Its node is "null", so a tag on an
// alias of it applies to that node.
func nullValue() *value {
	null := ast.Null(token.New("null", "null", &token.Position{}))

	return newScalarValue(vScalar, nil, scalar{node: null, decoded: true})
}

// graph is the value graph of a text.
type graph struct {
	res *paths.Resolver
	// The reader of the keys of the text, which gives each the key that a
	// decode reads.
	keys *mapkey.Keys
	// The aliases of the text that lie inside the anchor that each refers
	// to, as [Doc.enclosedAliases] gives them.
	enclosed map[*ast.AliasNode]bool
	root     *value
	// The value of each node of the text under its anchors, and the edge of
	// each entry.
	values  map[ast.Node]*value
	entries map[*ast.MappingValueNode]*edge
	// The tag of the root, as [graph.placeTag] reads it.
	tag string
}

// newGraph returns the graph of body, the body of a document whose
// aliases res reads. The key of each entry comes from keys, a reader of
// the keys of body. Enclosed holds the aliases of body that lie inside the
// anchor that each refers to. The maps of the graph start with room for
// size nodes.
func newGraph(
	body ast.Node,
	res *paths.Resolver,
	keys *mapkey.Keys,
	enclosed map[*ast.AliasNode]bool,
	size int,
) *graph {
	g := &graph{
		res:      res,
		keys:     keys,
		enclosed: enclosed,
		values:   make(map[ast.Node]*value, size),
		entries:  make(map[*ast.MappingValueNode]*edge, size),
	}
	g.root, g.tag = g.value(body), g.placeTag(body)

	return g
}

// underAnchors returns node without the anchors on it.
func underAnchors(node ast.Node) ast.Node {
	for {
		a, ok := node.(*ast.AnchorNode)
		if !ok || a == nil {
			return node
		}

		node = a.Value
	}
}

// value returns the value of node. Each node holds one value, and an alias
// holds the value of the node that it reads.
func (g *graph) value(node ast.Node) *value {
	node = underAnchors(node)
	if astnode.IsNil(node) {
		return nullValue()
	}

	if v, ok := g.values[node]; ok {
		return v
	}

	switch x := node.(type) {
	case *ast.AliasNode:
		// A decode reads an alias inside the value of its own anchor as null.
		if g.enclosed[x] {
			return nullValue()
		}

		target, err := g.res.Deref(x)
		if err == nil {
			return g.value(target)
		}

		if errors.Is(err, paths.ErrReferenceAlias) {
			v := newScalarValue(vReference, x, scalar{node: x, refs: g.keys})
			g.values[x] = v

			return v
		}

		// An alias without an anchor fails the decode, which the check
		// reports first.
		return nullValue()

	case *ast.TagNode:
		return g.tagged(x)

	case *ast.MappingNode:
		v := &value{kind: vMapping, node: x}
		g.values[x] = v

		// The edges of a mapping take one allocation.
		edges := make([]edge, len(x.Values))
		v.edges = make([]*edge, 0, len(x.Values))

		for i, e := range x.Values {
			v.edges = append(v.edges, g.entry(&edges[i], v, e))
		}

		return v

	case *ast.SequenceNode:
		v := &value{kind: vSequence, node: x}
		g.values[x] = v

		edges := make([]edge, len(x.Values))
		v.edges = make([]*edge, 0, len(x.Values))

		for i, item := range x.Values {
			e := &edges[i]
			*e = edge{owner: v, at: item, alias: isAliasValue(item), tag: g.placeTag(item)}
			v.edges = append(v.edges, e)
			e.val = g.value(item)
		}

		return v

	case *ast.CommentGroupNode:
		return nullValue()
	}

	v := newScalarValue(vScalar, node, scalar{node: node, plain: g.keys.Plain()})
	g.values[node] = v

	return v
}

// tagged returns the value of x, a tag. A tag on a scalar decodes with the
// scalar. A tag on a collection is a part of the place of the collection,
// as [graph.placeTag] reads it, so the value of a collection does not hold
// it. The tag holds the value of the collection, so a place below a tag
// that converts the collection, which holds no value, lies in that value.
// A tag before the anchor of a scalar, as in "!t &k x", tags the node of
// the anchor, so each alias of the anchor shares the value of the tag.
// Go-yaml's decoder reads some such aliases without the tag, and a decode
// of the text reads them so.
func (g *graph) tagged(x *ast.TagNode) *value {
	inner := g.value(x.Value)
	if inner.kind == vMapping || inner.kind == vSequence {
		g.values[x] = inner

		return inner
	}

	v := newScalarValue(vScalar, x, scalar{node: x, plain: g.keys.Plain()})
	g.values[x] = v

	if _, anchored := x.Value.(*ast.AnchorNode); anchored {
		g.values[underAnchors(x.Value)] = v
	}

	return v
}

// tagText returns the text of the tag x, such as "!t".
func tagText(x *ast.TagNode) string {
	if x.Start == nil {
		return ""
	}

	return x.Start.Value
}

// placeTag returns the tag of the collection that the text writes as node
// at a place, or "". That is the tag that the text writes there, on the
// collection, on its anchor, or on an alias of it. Without one, an alias
// reads the tag inside its anchor, but not a tag outside the anchor. So
// aliases share the value of an anchored collection without that outer
// tag.
func (g *graph) placeTag(node ast.Node) string {
	for {
		switch x := node.(type) {
		case *ast.AnchorNode:
			node = x.Value
		case *ast.AliasNode:
			node, _ = g.res.Deref(x) //nolint:errcheck // An alias that does not resolve reads no tag.
		case *ast.TagNode:
			if k := g.value(x.Value).kind; k == vMapping || k == vSequence {
				return tagText(x)
			}

			return ""

		default:
			return ""
		}
	}
}

// entry fills out, the edge of e, an entry of the mapping v, and returns
// it.
func (g *graph) entry(out *edge, v *value, e *ast.MappingValueNode) *edge {
	*out = edge{owner: v, entry: e, at: e.Value, alias: isAliasValue(e.Value), tag: g.placeTag(e.Value)}
	g.entries[e] = out

	if astnode.IsMergeKey(e.Key) {
		out.merge = true
	} else {
		k, ok := g.keys.Key(e.Key)
		out.key = keyText(e.Key, k, ok)
		out.name, _ = g.res.KeyName(e.Key)
	}

	out.val = g.value(e.Value)

	return out
}

// keyOf returns the key of an entry whose key node is key, as keys reads
// it. A key that does not decode, such as an alias key that does not
// resolve, takes "\x00" and its text, which names no other key.
func keyOf(key ast.Node, keys *mapkey.Keys) string {
	k, ok := keys.Key(key)

	return keyText(key, k, ok)
}

// keyText returns k, the key that a decode gives key, or "\x00" and the
// text of key when ok reports that key does not decode.
func keyText(key ast.Node, k string, ok bool) string {
	switch {
	case ok:
		return k
	case astnode.IsNil(key):
		return "\x00"
	default:
		return "\x00" + key.String()
	}
}

// isAliasValue reports whether node, a value as the text writes it, is an
// alias under its tags, or under an anchor and tags, as in "&b !t *a",
// which go-yaml takes.
func isAliasValue(node ast.Node) bool {
	for {
		switch x := node.(type) {
		case *ast.TagNode:
			if x == nil {
				return false
			}

			node = x.Value

		case *ast.AnchorNode:
			if x == nil {
				return false
			}

			node = x.Value

		case *ast.AliasNode:
			return x != nil
		default:
			return false
		}
	}
}

// matchEntries pairs the entries of a and b, two mappings. A key of the
// mapping itself pairs with the entry of its name in the other mapping
// that comes after as many merge keys. A merge key pairs with the merge
// key of its order. Entries of one name and one place among the merge keys
// pair in their order. It reports false when an entry of either mapping
// does not pair.
func matchEntries(a, b *value) ([][2]*edge, bool) {
	if len(a.edges) == len(b.edges) {
		pairs := make([][2]*edge, len(a.edges))
		aligned := true

		for i, e := range a.edges {
			o := b.edges[i]
			if e.merge != o.merge || e.key != o.key {
				aligned = false

				break
			}

			pairs[i] = [2]*edge{e, o}
		}

		if aligned {
			return pairs, true
		}
	}

	var pairs [][2]*edge

	am, bm := mergesOf(a), mergesOf(b)
	matched := len(am) == len(bm)

	for i := range min(len(am), len(bm)) {
		pairs = append(pairs, [2]*edge{am[i], bm[i]})
	}

	index := ownIndex(b)

	for e, rank := range ownEntries(a) {
		got, ok := index.take(e.key, rank)
		if !ok {
			matched = false

			continue
		}

		pairs = append(pairs, [2]*edge{e, got})
	}

	return pairs, matched && index.empty()
}

// mergesOf returns the merge keys of the mapping v, in order.
func mergesOf(v *value) []*edge {
	var out []*edge

	for _, e := range v.edges {
		if e.merge {
			out = append(out, e)
		}
	}

	return out
}

// ownEntries returns each key of the mapping v that is not a merge key,
// with the number of merge keys before it.
func ownEntries(v *value) func(yield func(*edge, int) bool) {
	return func(yield func(*edge, int) bool) {
		rank := 0

		for _, e := range v.edges {
			if e.merge {
				rank++

				continue
			}

			if !yield(e, rank) {
				return
			}
		}
	}
}

// ranked is a key of a mapping with the number of merge keys before it.
type ranked struct {
	e    *edge
	rank int
}

// entryIndex is an index of the keys of a mapping other than its merge
// keys.
type entryIndex struct {
	keys map[string][]ranked
	left int
}

// ownIndex returns the index of the keys of the mapping v.
func ownIndex(v *value) *entryIndex {
	x := &entryIndex{keys: make(map[string][]ranked)}

	for e, rank := range ownEntries(v) {
		x.keys[e.key] = append(x.keys[e.key], ranked{e: e, rank: rank})
		x.left++
	}

	return x
}

// take returns the first entry of name after rank merge keys that no
// earlier take returned, and drops it from the index.
func (x *entryIndex) take(name string, rank int) (*edge, bool) {
	list := x.keys[name]

	for i, r := range list {
		if r.rank == rank {
			x.keys[name] = slices.Delete(list, i, i+1)
			x.left--

			return r.e, true
		}
	}

	return nil, false
}

// empty reports whether take returned every entry of the index.
func (x *entryIndex) empty() bool {
	return x.left == 0
}
