package reftable

import (
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Table holds the anchors of reference documents by name.
//
// The anchors of the documents of a table hide those of the tables it
// reads, and a later table among those hides an earlier one. Within the
// documents, the last anchor of a name in the text wins, even one after
// the alias that reads it. An anchor inside another anchor comes after
// it in the text. In a document, the rule decides each alias that does
// not follow an anchor of its name. It also decides every alias inside a
// reference document, wherever a decode reads it.
//
// [Table.Weights] weighs an alias by the anchor that the rule picks for
// its name, the anchor that every decode reads for the alias.
//
// The go-yaml decoder reads the trees of a table and does not write to
// them, so the decodes of many goroutines share them. A nil Table does not
// hold any anchors.
//
// Create instances with [New].
type Table struct {
	// The anchors of the documents of the table, the last of each name.
	own map[string]*ast.AnchorNode
	// The weights of the anchors that the table holds, as [Table.Weights]
	// returns them.
	weights *aliasing.ReferenceWeights
	// The tables whose anchors those of own hide, in order.
	refs []*Table
}

// New creates a new [*Table] of the anchors under bodies, the bodies of
// documents in order, over the anchors of refs. New skips each nil table
// in refs.
func New(bodies []ast.Node, refs ...*Table) *Table {
	t := &Table{
		own:  map[string]*ast.AnchorNode{},
		refs: slices.DeleteFunc(slices.Clone(refs), isNil),
	}
	t.weights = aliasing.NewReferenceWeights(t.Anchor)

	// The walk visits the anchors in the order their names appear in the
	// text, so an anchor inside another comes after it.
	for _, body := range bodies {
		astnode.Inspect(body, func(n ast.Node) bool {
			if anchor, ok := n.(*ast.AnchorNode); ok {
				if name, ok := astnode.Name(anchor.Name); ok {
					t.own[name] = anchor
				}
			}

			return true
		})
	}

	return t
}

// isNil reports whether t is nil.
func isNil(t *Table) bool {
	return t == nil
}

// Anchor returns the anchor the table holds for name, and reports false
// when it does not hold one.
func (t *Table) Anchor(name string) (*ast.AnchorNode, bool) {
	if t == nil {
		return nil, false
	}

	if anchor, ok := t.own[name]; ok {
		return anchor, true
	}

	for _, ref := range slices.Backward(t.refs) {
		if anchor, ok := ref.Anchor(name); ok {
			return anchor, true
		}
	}

	return nil, false
}

// Weights returns what the alias limit weighs for the anchors that the
// table holds, as [aliasing.ReferenceWeights] describes. An alias under
// one of those anchors reads the anchor that this table holds for its
// name. The weights thus belong to this table and not to a table that it
// reads. Every document that reads the table shares them. The Weights of
// a nil Table are nil.
func (t *Table) Weights() *aliasing.ReferenceWeights {
	if t == nil {
		return nil
	}

	return t.weights
}

// maxPlaces is the count of places at which a lookup of [Places] stops. A
// caller learns from at most two places whether a node reaches a token,
// and whether it reaches it on one path alone.
const maxPlaces = 2

// Places finds the places of one token of the reference documents of a
// [Table], as [Places.Paths] and [Places.MergePaths] describe them. A
// lookup reads below an alias only when the anchor of the alias holds a
// place of the token. Places counts the places under the anchor of each
// name once for a read as a value, and once for a read as a merge source.
// Later lookups reuse the counts, and each lookup stops at the second
// place. A lookup
// thus reads each anchor of the reference documents at most a few times,
// however many aliases repeat it. A Places is not safe for concurrent
// use.
//
// The counts read an anchor without the anchors that the path to it goes
// through. They match the paths when the anchors do not form a loop. The
// decoder reads an alias inside an anchor that it is reading as null, and
// the lookups skip such an alias.
//
// Create instances with [Table.Places].
type Places struct {
	table *Table
	// The token the lookups find.
	tk *token.Token
	// The resolver that spells the keys that are not aliases.
	names *paths.Resolver
	// The count of the places of tk under the anchor of each name, up to
	// maxPlaces, for each anchor that the lookups have counted.
	counts map[countKey]int
	// The anchors whose places a lookup is counting.
	counting map[countKey]bool
}

// countKey names the anchor of a name, read as a value or, with merge
// set, as the value of a `<<` merge key. The fields together form the key
// of the counts of [Places].
//
//nolint:unused // The fields tell the keys of the map apart.
type countKey struct {
	name  string
	merge bool
}

// Places creates new [*Places] that find tk in the reference documents of
// the table.
func (t *Table) Places(tk *token.Token) *Places {
	return &Places{
		table:    t,
		tk:       tk,
		names:    paths.NewResolver(nil),
		counts:   map[countKey]int{},
		counting: map[countKey]bool{},
	}
}

// Paths returns the `@` path from node to each of the first two places in
// the reference documents where the go-yaml decoder can report the token
// of p, in document order. The argument node is a node of the reference
// documents as the decoder reads it, such as an anchor that [Table.Anchor]
// returns. A place is a node that has the token as its own token, or an
// entry whose key has the token as its token. The decoder reports an
// unknown field at such a key, so the path to a key ends in the `~`
// selector of [paths.Path.Key]. A node inside a place is not a place of
// its own.
//
// The paths read the documents as the decoder does. An alias reads the
// anchor that the table holds for its name. The entries that a `<<` merge
// key brings in read as entries of the mapping that holds the merge key,
// at every depth of merge keys. The decoder reads an alias inside the
// anchor that it reads as null, so Paths does not follow such an alias,
// and a loop of anchors ends. It does not return a path through a key
// that does not have text a selector can match, as
// [paths.Resolver.KeyName] spells keys.
func (p *Places) Paths(node ast.Node) []paths.Path {
	w := p.walk(true)
	w.find(node, paths.Current())

	return w.found
}

// MergePaths returns the paths that [Places.Paths] returns, for node read
// as the value of a `<<` merge key. The paths then start at the mapping
// that holds the merge key. Node brings in the entries of a mapping, or
// of each mapping of a sequence, as entries of that mapping. A path thus
// does not name the index of such a sequence.
func (p *Places) MergePaths(node ast.Node) []paths.Path {
	w := p.walk(true)
	w.merged(node, paths.Current())

	return w.found
}

// walk returns a new [*placeWalk] of p. With collect set, the walk keeps
// the path to each place. Without it, the walk only counts the places.
func (p *Places) walk(collect bool) *placeWalk {
	return &placeWalk{p: p, open: map[*ast.AnchorNode]bool{}, collect: collect}
}

// count returns the count of the places of the token of p under anchor,
// the anchor that the table holds for name, up to [maxPlaces]. With merge
// set, the count reads anchor as the value of a `<<` merge key. It counts
// the places of each anchor once. It returns zero for an anchor whose
// places a lookup is counting, which an alias inside it reaches through a
// loop of anchors.
func (p *Places) count(name string, anchor *ast.AnchorNode, merge bool) int {
	key := countKey{name: name, merge: merge}
	if count, ok := p.counts[key]; ok {
		return count
	}

	if p.counting[key] {
		return 0
	}

	p.counting[key] = true

	w := p.walk(false)
	w.inside(anchor, func(value ast.Node) { w.read(value, merge, paths.Path{}) })

	delete(p.counting, key)

	p.counts[key] = w.count

	return w.count
}

// placeWalk holds the state of one walk of [Places].
type placeWalk struct {
	p *Places
	// The anchors that the path the walk reads goes through.
	open map[*ast.AnchorNode]bool
	// The paths to the places, in document order, when collect is set.
	found []paths.Path
	// The count of the places that the walk has found.
	count int
	// Whether the walk keeps the paths to the places.
	collect bool
}

// done reports whether the walk has found [maxPlaces] places.
func (w *placeWalk) done() bool {
	return w.count >= maxPlaces
}

// add adds at as a place.
func (w *placeWalk) add(at paths.Path) {
	w.count++

	if w.collect {
		w.found = append(w.found, at)
	}
}

// read reads node as a value, or with merge set as the value of a `<<`
// merge key, where at is the path to node.
func (w *placeWalk) read(node ast.Node, merge bool, at paths.Path) {
	if merge {
		w.merged(node, at)
	} else {
		w.find(node, at)
	}
}

// find adds the places under node, where at is the path to node.
func (w *placeWalk) find(node ast.Node, at paths.Path) {
	if w.done() || astnode.IsNil(node) {
		return
	}

	if node.GetToken() == w.p.tk {
		w.add(at)

		return
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		w.inside(n, func(value ast.Node) { w.find(value, at) })

	case *ast.TagNode:
		w.find(n.Value, at)

	case *ast.AliasNode:
		w.through(n, false, at)

	case *ast.MappingNode:
		for _, entry := range n.Values {
			w.entry(entry, at)
		}

	case *ast.MappingValueNode:
		w.entry(n, at)

	case *ast.SequenceNode:
		for i, value := range n.Values {
			if w.collect {
				w.find(value, at.Index(i))
			} else {
				w.find(value, at)
			}
		}
	}
}

// entry adds the places under entry, an entry of the mapping that the path
// at names.
func (w *placeWalk) entry(entry *ast.MappingValueNode, at paths.Path) {
	if w.done() || entry == nil {
		return
	}

	if astnode.IsMergeKey(entry.Key) {
		w.merged(entry.Value, at)

		return
	}

	name, ok := w.keyName(entry.Key)
	if !ok {
		return
	}

	child := at
	if w.collect {
		child = at.Child(name)
	}

	if astnode.KeyToken(entry) == w.p.tk {
		if w.collect {
			child = child.Key()
		}

		w.add(child)

		return
	}

	w.find(entry.Value, child)
}

// merged adds the places through the entries of each mapping that value,
// the value of a `<<` merge key, brings into the mapping that the path at
// names. A node of value that has the token as its own token is a place
// with that path.
func (w *placeWalk) merged(value ast.Node, at paths.Path) {
	if w.done() || astnode.IsNil(value) {
		return
	}

	if value.GetToken() == w.p.tk {
		w.add(at)

		return
	}

	switch v := value.(type) {
	case *ast.AnchorNode:
		w.inside(v, func(value ast.Node) { w.merged(value, at) })

	case *ast.TagNode:
		w.merged(v.Value, at)

	case *ast.AliasNode:
		w.through(v, true, at)

	case *ast.SequenceNode:
		for _, elem := range v.Values {
			w.merged(elem, at)
		}

	case *ast.MappingNode:
		for _, entry := range v.Values {
			w.entry(entry, at)
		}

	case *ast.MappingValueNode:
		w.entry(v, at)
	}
}

// through adds the places under the anchor that the table holds for the
// name of alias, read as a value or, with merge set, as the value of a
// `<<` merge key. It skips an anchor that the path the walk reads goes
// through, and one without a place. A walk that counts adds the count of
// the anchor without reading it.
func (w *placeWalk) through(alias *ast.AliasNode, merge bool, at paths.Path) {
	name, ok := astnode.Name(alias.Value)
	if !ok {
		return
	}

	anchor, ok := w.p.table.Anchor(name)
	if !ok || w.open[anchor] {
		return
	}

	count := w.p.count(name, anchor, merge)

	switch {
	case count == 0:
		return
	case !w.collect:
		w.count = min(w.count+count, maxPlaces)
	default:
		w.inside(anchor, func(value ast.Node) { w.read(value, merge, at) })
	}
}

// inside calls read with the value of anchor, with the path the walk
// reads going through anchor.
func (w *placeWalk) inside(anchor *ast.AnchorNode, read func(ast.Node)) {
	if !w.open[anchor] {
		w.open[anchor] = true
		defer delete(w.open, anchor)
	}

	read(anchor.Value)
}

// keyName returns the text that a child selector matches key by, as
// [paths.Resolver.KeyName] gives it. An alias key reads the anchor that
// the table holds for its name, unless the path the walk reads goes
// through that anchor.
func (w *placeWalk) keyName(key ast.Node) (string, bool) {
	alias, ok := astnode.Content(key).(*ast.AliasNode)
	if !ok {
		return w.p.names.KeyName(key)
	}

	name, ok := astnode.Name(alias.Value)
	if !ok {
		return "", false
	}

	anchor, ok := w.p.table.Anchor(name)
	if !ok || w.open[anchor] {
		return "", false
	}

	return w.p.names.KeyName(anchor.Value)
}
