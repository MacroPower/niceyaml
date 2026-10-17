package edit

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// diffKind is the kind of a [diff].
type diffKind uint8

const (
	// The values differ in kind or as scalars.
	diffValue diffKind = iota
	// The places tag their collections differently.
	diffTag
	// The text lacks an entry of the model.
	diffMissing
	// The text holds an entry that the model lacks.
	diffExtra
	// The sequences hold different numbers of items.
	diffItems
	// The mappings hold different numbers of merge keys.
	diffMerges
	// The text writes a shared value of the model at a second place, or
	// where it read the value through an alias.
	diffShared
	// An alias of the text reads another value than the model holds at its
	// place.
	diffAlias
)

// diff is a place where the text differs from the model.
type diff struct {
	// The value of the model and the value of the text at the place, or
	// the mappings that hold the entry of a missing or an extra entry.
	m, t *value
	// The edge of the text or of the model of an extra or a missing entry,
	// or the edge of the model of a tag.
	e *edge
	// The edge of the text that holds t, or nil for the root.
	at   *edge
	kind diffKind
}

// id returns the name of d across the alignments of an Apply: the value
// of the model, the kind, and the key of an entry.
func (d *diff) id() string {
	key := ""
	if d.e != nil {
		key = d.e.key
	}

	return fmt.Sprintf("%p %d %s", d.m, d.kind, key)
}

// model is the model of the calls of an Apply. It starts as the graph of
// the text that the Apply found. Each call changes the model as the call
// describes, without the merge rules or the alias rules of go-yaml. After
// each call, an alignment pairs each value of the model with the value of
// the text at its place, as [model.align] describes. After the last call,
// the text must hold the values of the model.
type model struct {
	// The edge that holds the root of the model.
	top *edge
	// Each entry that read the key of another entry of its mapping in the
	// text that the Apply found, by the edge of the entry.
	twins map[*edge]twin
	// The graph of the text that the last alignment read, and the parse of
	// that text.
	text   *graph
	synced *niceyaml.Node
	// The places where the text differs from the model, after the last
	// alignment, and the number of the call after which each first differed.
	since map[string]int
	diffs []*diff
	// The calls of the Apply, as messages name them.
	calls []editRecord
	// The entries that the call running created.
	wrote []wrote
	// The pairs of the alias edges that wait for the values at their own
	// places.
	pending [][2]*edge
	// The text that the Apply found.
	start state
	// The number of the last alignment.
	epoch int
	// The number of the nodes of the Apply that hold the edge of the model
	// at their places.
	slottedNodes int
	// Whether the Set running keeps the anchor of the value that it
	// replaces, so each alias of the anchor reads the new value.
	keepsAnchor bool
}

// startModel returns the model of the current text: its graph, whose
// values the model shares with the text. An alignment of the graph with
// itself records where the text writes each value.
func (d *Doc) startModel() *model {
	g := newGraph(d.node.AST(), d.node.Resolver(), d.keyReader(), d.enclosedAliases(), len(d.curLines))
	x := &model{top: &edge{val: g.root, tag: g.tag}, start: d.save(), synced: d.node, since: make(map[string]int)}
	x.align(g, 0)

	x.twins = twinsOf(g)

	return x
}

// twin is an entry of the model that read the key of another entry of its
// mapping in the graph of the text that the Apply found, as [twinsOf]
// finds it.
type twin struct {
	// The mapping of the entry in that graph.
	owner *value
	// The key that the decode reads.
	key string
	// Whether the key of the other entry has the same text, so the two
	// entries read one key there. Only a Source that
	// [niceyaml.WithAllowDuplicateKeys] gives holds such entries.
	same bool
}

// twinsOf returns each entry of the model that has a twin in g, the graph
// of the text that the Apply found, by the edge of the entry. A twin is
// another entry of its mapping that the decode reads as the same key, as
// for "1" and "01".
func twinsOf(g *graph) map[*edge]twin {
	var (
		twins map[*edge]twin
		edges []*edge
	)

	seen := make(map[*value]bool)

	for _, te := range g.entries {
		o := te.owner
		if o == nil || seen[o] {
			continue
		}

		seen[o] = true
		edges = edges[:0]

		for _, e := range o.edges {
			if !e.merge && e.pair != nil {
				edges = append(edges, e)
			}
		}

		slices.SortStableFunc(edges, func(a, b *edge) int {
			return cmp.Or(strings.Compare(a.key, b.key), strings.Compare(a.name, b.name))
		})

		for i := 1; i < len(edges); i++ {
			a, b := edges[i-1], edges[i]
			if a.key != b.key {
				continue
			}

			if twins == nil {
				twins = make(map[*edge]twin)
			}

			same := a.name == b.name && a.name != ""
			for _, e := range []*edge{a, b} {
				t := twins[e.pair]
				twins[e.pair] = twin{owner: o, key: e.key, same: t.same || same}
			}
		}
	}

	return twins
}

// align pairs the values of the model with the values of g, the graph of the
// text after the call numbered call. It pairs each value at its own place in
// the text first, where the text writes the value rather than read it
// through an alias. The engine writes no copy of a shared value of the
// model. A value that the text writes at a second place is thus a
// difference, and so is a value that the text writes where the model reads
// it through an alias. Its place stays on the shared value, and the call
// fails, as [Doc.differProblem] describes.
//
// Then each alias of the text must read the value of the model that pairs
// with its anchor. An alias of a value that is a difference passes, because
// that difference fails the call already. Any other alias reads another
// value, so the call changed what the alias reads, and align records the
// alias as a difference too. The places where the text differs from the
// model go in the diffs.
func (x *model) align(g *graph, call int) {
	x.epoch++
	x.text = g
	x.pending = x.pending[:0]

	var diffs []*diff

	if x.top.tag != g.tag && x.top.val.kind == g.root.kind {
		diffs = append(diffs, &diff{kind: diffTag, m: x.top.val, t: g.root, e: x.top})
	}

	x.visit(x.top, nil, g.root, &diffs)

	for i := 0; i < len(x.pending); i++ { //nolint:intrange // The loop adds pairs as it goes.
		me, te := x.pending[i][0], x.pending[i][1]
		m, t := me.val, te.val

		switch prev := t.pair; {
		case prev == nil:
			x.visit(me, te, t, &diffs)
		case prev == m, t.kept:
		default:
			diffs = append(diffs, &diff{kind: diffAlias, m: m, t: t, at: te})
		}
	}

	since := make(map[string]int, len(diffs))

	for _, d := range diffs {
		id := d.id()
		if at, ok := x.since[id]; ok {
			since[id] = at
		} else {
			since[id] = call
		}
	}

	x.diffs, x.since = diffs, since
}

// visit pairs me, an edge of the model, with t, the value of the text at
// its own place. The edge te of the text holds t, or te is nil for the
// root. When te is an alias, the alignment did not pair the value that it
// reads at its own place. Visit pairs the edges below them too.
func (x *model) visit(me, te *edge, t *value, diffs *[]*diff) {
	m := me.val

	if m.kind != t.kind {
		*diffs = append(*diffs, &diff{kind: diffValue, m: m, t: t, at: te})

		return
	}

	written := te == nil || !te.alias

	// The text writes a copy of the value at a second place, or where it
	// read the value through an alias.
	if m.epoch == x.epoch || written && m.own != nil && m.own != me {
		t.pair, t.kept = m, true
		*diffs = append(*diffs, &diff{kind: diffShared, m: m, t: t, at: te})

		return
	}

	m.epoch, t.pair = x.epoch, m

	if written {
		m.own = me
	}

	switch m.kind {
	case vScalar, vReference:
		if !sameScalar(m.sc, t.sc) {
			*diffs = append(*diffs, &diff{kind: diffValue, m: m, t: t, at: te})
		}

	case vSequence:
		if len(m.edges) != len(t.edges) {
			*diffs = append(*diffs, &diff{kind: diffItems, m: m, t: t, at: te})
		}

		for i := range min(len(m.edges), len(t.edges)) {
			x.pairEdges(m.edges[i], t.edges[i], diffs)
		}

	case vMapping:
		x.pairEntries(m, te, t, diffs)
	}
}

// pairEntries pairs the entries of m, a mapping of the model, with the
// entries of t, the mapping of the text at its place, as [matchEntries]
// pairs them. The edge te of the text holds t, or te is nil for the root.
func (x *model) pairEntries(m *value, te *edge, t *value, diffs *[]*diff) {
	pairs, matched := matchEntries(m, t)

	for _, p := range pairs {
		x.pairEdges(p[0], p[1], diffs)
	}

	if matched {
		return
	}

	if len(mergesOf(m)) != len(mergesOf(t)) {
		*diffs = append(*diffs, &diff{kind: diffMerges, m: m, t: t, at: te})
	}

	for _, e := range m.edges {
		if !e.merge && e.seen != x.epoch {
			*diffs = append(*diffs, &diff{kind: diffMissing, m: m, t: t, e: e, at: te})
		}
	}

	for _, e := range t.edges {
		if !e.merge && e.pair == nil {
			*diffs = append(*diffs, &diff{kind: diffExtra, m: m, t: t, e: e, at: te})
		}
	}
}

// pairEdges pairs me, an edge of the model, with te, the edge of the text
// at its place. An alias waits until the values at their own places pair.
// The tag of a collection differs only between values of one kind, so a
// value of another kind differs as a value, as [model.visit] finds it.
func (x *model) pairEdges(me, te *edge, diffs *[]*diff) {
	te.pair = me
	me.seen, me.mate = x.epoch, te

	if me.tag != te.tag && me.val.kind == te.val.kind {
		*diffs = append(*diffs, &diff{kind: diffTag, m: me.val, t: te.val, e: me, at: te})
	}

	if te.alias {
		x.pending = append(x.pending, [2]*edge{me, te})

		return
	}

	x.visit(me, te, te.val, diffs)
}

// modelCall is one call of an edit as the model reads it: the place of the
// call in the model before the edit.
type modelCall struct {
	// The model of the Apply running.
	x *model
	// The edge of the model at the place, or nil when the place does not
	// hold a value. The owner is the value of the model that holds the
	// edge, or nil for the root. For an entry, text is the edge of the text
	// that pairs with edge.
	edge  *edge
	owner *value
	text  *edge
	// For a place that does not hold a value, the value of the model that
	// holds the place. The selectors of rest lead from it to the place.
	holder *value
	// The record of the last edit before the call.
	before *editRecord
	rest   []paths.Selector
	// The text of each protected value before the call, as
	// [Doc.guardTexts] gives it. It is nil for the write of a protected
	// field, which changes such a value.
	guards []string
	// What the call does, by the path of its place, as [editRecord] names
	// an edit at another place. A call that leaves the text as it found it
	// records no edit, so a message names it by named.
	named string
	// Each entry that a later entry of its key hides on the path of the
	// place, as the node of the call holds them, and the path of the place
	// in the text before the call.
	via  []hiddenEntry
	path paths.Path
	// Whether the model maps the place. It then holds the edge at the place,
	// or the value that holds the place when the place does not hold a value.
	mapped bool
}

// placeOf returns the place p of the current text as a call of the model
// reads it.
func (x *model) placeOf(p *place) modelCall {
	c := modelCall{x: x, path: p.path}

	switch {
	case p.root:
		c.edge, c.mapped = x.top, true

	case p.exists && p.entry != nil:
		te := x.text.entries[p.entry]
		if te != nil && te.pair != nil && te.owner.pair != nil {
			c.edge, c.owner, c.text, c.mapped = te.pair, te.owner.pair, te, true
		}

	case p.exists && p.seq != nil:
		tv := x.text.values[p.seq]
		if tv != nil && tv.pair != nil && p.index < len(tv.edges) && tv.edges[p.index].pair != nil {
			c.edge, c.owner, c.mapped = tv.edges[p.index].pair, tv.pair, true
		}

	case !p.exists && !astnode.IsNil(p.container):
		tv := x.text.values[p.container]
		_, isSeq := p.container.(*ast.SequenceNode)
		_, isMap := readEntries(p.container)

		switch {
		case isSeq || isMap:
			if tv != nil && tv.pair != nil {
				c.holder, c.rest, c.mapped = tv.pair, slices.Collect(p.path.Selectors())[p.found:], true
			}

		default:
			// A scalar holds no value below it, whatever its tags, as
			// [Doc.readValue] reads it.
			c.mapped = true
		}
	}

	return c
}

// nested returns v below the mappings that the names of sels give, the
// first name outermost.
func nested(sels []paths.Selector, v *value) *value {
	for _, sel := range slices.Backward(sels) {
		v = &value{kind: vMapping, edges: []*edge{{key: sel.Name, name: sel.Name, val: v}}}
	}

	return v
}

// attach puts e under h, a value of the model, at sel, after the entries
// or the items of h, for the call numbered call. Each name of rest gives a
// mapping on the way, and e takes the last name of rest, or sel when rest
// is empty. The key that an edit writes for a name reads as the name. It
// returns the edge that h takes, or nil when h cannot hold sel.
func attach(h *value, sel paths.Selector, rest []paths.Selector, e *edge, call int) *edge {
	top := e
	e.changed = call

	if len(rest) > 0 {
		last := rest[len(rest)-1]
		e.key, e.name, e.merge = last.Name, last.Name, false
		top = &edge{val: nested(rest[:len(rest)-1], &value{kind: vMapping, edges: []*edge{e}})}
		top.changed = call
	}

	switch {
	case sel.Kind == paths.SelectorIndex && h.kind == vSequence && len(rest) == 0:
		top.key, top.name = "", ""
	case sel.Kind == paths.SelectorChild && h.kind == vMapping:
		top.key, top.name, top.merge = sel.Name, sel.Name, false
	default:
		return nil
	}

	h.edges = append(h.edges, top)

	return top
}

// detach takes e out of the model value owner, for the call numbered call.
func detach(owner *value, e *edge, call int) bool {
	i := slices.Index(owner.edges, e)
	if i < 0 {
		return false
	}

	owner.edges = slices.Delete(slices.Clone(owner.edges), i, i+1)
	e.changed = call

	return true
}

// call returns the number of the call of c.
func (c *modelCall) call() int {
	return len(c.x.calls)
}

// set replaces the value at the place of c with the value of e, or
// creates the place with it, as [Node.Set] does. The place drops its tag
// and takes the tag of e, the tag that the encode of the value writes on
// a collection. It reports false when the model cannot take the call.
func (c *modelCall) set(e *edge) bool {
	switch {
	case !c.mapped || e == nil || e.val == nil:
		return false
	case c.edge != nil:
		old := c.edge.val
		c.edge.val, c.edge.tag = e.val, e.tag

		if c.x.keepsAnchor {
			c.x.share(old, e)
		}

		c.x.wroteIn(e.val)

		return true

	case c.holder == nil || len(c.rest) == 0:
		return false
	}

	c.x.wroteIn(e.val)

	// The value takes its tag at its own place, below the mappings that the
	// call creates on the way.
	return c.x.created(c.holder, attach(c.holder, c.rest[0], c.rest[1:], &edge{val: e.val, tag: e.tag}, c.call()))
}

// share gives every edge of x that reads old, a value that a Set replaces
// at its anchor, the value and the tag of e, which the Set writes there.
// Each alias of the anchor reads the new value, and so does each merge key
// that reads it.
func (x *model) share(old *value, e *edge) {
	seen := make(map[*value]bool)

	var walk func(v *value)

	walk = func(v *value) {
		if v == nil || seen[v] {
			return
		}

		seen[v] = true

		for _, ed := range v.edges {
			if ed.val == old {
				ed.val, ed.tag = e.val, e.tag
			}

			walk(ed.val)
		}
	}

	walk(x.top.val)
}

// wroteIn records that the call running wrote each entry of each mapping
// inside v, a value that the call puts in the model. The user's text does
// not hold those entries, so the repeat rule reads each of them, as it
// reads an entry that a call creates. The entries of a mapping go in the
// order of their keys and then of their names, so the rule names the same
// entry whatever order the encode gave them.
func (x *model) wroteIn(v *value) {
	seen := make(map[*value]bool)

	var walk func(v *value)

	walk = func(v *value) {
		if seen[v] {
			return
		}

		seen[v] = true

		var own []wrote

		for _, e := range v.edges {
			if v.kind == vMapping && !e.merge {
				own = append(own, wrote{owner: v, e: e, key: e.key})
			}

			walk(e.val)
		}

		slices.SortStableFunc(own, func(a, b wrote) int {
			return cmp.Or(strings.Compare(a.key, b.key), strings.Compare(a.e.name, b.e.name))
		})

		x.wrote = append(x.wrote, own...)
	}

	walk(v)
}

// remove takes the value at the place of c out of the model, as
// [Node.Remove] does.
func (c *modelCall) remove() bool {
	switch {
	case !c.mapped:
		return false
	case c.edge == nil:
		return true
	case c.owner == nil:
		return false
	}

	return detach(c.owner, c.edge, c.call())
}

// rename gives the entry at the place of c the key of r, as [Node.SetKey]
// does. It reports false when the model cannot take the call.
func (c *modelCall) rename(r *renaming) bool {
	switch {
	case !c.mapped:
		return false
	case c.edge == nil:
		return true
	case c.owner == nil || c.owner.kind != vMapping || c.edge.merge:
		return false
	}

	c.edge.key, c.edge.name, c.edge.changed = r.key, r.name, c.call()

	return true
}

// renaming is a rename of a key as the model reads it.
type renaming struct {
	// The key of the entry after the rename, as a decode reads it, and the
	// name that a path selects it by.
	key, name string
	// Whether the call leaves the text as it is, so the model stays as it
	// is too.
	kept bool
}

// renames returns the entry of the model whose key a rename of the entry
// at the place of c to the key of r changes, with its key after the
// rename. No key of the text holds an anchor or an alias, so the rename
// changes no other entry.
func (c *modelCall) renames(r *renaming) []wrote {
	if !c.mapped || c.edge == nil || c.owner == nil {
		return nil
	}

	return []wrote{{owner: c.owner, e: c.edge, te: c.text, key: r.key, old: c.edge.key, renamed: true}}
}

// wrote is an entry of a mapping of the model whose key a call writes. The
// call creates the entry, or it renames the entry, through the anchor of a
// key too.
type wrote struct {
	// The mapping of the model that holds the entry, the entry, and the
	// edge of the text that pairs with it, or nil.
	owner *value
	e, te *edge
	// The key of the entry after the call, and before it for a renamed
	// entry.
	key, old string
	// Whether the call renames the entry, rather than creates it.
	renamed bool
}

// created records that the call running created e, an entry of the
// mapping owner, or an item of the sequence owner. It reports false for a
// nil e, which shows that the model cannot take the call.
func (x *model) created(owner *value, e *edge) bool {
	if e == nil {
		return false
	}

	if owner.kind == vMapping {
		x.wrote = append(x.wrote, wrote{owner: owner, e: e, key: e.key})
	}

	return true
}

// twinned reports whether a and b, two entries of one mapping of x, can
// read one key. Two twins whose keys have the same text can read one key
// again.
func (x *model) twinned(a, b *edge) bool {
	ta, oka := x.twins[a]
	tb, okb := x.twins[b]

	return oka && okb && ta.same && tb.same && ta.owner == tb.owner && ta.key == tb.key
}

// repeated is an entry that a call writes, whose key repeats the key of
// another entry of its mapping, as [model.repeat] finds it.
type repeated struct {
	other *edge
	w     wrote
}

// repeat finds an entry of ws, the entries whose keys a call writes, that
// reads the key of another entry of its mapping after the call. The two
// entries read different keys before the call, and a created entry reads
// no key before it. A decode reads a mapping of two entries of one key as
// one entry, so the call fails there. Two twins of x, entries that had one
// key text and read one key in the text that the Apply found, can read one
// key again, as [model.twinned] reads them. The decode read them as one
// entry already.
func (x *model) repeat(ws []wrote) (repeated, bool) {
	after := make(map[*edge]string, len(ws))
	before := make(map[*edge]string, len(ws))

	for _, w := range ws {
		after[w.e] = w.key

		if w.renamed {
			before[w.e] = w.old
		}
	}

	// The entries of each owner by their keys after the writes, in the
	// order of the owner, so each write looks up the entries of its key.
	byKey := make(map[*value]map[string][]*edge)

	for _, w := range ws {
		keyed, ok := byKey[w.owner]
		if !ok {
			keyed = make(map[string][]*edge, len(w.owner.edges))

			for _, f := range w.owner.edges {
				if f.merge {
					continue
				}

				key, ok := after[f]
				if !ok {
					key = f.key
				}

				keyed[key] = append(keyed[key], f)
			}

			byKey[w.owner] = keyed
		}

		for _, f := range keyed[w.key] {
			if f == w.e {
				continue
			}

			old, ok := before[f]
			if !ok {
				old = f.key
			}

			if (!w.renamed || old != w.old) && !x.twinned(f, w.e) {
				return repeated{w: w, other: f}, true
			}
		}
	}

	return repeated{}, false
}

// move takes the value at the place of c to the place of to, as
// [Node.Move] does. The edge of the value stays the same.
func (c *modelCall) move(to *modelCall) bool {
	switch {
	case !c.mapped:
		return false
	case c.edge == nil:
		return true
	case c.owner == nil || to == nil || !to.mapped || to.holder == nil || len(to.rest) == 0:
		return false
	}

	if !detach(c.owner, c.edge, c.call()) {
		return false
	}

	return c.x.created(to.holder, attach(to.holder, to.rest[0], to.rest[1:], c.edge, c.call()))
}

// insert puts the entry name with v into h, a mapping of the model, as the
// first entry of h.
func (x *model) insert(h *value, name string, v *value) bool {
	if h == nil || h.kind != vMapping || v == nil {
		return false
	}

	e := &edge{key: name, name: name, val: v, changed: len(x.calls)}
	h.edges = slices.Insert(slices.Clone(h.edges), 0, e)

	return x.created(h, e)
}

// syncModel returns the model of the Apply running, in step with the
// current text. It starts the model at the first call of the Apply.
func (d *Doc) syncModel() *model {
	if d.model == nil {
		d.model = d.startModel()
	}

	x := d.model
	if x.synced != d.node {
		d.alignModel(x)
	}

	return x
}

// alignModel aligns x with the current text, as [model.align] describes.
func (d *Doc) alignModel(x *model) {
	x.align(
		newGraph(d.node.AST(), d.node.Resolver(), d.keyReader(), d.enclosedAliases(), len(d.curLines)),
		len(x.calls),
	)

	x.synced = d.node
}

// modelStart returns the place of n as the model reads it before a call
// of an edit through n, which named names as [modelCall] holds it. It
// returns nil for a node whose Apply returned. A Set, as sets tells, starts
// as [Doc.startCall] describes.
func (d *Doc) modelStart(n *Node, named string, sets bool) *modelCall {
	if n.ended || !d.applying {
		return nil
	}

	d.callStart, d.calling, d.found, d.callNamed = d.save(), true, nil, named
	d.mergesAt = d.mergeState()

	if d.model != nil {
		d.model.keepsAnchor = false
	}

	// A node whose path reads another value selects its own entry first, as
	// the edit of the call reads it.
	var (
		at    *place
		moved bool
	)

	c := d.modelOf(n.path, func() (*place, error) {
		moved = d.displaced(n)

		p, err := d.nodePlace(n)
		at = p

		return p, err
	})
	c.guards, c.named, c.before, c.via = d.guardTexts(), named, d.lastEdit, n.hidden

	if !sets || n.gone || n.err != nil || moved {
		at = nil
	}

	d.startCall(named, at)

	return c
}

// startCall starts the call running, which named names, as
// [Doc.callDetails] names it. When at is the place of a Set that holds a
// value, the Set replaces that value. The value binds where the note of a
// Set binds the value that replaces it, as [Doc.setNote] gives it. A Set
// replaces no value that an edit created and no root, so for such a place,
// and for a nil at, the call replaces no value.
func (d *Doc) startCall(named string, at *place) {
	d.running, d.replacing = named, false

	if at == nil || !at.exists || at.root {
		return
	}

	n := d.setNote(at, named)
	d.replacing, d.replaced = n.replaced && n.located, d.noteSite(n, 0, false)
}

// endRunning ends the call that [Doc.startCall] started.
func (d *Doc) endRunning() {
	d.running, d.replacing = "", false
}

// runningState is the call running, as [Doc.startCall] records it.
type runningState struct {
	running   string
	replaced  site
	replacing bool
}

// runningCall returns the call running, which [Doc.resumeCall] runs again.
func (d *Doc) runningCall() runningState {
	return runningState{running: d.running, replaced: d.replaced, replacing: d.replacing}
}

// resumeCall ends the call running and runs s again, a call that
// [Doc.runningCall] returned.
func (d *Doc) resumeCall(s runningState) {
	d.running, d.replaced, d.replacing = s.running, s.replaced, s.replacing
}

// modelAt returns the place at path as the model reads it.
func (d *Doc) modelAt(path paths.Path) *modelCall {
	return d.modelOf(path, func() (*place, error) { return d.resolve(path) })
}

// modelOf returns the place at path, which find gives in the current text,
// as the model reads it.
func (d *Doc) modelOf(path paths.Path, find func() (*place, error)) *modelCall {
	x := d.syncModel()
	d.slotNodes(x)

	p, err := find()
	if err != nil {
		return &modelCall{x: x, path: path}
	}

	c := x.placeOf(p)

	return &c
}

// modelEnd ends c, a call of an edit that returned err, in the model.
// When the edit passed, change makes the change of the call in the model,
// and modelEnd reads the text after the call, as [model.align] describes.
// The problem of an alias whose value the call changed fails the call. So
// does a change that the model cannot make, which shows a call that the
// model does not describe. So do a character that YAML does not allow, as
// [Doc.printableProblem] describes, and a `<<` merge key that go-yaml's
// decoder reads otherwise than the merge spec, as [Doc.madeMisread]
// describes. A call that fails leaves the Doc as it found it, as
// [Doc.undoCall] describes.
//
//nolint:contextcheck // The decode of a text whose alias reads another value reads no state that a context carries.
func (d *Doc) modelEnd(c *modelCall, err error, change func() bool) (problem error) {
	changed := false

	defer func() {
		if problem != nil && c != nil {
			d.undoCall(changed)
		}

		d.calling, d.found, d.callNamed = false, nil, ""
		d.endRunning()
	}()

	if err != nil || c == nil {
		return err
	}

	err = d.printableProblem(d.callStart.cur)
	if err != nil {
		return err
	}

	err = d.guardChanged(c.path, c.guards)
	if err != nil {
		return err
	}

	h := &Node{d: d, path: c.path, hidden: c.via}

	// A block scalar that the call wrote keeps its string first. The call
	// can write the text that it found, as a Set of "x\n" over a scalar "x"
	// at the end of a text without a line break, and the scalar then takes
	// the line break back.
	err = d.keepStrings(context.Background(), h)
	if err == nil {
		d.keepOrigins()

		err = d.madeMisread(c.named)
	}

	if err == nil {
		err = d.scalarProblem(h, c.named)
	}

	if err == nil {
		err = d.commentProblem(h, c.named)
	}

	if err == nil {
		err = d.mergeKept(h, c.named)
	}

	if err == nil {
		err = d.madeProblem(h, c.named)
	}

	if err != nil {
		return err
	}

	x := d.model
	if x == nil {
		return nil
	}

	rec := editRecord{named: c.named}
	if d.lastEdit != nil && d.lastEdit != c.before {
		rec = *d.lastEdit
	}

	x.calls = append(x.calls, rec)
	x.wrote = x.wrote[:0]

	changed = true

	if !change() {
		return d.bug(
			&Node{d: d, path: c.path, hidden: c.via},
			fmt.Errorf("the check cannot follow %s %s", d.subject(), rec.named),
		)
	}

	if x.synced == d.node {
		return nil
	}

	d.alignModel(x)

	// A key that a call creates reads as its name, which no other entry of
	// its mapping has as its name. The decode can still read another entry
	// under that key.
	for i, w := range x.wrote {
		if w.e.seen == x.epoch {
			x.wrote[i].te = w.e.mate
		}
	}

	if r, ok := x.repeat(x.wrote); ok {
		return d.repeatProblem(x, r, rec, c.path)
	}

	if len(x.diffs) == 0 {
		return nil
	}

	d.stuck = d.differProblem(x)

	return d.stuck
}

// differProblem returns the problem of a call after which the current text
// differs from x, the model of the calls. The engine can write a text that
// does not decode, and a call that the engine wrote wrong can make an alias
// read another value. Either shows a bug in the engine, so the call fails at
// once, and each later call of the Apply returns the problem, as [Node]
// describes. The problem names the first difference. A text that does not
// decode gives the problem of its decode.
//
//nolint:contextcheck // The decode of a text that differs from the model reads no state that a context carries.
func (d *Doc) differProblem(x *model) error {
	_, err := d.decodeProblem(context.Background())
	if err != nil {
		return err
	}

	return d.diffProblem(x, d.firstDiff(x))
}

// slotNodes gives each node of the Apply that does not hold the edge of
// the model at its place that edge. The text has not changed since the
// Apply gave such a node, so the edge is the edge at the place that the
// node took.
func (d *Doc) slotNodes(x *model) {
	for ; x.slottedNodes < len(d.nodes); x.slottedNodes++ {
		n := d.nodes[x.slottedNodes]
		n.slot, n.slotted = d.edgeOf(x, n), true
	}
}

// edgeOf returns the edge of the model at the place of n, or nil when the
// place does not hold a value.
func (d *Doc) edgeOf(x *model, n *Node) *edge {
	if n.err != nil || n.gone || n.ended {
		return nil
	}

	p, err := d.nodePlace(n)
	if err != nil || !p.exists {
		return nil
	}

	return x.placeOf(p).edge
}

// displaced reports whether the path of n reads another value than the
// place of n, after an earlier edit of the Apply gave that path to another
// value. A node whose place did not hold a value takes the value that a
// later edit writes there. A node can sit on an entry that its path no
// longer selects. The node then selects the entry by the place of its key,
// as [Doc.readdress] gives it, so that node is not displaced.
func (d *Doc) displaced(n *Node) bool {
	x := d.model
	if x == nil || !n.slotted || x.synced != d.node {
		return false
	}

	// A path that reads no value reads another value than the place of n
	// when the text still holds that place.
	cur := d.edgeOf(x, n)
	if cur == nil {
		return n.slot != nil && n.slot.seen == x.epoch && !d.readdress(n)
	}

	if cur == n.slot {
		return false
	}

	if n.slot == nil {
		n.slot = cur

		return false
	}

	return !d.readdress(n)
}

// readdress makes the path of n select the entry at the place of n, as
// [Doc.slotPlace] finds it, by the place of its key, as a node on an entry
// that a later entry of its key hides selects it. An earlier edit can give
// the name of the entry to another entry that its mapping reads in place of
// it, as a rename of a key that a later `<<` merge key brings in does. The
// node then keeps reading and editing its own entry. It reports false, and
// leaves n as it is, when the path through that place does not select the
// entry.
func (d *Doc) readdress(n *Node) bool {
	p, ok := d.slotPlace(n)
	if !ok || p.entry == nil || n.path.IsRoot() {
		return false
	}

	depth := n.path.Len()
	hidden := slices.Clip(n.hidden)

	if last := len(hidden) - 1; last >= 0 && hidden[last].depth == depth {
		hidden = hidden[:last]
	}

	hidden = append(hidden, hiddenEntry{at: d.entryPlace(p.entry), depth: depth})

	got, err := d.walk(n.path, hidden, nil)
	if err != nil || !got.exists || got.entry != p.entry {
		return false
	}

	n.hidden = hidden

	return true
}

// use is the use that a call makes of its place, as the problem of a node
// whose path reads another value names it.
type use uint8

const (
	// The call edits the value at its place.
	useEdit use = iota
	// The call edits the key of the entry at its place, as SetKey does.
	useKey
	// The call creates its place when the text does not hold it, as Set and
	// the target of a move do.
	useCreate
	// The call reads the value at its place, as a decode does.
	useRead
)

// verb returns what a call of the use u does with a value, as a message
// names it after "cannot".
func (u use) verb() string {
	if u == useRead {
		return "read"
	}

	return "change"
}

// displacedProblem returns the problem of a call of the use u through n, a
// node that [Doc.displaced] reports, when the text does not differ from
// the model. It binds at the place of n when the text still holds the
// value there, and at the value that the path of n reads otherwise. Its
// message names the last call that renamed, moved, created, or removed the
// entry at the path or the entry of the place. The Apply records the
// problem.
func (d *Doc) displacedProblem(n *Node, u use) error {
	x := d.model

	msg := fmt.Sprintf("%s cannot %s this value, because an earlier edit made %s read another value",
		d.subject(), u.verb(), n.path)

	call := n.slot.changed
	if cur := d.edgeOf(x, n); cur != nil {
		call = max(call, cur.changed)
	}

	if call > 0 && call <= len(x.calls) {
		msg = fmt.Sprintf("%s cannot %s this value, because %s %s, so %s reads another value",
			d.subject(), u.verb(), d.subject(), x.calls[call-1].named, n.path)
	}

	if p, ok := d.slotPlace(n); ok {
		// The problem binds under the path that the user's text spells where
		// it binds. No path of the current text selects the place.
		s := d.placeRange(n, p)

		err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(s.path), niceyaml.AtRange(s.at),
			niceyaml.WithDetails(d.callDetails(s)...)))
		d.record(err)

		return err //nolint:wrapcheck // The binding is the problem of the edit.
	}

	return d.fail(n, "%s", msg)
}

// slotPlace returns the place of the current text that holds the value of
// the edge of the model at the place of n. It reports false when the text
// does not hold that value.
func (d *Doc) slotPlace(n *Node) (*place, bool) {
	x := d.model
	if n.slot == nil || n.slot.seen != x.epoch || n.slot.mate == nil || n.slot.mate.owner == nil {
		return nil, false
	}

	te := n.slot.mate
	owner := te.owner.node

	if te.entry != nil {
		return &place{container: owner, entry: te.entry, value: te.entry.Value, exists: true}, true
	}

	seq, ok := owner.(*ast.SequenceNode)
	if !ok {
		return nil, false
	}

	i := slices.Index(te.owner.edges, te)
	if i < 0 || i >= len(seq.Values) {
		return nil, false
	}

	return &place{container: seq, seq: seq, index: i, value: seq.Values[i], exists: true}, true
}
