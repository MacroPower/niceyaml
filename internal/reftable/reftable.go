package reftable

import (
	"slices"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
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
// reference document.
//
// A decode departs from the rule in one case. When the decoder reads an
// anchor, it registers each anchor inside it again. An alias that it
// reads after the outer anchor then finds the inner one, even when the
// table holds a later anchor of that name. The decoder reads only the
// anchors that the table holds and the anchors inside those. So no
// decode reads an anchor that lies only inside anchors that later
// anchors of their names hide. [Table.Anchors] lists the anchors that an
// alias of a name can read under either rule, and [Table.Weights] weighs
// the alias by the largest of them.
//
// The go-yaml decoder reads the trees of a table and does not write to
// them, so the decodes of many goroutines share them. An anchor inside
// another reads as the node that stands for it in the view of the other,
// as [Table.View] describes. The table also keeps the keys of the merge
// sources in those views that walks read, as [Table.Keys] describes. A
// nil Table does not hold any anchors.
//
// Create instances with [New].
type Table struct {
	// The anchors of the documents of the table, the last of each name.
	own map[string]*entry
	// The anchors of the documents of the table that lie inside another
	// anchor, by name, in document order.
	inner map[string][]*ast.AnchorNode
	// The anchor that each anchor of the documents of the table lies
	// directly inside, for each anchor that lies inside another.
	outer map[*ast.AnchorNode]*ast.AnchorNode
	// The weights of the anchors that the table holds, as [Table.Weights]
	// returns them.
	weights *aliasing.ReferenceWeights
	// The views of the anchors of own.
	views decodetree.AnchorViews
	// The keys of the merge sources under the anchors of own, as
	// [Table.Keys] returns them.
	keys decodewalk.Keys
	// The tables whose anchors those of own hide, in order.
	refs []*Table
}

// entry is an anchor of a [Table].
type entry struct {
	// The anchor, a node of a reference document.
	anchor *ast.AnchorNode
	// Returns the anchor as the decoder reads it, built on the first call.
	view func() *ast.AnchorNode
	// Returns the names the aliases under the anchor read, each once, in
	// document order, found on the first call.
	reads func() []string
	// The keys of the table whose documents hold the anchor.
	keys *decodewalk.Keys
}

// New creates a new [*Table] of the anchors under bodies, the bodies of
// documents in order, over the anchors of refs. New skips each nil table
// in refs.
func New(bodies []ast.Node, refs ...*Table) *Table {
	t := &Table{
		own:   map[string]*entry{},
		inner: map[string][]*ast.AnchorNode{},
		outer: map[*ast.AnchorNode]*ast.AnchorNode{},
		refs:  slices.DeleteFunc(slices.Clone(refs), isNil),
	}
	t.weights = aliasing.NewReferenceWeights(t.Anchors)

	var (
		// The anchors the walk is inside, innermost last.
		around []*ast.AnchorNode
		visit  func(ast.Node) bool
	)

	// The walk visits the anchors in the order their names appear in the
	// text, so an anchor inside another comes after it.
	visit = func(n ast.Node) bool {
		anchor, ok := n.(*ast.AnchorNode)
		if !ok {
			return true
		}

		if len(around) > 0 {
			t.outer[anchor] = around[len(around)-1]
		}

		if name, ok := astnode.Name(anchor.Name); ok {
			t.own[name] = t.newEntry(anchor)

			if len(around) > 0 {
				t.inner[name] = append(t.inner[name], anchor)
			}
		}

		around = append(around, anchor)

		astnode.Inspect(anchor.Value, visit)

		around = around[:len(around)-1]

		return false
	}

	for _, body := range bodies {
		astnode.Inspect(body, visit)
	}

	return t
}

// isNil reports whether t is nil.
func isNil(t *Table) bool {
	return t == nil
}

// newEntry creates a new [*entry] of t for anchor.
func (t *Table) newEntry(anchor *ast.AnchorNode) *entry {
	return &entry{
		anchor: anchor,
		view:   sync.OnceValue(func() *ast.AnchorNode { return t.views.View(anchor) }),
		reads:  sync.OnceValue(func() []string { return aliasNames(anchor.Value) }),
		keys:   &t.keys,
	}
}

// Anchor returns the anchor the table holds for name, and reports false
// when it does not hold one.
func (t *Table) Anchor(name string) (*ast.AnchorNode, bool) {
	e := t.entry(name)
	if e == nil {
		return nil, false
	}

	return e.anchor, true
}

// Anchors returns the anchors that an alias of name can read, or nil
// when the table does not hold an anchor for name. The first is the
// anchor that the table holds for name, as [Table.Anchor] returns it. The
// rest are the anchors of that name that lie inside another anchor that
// a decode can read, in the documents of the table and of the tables that
// it reads. A decode that reads the outer anchor registers such an anchor
// again, so an alias that it reads later can find that anchor.
//
// A decode reads each anchor that the table holds for a name, and every
// anchor inside such an anchor. Anchors leaves out an anchor that lies
// only inside anchors that later anchors of their names hide, because no
// decode reads it.
func (t *Table) Anchors(name string) []*ast.AnchorNode {
	e := t.entry(name)
	if e == nil {
		return nil
	}

	return t.appendInner(t, []*ast.AnchorNode{e.anchor}, name, map[*Table]bool{})
}

// appendInner appends to anchors each anchor of name that lies inside
// another anchor that a decode of root can read, in the documents of the
// table and of the tables that it reads. The first of anchors is the
// anchor that an alias of name reads under the rule of root, and
// appendInner skips that anchor. It reads each table once and records the
// table in visited, so the result holds each anchor once when two tables
// read the same table.
func (t *Table) appendInner(
	root *Table,
	anchors []*ast.AnchorNode,
	name string,
	visited map[*Table]bool,
) []*ast.AnchorNode {
	if visited[t] {
		return anchors
	}

	visited[t] = true

	for _, anchor := range t.inner[name] {
		if anchor != anchors[0] && root.decoded(t, t.outer[anchor]) {
			anchors = append(anchors, anchor)
		}
	}

	for _, ref := range t.refs {
		anchors = ref.appendInner(root, anchors, name, visited)
	}

	return anchors
}

// decoded reports whether a decode of a document that reads the table
// can read anchor, an anchor of the documents of in. A decode reads the
// anchor that the table holds for a name, and every anchor inside it.
func (t *Table) decoded(in *Table, anchor *ast.AnchorNode) bool {
	for at := anchor; at != nil; at = in.outer[at] {
		if name, ok := astnode.Name(at.Name); ok {
			if e := t.entry(name); e != nil && e.anchor == at {
				return true
			}
		}
	}

	return false
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

// View returns the anchor the table holds for name as the decoder reads
// it, the view that [Table.Prime] returns for that anchor. The view of an
// anchor inside another is the node that stands for it in the view of
// the other, as [decodetree.AnchorViews] gives it. The views of a table
// thus copy each node of its documents at most once. View reports false
// when the table does not hold an anchor for name.
func (t *Table) View(name string) (*ast.AnchorNode, bool) {
	e := t.entry(name)
	if e == nil {
		return nil, false
	}

	return e.view(), true
}

// Keys returns the [decodewalk.Keys] that keep the keys of the merge
// sources in the view of the anchor that the table holds for name, as
// [decodewalk.Rules.AnchorKeys] describes. The anchors of the documents
// of a table share one Keys, and every table that reads that table
// returns it for those anchors. So the walks of every Source that reads
// the anchors share it, and the keys stay as long as the table does.
//
// A walk keeps the keys of a source under the node of the view that it
// reads. The views of the table hold at most one node for each node of
// its documents, as [Table.View] describes. So the Keys hold the keys of
// each mapping of the documents at most once, however many anchors hold
// it. They grow with the text of the documents alone. Keys returns nil
// when the table does not hold an anchor for name.
func (t *Table) Keys(name string) *decodewalk.Keys {
	e := t.entry(name)
	if e == nil {
		return nil
	}

	return e.keys
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
// decoder rejects an alias that closes a loop of anchors, other than an
// alias inside its own anchor, which the lookups skip.
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
// documents as the decoder reads it, such as an anchor that [Table.View]
// returns. A place is a node that has the token as its own token, or an
// entry whose key has the token as its token. The decoder reports an
// unknown field at such a key, so the path to a key ends in the `~`
// selector of [paths.Path.Key]. A node inside a place is not a place of
// its own.
//
// The paths read the documents as the decoder does. An alias reads the
// anchor that the table holds for its name. The entries that a `<<` merge
// key brings in read as entries of the mapping that holds the merge key,
// at every depth of merge keys. The decoder reads an alias inside an
// anchor of its name as null, so Paths does not follow such an alias,
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
	return &placeWalk{p: p, open: map[string]bool{}, collect: collect}
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
	// The names of the anchors that the path the walk reads goes through.
	open map[string]bool
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
	if !ok || w.open[name] {
		return
	}

	anchor, ok := w.p.table.View(name)
	if !ok {
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
	name, ok := astnode.Name(anchor.Name)
	if ok && !w.open[name] {
		w.open[name] = true
		defer delete(w.open, name)
	}

	read(anchor.Value)
}

// keyName returns the text that a child selector matches key by, as
// [paths.Resolver.KeyName] gives it. An alias key reads the anchor that
// the table holds for its name, unless the path the walk reads goes
// through an anchor of that name.
func (w *placeWalk) keyName(key ast.Node) (string, bool) {
	alias, ok := astnode.Content(key).(*ast.AliasNode)
	if !ok {
		return w.p.names.KeyName(key)
	}

	name, ok := astnode.Name(alias.Value)
	if !ok || w.open[name] {
		return "", false
	}

	anchor, ok := w.p.table.View(name)
	if !ok {
		return "", false
	}

	return w.p.names.KeyName(anchor.Value)
}

// entry returns the entry the table holds for name, or nil.
func (t *Table) entry(name string) *entry {
	if t == nil {
		return nil
	}

	if e, ok := t.own[name]; ok {
		return e
	}

	for _, ref := range slices.Backward(t.refs) {
		if e := ref.entry(name); e != nil {
			return e
		}
	}

	return nil
}

// Prime returns the anchors that a go-yaml decoder reads, in order,
// before it decodes a node whose aliases read names. Each of those
// aliases then finds the anchor the table holds for its name. The list
// also holds every anchor that an alias inside a listed anchor reaches.
// It holds each anchor once, after the anchors that anchor reaches, so
// the aliases inside each find their anchors the same way.
//
// In a loop of anchors that reach each other, the anchor of the loop
// that names reach first comes last. The decoder reads an alias inside
// the anchor it refers to as null. It rejects any other alias that
// closes a loop, because the anchor that alias names comes later in the
// list.
//
// Each anchor comes back as [Table.View] gives it. Prime skips each name
// that the table does not hold an anchor for.
func (t *Table) Prime(names []string) []ast.Node {
	if t == nil || len(names) == 0 {
		return nil
	}

	var (
		order []ast.Node
		seen  = map[*entry]bool{}
		// The entries the walk is inside, each with the index of its next
		// read, innermost last.
		stack []frame
	)

	push := func(name string) {
		e := t.entry(name)
		if e != nil && !seen[e] {
			seen[e] = true
			stack = append(stack, frame{entry: e})
		}
	}

	for _, name := range names {
		push(name)

		for len(stack) > 0 {
			top := len(stack) - 1
			reads := stack[top].entry.reads()

			if next := stack[top].next; next < len(reads) {
				stack[top].next++
				push(reads[next])

				continue
			}

			order = append(order, stack[top].entry.view())
			stack = stack[:top]
		}
	}

	return order
}

// frame is an entry on the stack of [Table.Prime], with the index of the
// next name it reads.
type frame struct {
	entry *entry
	// The index of the next name among the reads of entry.
	next int
}

// aliasNames returns the names the aliases under node read, each once, in
// document order.
func aliasNames(node ast.Node) []string {
	var names []string

	seen := map[string]bool{}

	astnode.Inspect(node, func(n ast.Node) bool {
		alias, ok := n.(*ast.AliasNode)
		if !ok {
			return true
		}

		if name, ok := astnode.Name(alias.Value); ok && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}

		return true
	})

	return names
}
