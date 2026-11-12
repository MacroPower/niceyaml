package edit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Update gives the place the value v, as [Node.Set] gives it, but it
// rewrites only the text that must change. The comments, the blank lines,
// the key order, and the quotes inside the value stay wherever v keeps the
// value that the text holds. A [Func] thus decodes a section of a document
// into a type, changes a field, and writes the section back:
//
//	cfg, err := n.Decode[Config](ctx)
//	if err != nil {
//		return err
//	}
//
//	cfg.Server.Port = 9090
//
//	return n.Update(ctx, cfg)
//
// After Update, the place holds the value that a Set of v leaves there, as
// a decode reads it. Update encodes v once. Before it changes the text, it
// fails where the encode of a Set of v fails, with the same problems. It
// then compares v with the value of the place and makes the changes that
// they differ by:
//
//   - A value that decodes equal to its part of v stays as it is, with its
//     comments, its style, its tags, and its aliases.
//   - Two mappings pair their entries by key. Update removes each entry
//     whose key v lacks, with its comments, as [Node.Remove] does. It
//     compares the values of each pair. It creates each entry of v that the
//     mapping lacks after the entry of the key before it in v, or first
//     when no key comes before it.
//   - Two sequences pair the items that decode equal, in order, as many of
//     them as they can. Update compares the other items between two such
//     pairs at the same index. It removes the items that the text holds
//     past them, and it inserts the items that v holds past them, as
//     [Node.Insert] does. The items that the two sequences share at their
//     start and at their end pair first. The count of the other items of
//     the text times the count of the other items of v can be more than
//     1,048,576. None of those other items pair then, so Update compares
//     them at the same index.
//   - Any other value takes its part of v as a Set of that place writes
//     it, and so keeps the comments of its entry or its item. Such values
//     include a scalar that decodes to another value, a value of another
//     kind, and an alias whose anchor holds another value.
//
// Each of those changes is an edit at its own place. The edit fails where
// the same call of Set, Remove, or Insert at that place fails. So Update
// can fail where a Set of the whole value passes, as with [ErrUnsupported]
// when it reaches a construct that the engine does not edit. Update also
// fails with ErrUnsupported, as [ConstructMergeKeys] names it, where v
// changes an entry that a `<<` merge key brings into its mapping, or
// leaves one out. A Set of the mapping writes its entries in place of the
// merge key instead. A new entry of a mapping with a merge key goes after
// its last entry.
//
// An edit inside an anchored value changes each alias that reads it, as
// for any call. An alias inside the place can read a value that Update
// changes. Update then replaces the alias with its part of v, as a Set of
// the alias does, so the place holds v. A merge key inside the place that
// reads such a value fails the Update with ErrUnsupported. Update makes its
// edits from the end of the text to its start. An alias follows its anchor
// in the text. So Update removes or replaces a value that holds an alias or
// a merge key before it changes the anchor that the alias reads.
//
// At the root of the document, a Set replaces the root. So Update fails
// there when the root holds a value of another kind than v, or a scalar
// that decodes to another value. A place that does not hold a value takes
// v as a Set there writes it, with the options of opts. Inside a value
// that the place holds, Update ignores the options.
//
// Update is one call, so the details of its problems name it, as in "the
// edit updates $.server". Its checks of the model and of the whole text
// run once for all its edits. Update parses the text once for each run of
// scalars that it replaces with scalars. So the time of such changes grows
// with the size of the text plus their number. Each other edit parses the
// whole text, such as a remove, an insert, a new entry, or a value of
// another kind. So the time of an Update that removes or adds many entries
// or items grows with their number times the size of the text. The time of
// the same calls of Remove, Insert, and Set grows that way too.
//
// [Doc.Removals] lists each value of the user's text that Update removes.
// A key that v holds and that the text lacks reads back as its name, as
// for each key that an edit writes.
func (n *Node) Update(ctx context.Context, v any, opts ...SetOption) error {
	var cfg setConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	c := n.d.modelStart(n, "updates "+n.path.String(), false)

	change, err := n.update(ctx, v, cfg, c)

	return n.d.modelEnd(c, err, func() bool { return change != nil && change() })
}

// update makes the edits of [Node.Update] in the call c, and returns the
// change of the call in the model.
func (n *Node) update(ctx context.Context, v any, cfg setConfig, c *modelCall) (func() bool, error) {
	d := n.d

	p, err := n.place(useCreate, "updates")
	if err != nil {
		return nil, err
	}

	if !p.exists || c == nil {
		mv, where, err := n.set(ctx, v, false, cfg, "updates")
		if err != nil {
			return nil, err
		}

		return func() bool { return c.set(mv, where) }, nil
	}

	named := "updates " + n.path.String()

	r, err := d.updateRender(ctx, n, p, v, named)
	if err != nil {
		return nil, err
	}

	te, ov, ok := d.textAt(c.x, p)
	if !ok {
		return nil, d.bug(n, fmt.Errorf("the check cannot follow %s %s", d.subject(), named))
	}

	pl := &updatePlan{
		d: d, n: n, named: named, enc: r.enc,
		dirty: make(map[*value]bool), same: make(map[[2]*value]bool), plains: make(map[*value]any),
	}

	if p.root && !pl.equal(ov, r.value) && (ov.kind != r.value.kind || ov.kind == vScalar) {
		return nil, d.failAs(ErrUsage, n, "%s %s, which replaces the root of the document", d.subject(), named)
	}

	err = pl.walk(n.path, te, ov, nil, r.value)
	if err == nil {
		err = pl.check()
	}

	if err != nil {
		return nil, err
	}

	err = pl.run(ctx)
	if err != nil {
		return nil, err
	}

	// The model names the call by its own words, not by the last of its
	// edits.
	if pl.changed() {
		at, err := d.nodePlace(n)
		if err == nil && at.exists {
			d.recordEdit(at, named, "")
		}
	}

	return func() bool { return pl.change(c) }, nil
}

// updateRender returns v as [render] writes it for p, the place of n. It
// first makes the checks of the encode that a Set of v at p makes, in the
// order that [Doc.renderPayload] makes them. Named names the call, as in
// "updates $.a".
func (d *Doc) updateRender(ctx context.Context, n *Node, p *place, v any, named string) (rendered, error) {
	origin := d.addNote(d.setNote(p, named))
	step := d.stepOf(p, n.path)
	seqIndent := d.sequenceIndent()

	r, err := render(ctx, v, step, seqIndent, noFlow, d.cfg.marshalers)

	failed := err
	if len(r.refused) > 0 && !errors.Is(err, errScalarKind) {
		err = nil
	}

	switch {
	case errors.Is(err, errEncoder):
		return rendered{}, d.valueBug(n, origin, err)
	case err != nil:
		return rendered{}, d.valueProblem(n, err)
	}

	if failed == nil {
		pl := d.payloadOf(r, v, step, origin, seqIndent, nil)
		if what, ok := pl.unprintable(); ok {
			return rendered{}, d.valueBug(n, origin, unprintableError(what))
		}

		err = d.ownRepeat(n, r.value, origin)
		if err != nil {
			return rendered{}, err
		}
	}

	if len(r.refused) > 0 {
		return rendered{}, d.valueRefusal(n, origin, &r.refused[0], named)
	}

	return r, nil
}

// textAt returns the edge and the value of the graph of the current text
// that x read last, at p, a place that holds a value. The edge is nil for
// the root. It reports false when the graph does not hold the place.
func (d *Doc) textAt(x *model, p *place) (*edge, *value, bool) {
	g := x.text

	switch {
	case p.root:
		return nil, g.root, g.root != nil
	case p.entry != nil:
		te := g.entries[p.entry]
		if te == nil {
			return nil, nil, false
		}

		return te, te.val, true

	case p.seq != nil:
		sv := g.values[p.seq]
		if sv == nil || p.index >= len(sv.edges) {
			return nil, nil, false
		}

		te := sv.edges[p.index]

		return te, te.val, true
	}

	return nil, nil, false
}

// updateKind is the kind of an [updateOp].
type updateKind uint8

const (
	// A Set of a place that holds a value.
	opSet updateKind = iota
	// A Set that creates an entry of a mapping.
	opCreate
	// An Insert of an item of a sequence.
	opInsert
	// A Remove of an entry or an item.
	opRemove
)

// updateOp is one edit of an Update, at a place of the text that the
// Update found.
type updateOp struct {
	nv    *value
	ne    *edge
	te    *edge
	owner *value
	mv    *edge
	name  string
	at    placement
	path  paths.Path
	// The offset in the text that the Update found where the edit writes,
	// which orders the edits, as [updatePlan.run] describes.
	pos   int
	kind  updateKind
	keeps bool
}

// seqItem is an item of a sequence after an Update: an item of the text,
// or one that the Update inserts.
type seqItem struct {
	te *edge
	op *updateOp
}

// seqPlan is the order of the items of a sequence of the text after an
// Update, which the model takes.
type seqPlan struct {
	owner   *value
	items   []seqItem
	removed []*edge
}

// keptPart is a value of the text that an Update leaves as it is, because
// it decodes equal to its part of the new value.
type keptPart struct {
	te, ne *edge
	ov, nv *value
	path   paths.Path
}

// mergeRead is a `<<` merge key of a mapping that an Update reads the
// entries of, at the path of its mapping.
type mergeRead struct {
	e    *edge
	path paths.Path
}

// updatePlan is the plan of the edits of one Update, as [Node.Update]
// describes them. The plan reads the graph of the text that the Update
// found and the graph of the encode of its value. It makes the edits in an
// order that keeps the path of each edit on its place.
type updatePlan struct {
	d     *Doc
	n     *Node
	enc   *Doc
	dirty map[*value]bool
	same  map[[2]*value]bool
	// The value as a decode into any reads it, for each value that the plan
	// compared that way.
	plains map[*value]any
	named  string
	// The edits that replace an alias, and the other edits, in the order
	// that [updatePlan.run] keeps between two edits at one offset.
	first []*updateOp
	ops   []*updateOp
	seqs  []*seqPlan
	kept  []keptPart
	reads []mergeRead
	// The values of the text that hold the place that the walk compares.
	stack []*value
}

// changed reports whether the plan makes an edit.
func (pl *updatePlan) changed() bool {
	return len(pl.first) > 0 || len(pl.ops) > 0
}

// walk compares ov, the value of the text at path, with nv, its part of
// the new value, and plans the edits that they differ by. The edge te
// holds ov, and ne holds nv. Te is nil at the root of the document, and ne is nil at
// the root of the encode.
func (pl *updatePlan) walk(path paths.Path, te *edge, ov *value, ne *edge, nv *value) error {
	if pl.equal(ov, nv) {
		pl.kept = append(pl.kept, keptPart{path: path, te: te, ov: ov, ne: ne, nv: nv})

		return nil
	}

	switch {
	case te != nil && te.alias:
		pl.mark(nil)

		pl.first = append(pl.first, &updateOp{kind: opSet, path: path, te: te, ne: ne, nv: nv, pos: pl.start(te)})

		return nil

	case ov.kind != nv.kind || ov.kind != vMapping && ov.kind != vSequence:
		pl.mark(ov)

		pl.ops = append(pl.ops, &updateOp{kind: opSet, path: path, te: te, ne: ne, nv: nv, pos: pl.start(te)})

		return nil
	}

	pl.stack = append(pl.stack, ov)
	defer func() { pl.stack = pl.stack[:len(pl.stack)-1] }()

	if ov.kind == vSequence {
		return pl.sequence(path, ov, nv)
	}

	return pl.mapping(path, ov, nv)
}

// mark records that the plan changes the values that hold the place that
// the walk compares. It also records the whole of old, a value of the text
// that an edit replaces or removes, or nil. An alias that reads such a value no
// longer reads the value that it read.
func (pl *updatePlan) mark(old *value) {
	for _, v := range pl.stack {
		pl.dirty[v] = true
	}

	var walk func(v *value)

	walk = func(v *value) {
		if v == nil || pl.dirty[v] {
			return
		}

		pl.dirty[v] = true

		for _, e := range v.edges {
			if !e.alias {
				walk(e.val)
			}
		}
	}

	walk(old)
}

// mapping plans the edits of ov, a mapping of the text at path, whose new
// value is nv, a mapping.
func (pl *updatePlan) mapping(path paths.Path, ov, nv *value) error {
	merges := mergesOf(ov)

	// The own entries of each key, in the order of the text.
	own := make(map[string][]*edge)

	for e := range ownEntries(ov) {
		own[e.key] = append(own[e.key], e)
	}

	// The value that a merge key brings in under each key that the mapping
	// does not write itself, as a decode reads it.
	merged := make(map[string]any)

	for _, m := range merges {
		pl.reads = append(pl.reads, mergeRead{e: m, path: path})
		mergeInto(merged, own, pl.plain(m.val))
	}

	used := make(map[*edge]bool)
	prev := ""

	// A new entry writes at the end of the entry of the text that it
	// follows, or at the start or the end of the mapping.
	at := pl.bound(ov.node, false)
	if len(merges) > 0 {
		at = pl.bound(ov.node, true)
	}

	type pair struct {
		te, ne *edge
	}

	var (
		pairs   []pair
		created []*updateOp
	)

	for _, ne := range nv.edges {
		if olds := own[ne.key]; len(olds) > 0 {
			te := olds[0]
			own[ne.key] = olds[1:]
			used[te] = true
			pairs = append(pairs, pair{te: te, ne: ne})
			prev = te.name

			if len(merges) == 0 {
				at = pl.end(te)
			}

			continue
		}

		if was, ok := merged[ne.key]; ok {
			delete(merged, ne.key)

			if sameValue(was, pl.plain(ne.val)) {
				continue
			}

			return pl.mergeProblem(path, merges, ne.key, "with another value")
		}

		if !ne.named {
			return pl.unnamed(path, ne.key)
		}

		where := placement{kind: placeLast}

		switch {
		case len(merges) > 0:
		case prev == "":
			where.kind = placeFirst
		default:
			where = placement{kind: placeAfter, key: prev}
		}

		created = append(created, &updateOp{
			kind: opCreate, path: path.Child(ne.name), owner: ov, ne: ne, nv: ne.val, name: ne.name, at: where,
			pos: at,
		})
		prev = ne.name
	}

	if len(merged) > 0 {
		keys := slices.Sorted(maps.Keys(merged))

		return pl.mergeProblem(path, merges, keys[0], "that the value does not hold")
	}

	for e := range ownEntries(ov) {
		if used[e] {
			continue
		}

		if !e.named {
			return pl.unnamed(path, e.key)
		}

		pl.mark(e.val)

		pl.ops = append(pl.ops, &updateOp{kind: opRemove, path: path.Child(e.name), te: e, owner: ov, pos: pl.start(e)})
	}

	for _, op := range created {
		pl.mark(nil)

		pl.ops = append(pl.ops, op)
	}

	for _, p := range pairs {
		// No edit reaches inside an entry that the plan keeps, so its key
		// needs a name only when the values differ.
		if !p.te.named {
			if pl.equal(p.te.val, p.ne.val) {
				continue
			}

			return pl.unnamed(path, p.te.key)
		}

		err := pl.walk(path.Child(p.te.name), p.te, p.te.val, p.ne, p.ne.val)
		if err != nil {
			return err
		}
	}

	return nil
}

// unnamed returns the problem of an Update that edits the entry of key in
// the mapping at path, whose key no path names.
func (pl *updatePlan) unnamed(path paths.Path, key string) error {
	return pl.d.failAs(ErrUsage, pl.n, "%s %s, but no path names the key %q of %s, so set %s with Set",
		pl.d.subject(), pl.named, strings.TrimPrefix(key, "\x00"), path, path)
}

// start returns the offset in the text where te, an entry or an item of
// the text, starts: at its key, or at its value for an item.
func (pl *updatePlan) start(te *edge) int {
	if te.entry != nil {
		if s, ok := pl.d.nodeSpan(te.entry.Key); ok {
			return s.start
		}
	}

	if astnode.IsNil(te.at) {
		return pl.bound(te.val.node, false)
	}

	return pl.bound(te.at, false)
}

// end returns the offset in the text where te, an entry or an item of the
// text, ends.
func (pl *updatePlan) end(te *edge) int {
	at := pl.start(te)

	if te.entry != nil {
		if s, ok := pl.d.nodeSpan(te.entry.Key); ok {
			at = max(at, s.end)
		}
	}

	if !astnode.IsNil(te.at) {
		at = max(at, pl.bound(te.at, true))
	}

	return at
}

// bound returns the offset in the text where node starts, or where it ends
// when end is true, as [Doc.valueSpan] gives its bytes, or 0 when the text
// does not hold the node.
func (pl *updatePlan) bound(node ast.Node, end bool) int {
	if astnode.IsNil(node) {
		return 0
	}

	s, ok := pl.d.valueSpan(node)

	switch {
	case !ok:
		return 0
	case end:
		return s.end
	default:
		return s.start
	}
}

// mergeInto adds to m each entry of src, the value that a `<<` merge key
// reads as a decode reads it, whose key neither m nor own holds. A
// sequence merges each of its mappings in order.
func mergeInto(m map[string]any, own map[string][]*edge, src any) {
	switch x := src.(type) {
	case map[string]any:
		for k, v := range x {
			if _, ok := m[k]; ok {
				continue
			}

			if _, ok := own[k]; ok {
				continue
			}

			m[k] = v
		}

	case []any:
		for _, item := range x {
			mergeInto(m, own, item)
		}
	}
}

// mergeProblem returns the problem of an Update of the mapping at path,
// whose `<<` merge keys are merges. The new value changes the entry of key
// that a merge key brings in. What says how the new value differs,
// as in "with another value".
func (pl *updatePlan) mergeProblem(path paths.Path, merges []*edge, key, what string) error {
	d := pl.d
	at := d.nodeSite(merges[0].entry.Key, true)
	pos := at.at.Start

	in := instance{
		construct: ConstructMergeKeys,
		at:        at,
		but: fmt.Sprintf(", but the `<<` merge key at %s brings %s into %s %s",
			pos.OneBased(), key, path, what),
		fix: fmt.Sprintf("set %s with Set, which writes the entries in place of the merge key", path),
	}

	return d.refusal(&in, pl.named)
}

// sequence plans the edits of ov, a sequence of the text at path, whose
// new value is nv, a sequence. The items of the two that decode equal pair
// in order, as many of them as can. The edits go from the last item to the
// first, so no edit moves the index of an item that a later edit reaches.
func (pl *updatePlan) sequence(path paths.Path, ov, nv *value) error {
	olds, news := ov.edges, nv.edges
	matches := pl.align(olds, news)

	sp := &seqPlan{owner: ov}
	pl.seqs = append(pl.seqs, sp)

	// The items of the new value in order, as the model takes them.
	items := make([]seqItem, len(news))

	// The gaps between the pairs, from the last.
	matches = append(matches, [2]int{len(olds), len(news)})

	for g, matche := range slices.Backward(matches) {
		oi, nj := 0, 0
		if g > 0 {
			oi, nj = matches[g-1][0]+1, matches[g-1][1]+1
		}

		lo, ln := matche[0]-oi, matche[1]-nj
		k := min(lo, ln)

		if g < len(matches)-1 {
			i, j := matche[0], matche[1]
			items[j] = seqItem{te: olds[i]}
			pl.kept = append(pl.kept, keptPart{
				path: path.Index(i), te: olds[i], ov: olds[i].val, ne: news[j], nv: news[j].val,
			})
		}

		for t := lo - 1; t >= k; t-- {
			e := olds[oi+t]
			sp.removed = append(sp.removed, e)

			pl.mark(e.val)

			pl.ops = append(pl.ops, &updateOp{
				kind: opRemove, path: path.Index(oi + t), te: e, owner: ov, pos: pl.start(e),
			})
		}

		// An item that the Update inserts writes before the item of the
		// text at its index, or at the end of the sequence.
		at := pl.bound(ov.node, true)
		if oi+k < len(olds) {
			at = pl.start(olds[oi+k])
		}

		for t := ln - 1; t >= k; t-- {
			op := &updateOp{
				kind: opInsert, path: path.Index(oi + k), owner: ov, ne: news[nj+t], nv: news[nj+t].val, pos: at,
			}
			items[nj+t] = seqItem{op: op}

			pl.mark(nil)

			pl.ops = append(pl.ops, op)
		}

		for t := k - 1; t >= 0; t-- {
			e := olds[oi+t]
			items[nj+t] = seqItem{te: e}

			err := pl.walk(path.Index(oi+t), e, e.val, news[nj+t], news[nj+t].val)
			if err != nil {
				return err
			}
		}
	}

	sp.items = items

	return nil
}

// maxAlign is the most pairs of items that [updatePlan.align] compares
// between the items that two sequences do not share at their ends. The
// doc of [Node.Update] states its value.
const maxAlign = 1 << 20

// align returns the pairs of the indexes of olds and news, the items of
// two sequences, whose values decode equal, in order. It returns as many
// pairs as such an order allows, the longest common subsequence of the
// two. The items that the two share at their start and at their end pair
// first. The items between them pair only when they take maxAlign
// comparisons or fewer.
func (pl *updatePlan) align(olds, news []*edge) [][2]int {
	var out [][2]int

	head := 0
	for head < len(olds) && head < len(news) && pl.equal(olds[head].val, news[head].val) {
		out = append(out, [2]int{head, head})
		head++
	}

	tail := 0
	for tail < len(olds)-head && tail < len(news)-head &&
		pl.equal(olds[len(olds)-1-tail].val, news[len(news)-1-tail].val) {
		tail++
	}

	a, b := olds[head:len(olds)-tail], news[head:len(news)-tail]

	if len(a) > 0 && len(b) > 0 && len(a)*len(b) <= maxAlign {
		// The length of the longest common subsequence of a[i:] and b[j:].
		w := len(b) + 1
		lcs := make([]int, (len(a)+1)*w)

		for i, x := range slices.Backward(a) {
			for j, y := range slices.Backward(b) {
				switch {
				case pl.equal(x.val, y.val):
					lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
				default:
					lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
				}
			}
		}

		for i, j := 0, 0; i < len(a) && j < len(b); {
			switch {
			case pl.equal(a[i].val, b[j].val):
				out = append(out, [2]int{head + i, head + j})
				i++
				j++

			case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
				i++
			default:
				j++
			}
		}
	}

	for t := tail; t > 0; t-- {
		out = append(out, [2]int{len(olds) - t, len(news) - t})
	}

	return out
}

// equal reports whether o, a value of the text, and n, a value of the
// encode of an Update, decode to equal values.
func (pl *updatePlan) equal(o, n *value) bool {
	key := [2]*value{o, n}
	if eq, ok := pl.same[key]; ok {
		return eq
	}

	var eq bool

	switch {
	case o.kind == vReference || n.kind == vReference || len(mergesOf(o)) > 0 || len(mergesOf(n)) > 0:
		eq = sameValue(pl.plain(o), pl.plain(n))
	case o.kind != n.kind:
	case o.kind == vScalar:
		eq = sameScalar(o.sc, n.sc)
	case o.kind == vSequence:
		eq = len(o.edges) == len(n.edges)

		for i := 0; eq && i < len(o.edges); i++ {
			eq = pl.equal(o.edges[i].val, n.edges[i].val)
		}

	default:
		pairs, ok := matchEntries(o, n)
		eq = ok

		for i := 0; eq && i < len(pairs); i++ {
			eq = pl.equal(pairs[i][0].val, pairs[i][1].val)
		}
	}

	pl.same[key] = eq

	return eq
}

// plain returns v as a decode into any reads it. The own entries of a
// mapping come first, and then the entries that its `<<` merge keys bring
// in, the earlier merge first.
func (pl *updatePlan) plain(v *value) any {
	if got, ok := pl.plains[v]; ok {
		return got
	}

	var out any

	switch v.kind {
	case vScalar, vReference:
		out = v.sc.get()

	case vSequence:
		items := make([]any, 0, len(v.edges))
		for _, e := range v.edges {
			items = append(items, pl.plain(e.val))
		}

		out = items

	case vMapping:
		m := make(map[string]any, len(v.edges))
		own := make(map[string][]*edge)

		for e := range ownEntries(v) {
			m[e.key] = pl.plain(e.val)
			own[e.key] = nil
		}

		for _, e := range mergesOf(v) {
			mergeInto(m, own, pl.plain(e.val))
		}

		out = m
	}

	pl.plains[v] = out

	return out
}

// check plans an edit for each alias inside a value that the plan keeps
// whose anchor holds a value that the plan changes. The edit replaces the
// alias with its part of the new value, before the other edits. Check
// returns the problem of a `<<` merge key that reads such a value, which
// no edit of the alias can keep.
func (pl *updatePlan) check() error {
	if len(pl.dirty) == 0 {
		return nil
	}

	for _, r := range pl.reads {
		if pl.readsChanged(r.e) {
			return pl.mergeProblem(r.path, []*edge{r.e}, "entries", "that the update changes")
		}
	}

	for _, k := range pl.kept {
		err := pl.scan(k.path, k.te, k.ov, k.ne, k.nv)
		if err != nil {
			return err
		}
	}

	return nil
}

// readsChanged reports whether e, a `<<` merge key, reads a value that the
// plan changes, as a mapping or as an item of a sequence of mappings.
func (pl *updatePlan) readsChanged(e *edge) bool {
	if pl.dirty[e.val] {
		return true
	}

	if e.val.kind != vSequence {
		return false
	}

	return slices.ContainsFunc(e.val.edges, func(item *edge) bool { return pl.dirty[item.val] })
}

// scan plans the edits inside ov, a value of the text at path that the
// plan keeps, as [updatePlan.check] describes them. Te holds ov, or it is
// nil at the root, and nv is its part of the new value, which ne holds.
func (pl *updatePlan) scan(path paths.Path, te *edge, ov *value, ne *edge, nv *value) error {
	if te != nil && te.alias {
		if pl.dirty[ov] {
			pl.first = append(pl.first, &updateOp{kind: opSet, path: path, te: te, ne: ne, nv: nv, pos: pl.start(te)})
		}

		return nil
	}

	switch ov.kind {
	case vSequence:
		if nv.kind != vSequence || len(nv.edges) != len(ov.edges) {
			return nil
		}

		for i, e := range ov.edges {
			err := pl.scan(path.Index(i), e, e.val, nv.edges[i], nv.edges[i].val)
			if err != nil {
				return err
			}
		}

	case vMapping:
		if nv.kind != vMapping {
			return nil
		}

		for _, m := range mergesOf(ov) {
			if pl.readsChanged(m) {
				return pl.mergeProblem(path, []*edge{m}, "entries", "that the update changes")
			}
		}

		news := make(map[string]*edge, len(nv.edges))
		for _, e := range nv.edges {
			if _, ok := news[e.key]; !ok {
				news[e.key] = e
			}
		}

		for e := range ownEntries(ov) {
			n, ok := news[e.key]
			if !ok || !e.named {
				continue
			}

			err := pl.scan(path.Child(e.name), e, e.val, n, n.val)
			if err != nil {
				return err
			}
		}

	case vScalar, vReference:
	}

	return nil
}

// run makes the edits of the plan, each through a node at its path, as a
// call of Set, Insert, or Remove there makes it. The edits go from the last
// offset of the text to the first, and two edits at one offset keep the
// order of the plan. So no edit moves the index of an item that a later
// edit reaches. An alias follows its anchor in the text. So an edit that
// removes or replaces an alias, or a `<<` merge key, comes before an edit
// that changes the anchor that it reads.
//
// Some Sets replace a scalar of the text without an anchor with a scalar.
// Each such Set writes inside the bytes of its scalar. So the places and
// the bytes of the text stay where the Sets after it find them. The Sets
// of such scalars in a row parse the text once, after the last of them.
func (pl *updatePlan) run(ctx context.Context) error {
	d := pl.d

	ops := slices.Concat(pl.first, pl.ops)
	slices.SortStableFunc(ops, func(a, b *updateOp) int { return cmp.Compare(b.pos, a.pos) })

	defer func() {
		d.deferring, d.deferred = false, nil

		if d.model != nil {
			d.model.keepsAnchor = false
		}
	}()

	for _, op := range ops {
		d.deferring = op.scalar()

		if !d.deferring && len(d.deferred) > 0 {
			err := d.apply()
			if err != nil {
				return d.bug(pl.n, err)
			}
		}

		h := &Node{d: d, path: op.path, hidden: slices.Clone(pl.n.hidden)}

		var (
			v   any
			err error
		)

		if op.kind != opRemove {
			v, err = pl.leafValue(op.ne, op.nv)
			if err != nil {
				return err
			}
		}

		switch op.kind {
		case opSet:
			if d.model != nil {
				d.model.keepsAnchor = false
			}

			op.mv, _, err = h.set(ctx, v, false, setConfig{}, "sets")
			op.keeps = d.model != nil && d.model.keepsAnchor

		case opCreate:
			op.mv, _, err = h.set(ctx, v, false, setConfig{at: op.at}, "sets")

		case opInsert:
			op.mv, err = h.insert(ctx, v)

		case opRemove:
			err = h.remove()
		}

		if err != nil {
			return err
		}
	}

	d.deferring = false

	if len(d.deferred) > 0 {
		err := d.apply()
		if err != nil {
			return d.bug(pl.n, err)
		}
	}

	return nil
}

// scalar reports whether op is a Set of a scalar of the text, which no
// alias writes and which holds no anchor, to a scalar.
func (op *updateOp) scalar() bool {
	if op.kind != opSet || op.te == nil || op.te.alias || op.te.val.kind != vScalar || op.nv.kind != vScalar {
		return false
	}

	_, anchored := keptAnchorOf(op.te.at)

	return !anchored
}

// errEncodeSpan is the bug of an Update whose encode does not hold the
// bytes of a part of its value.
var errEncodeSpan = errors.New("the encode of the value does not hold the bytes of its part")

// rawYAML is YAML text that a Set writes as the YAML of a marshaler.
type rawYAML string

// MarshalYAML returns the text of r.
func (r rawYAML) MarshalYAML() ([]byte, error) {
	return []byte(r), nil
}

// leafValue returns the Go value that an edit of the plan writes for nv, a
// value of the encode of the Update. The edge ne holds nv, or nv is the
// root of the encode for a nil ne. A string without a tag is a Go string,
// so the edit keeps the quotes of a scalar that it replaces. Any other
// value is the text of the encode, so the edit writes the value in its
// style, with its tags.
func (pl *updatePlan) leafValue(ne *edge, nv *value) (any, error) {
	enc := pl.enc

	at := enc.node.AST()
	if ne != nil {
		at = ne.at
	}

	switch underAnchors(at).(type) {
	case *ast.StringNode, *ast.LiteralNode:
		if s, ok := nv.sc.get().(string); ok {
			return s, nil
		}
	}

	s, ok := enc.nodeSpan(at)
	if !ok {
		return nil, pl.d.bug(pl.n, errEncodeSpan)
	}

	// A block scalar that ends the value holds the line break of its last
	// line and the blank lines after it. Its chomping indicator keeps or
	// drops them.
	if endScalar(at) != nil && s.end > s.start {
		l := enc.lineOf(s.end-1) + 1
		for l < len(enc.curLines) && enc.lineStart(l) < len(enc.cur) && enc.isBlankLine(l) {
			l++
		}

		s.end = enc.lineStart(l)
	}

	text := enc.cur[s.start:s.end]

	// The lines of a block collection after its first line indent past the
	// column of its first entry. The YAML of a marshaler starts that entry
	// at the start of its line, so the lines lose that indent. A block
	// scalar finds the indent of its lines itself, because the encode
	// writes no indentation indicator.
	content := astnode.Content(at)
	if !strings.Contains(text, "\n") || !isBlockCollection(content) {
		return rawYAML(text), nil
	}

	cs, ok := enc.nodeSpan(content)
	if !ok {
		return nil, pl.d.bug(pl.n, errEncodeSpan)
	}

	col := enc.column(cs.start)
	lines := strings.Split(text, "\n")

	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " ")
		lines[i] = lines[i][min(col, len(lines[i])-len(trimmed)):]
	}

	return rawYAML(strings.Join(lines, "\n")), nil
}

// change makes the change of the edits of the plan in the model of the
// call c. It reports false when the model cannot take it.
func (pl *updatePlan) change(c *modelCall) bool {
	x := c.x
	call := c.call()

	modelOf := func(te *edge) *edge {
		if te == nil {
			return x.top
		}

		return te.pair
	}

	for _, op := range slices.Concat(pl.first, pl.ops) {
		switch op.kind {
		case opSet:
			me := modelOf(op.te)
			if me == nil || op.mv == nil || op.mv.val == nil {
				return false
			}

			old := me.val
			me.val, me.tag = op.mv.val, op.mv.tag

			if op.keeps {
				x.share(old, op.mv)
			}

			x.wroteIn(op.mv.val)

		case opCreate:
			owner := op.owner.pair
			if owner == nil || op.mv == nil || op.mv.val == nil {
				return false
			}

			x.wroteIn(op.mv.val)

			top := attach(owner, paths.Selector{Kind: paths.SelectorChild, Name: op.name}, nil,
				&edge{val: op.mv.val, tag: op.mv.tag}, call)
			if !x.created(owner, top) {
				return false
			}

		case opRemove:
			if op.owner.kind != vMapping {
				continue
			}

			if op.owner.pair == nil || op.te.pair == nil || !detach(op.owner.pair, op.te.pair, call) {
				return false
			}

		case opInsert:
		}
	}

	for _, sp := range pl.seqs {
		owner := sp.owner.pair
		if owner == nil {
			return false
		}

		edges := make([]*edge, 0, len(sp.items))

		for _, it := range sp.items {
			if it.op == nil {
				if it.te == nil || it.te.pair == nil {
					return false
				}

				edges = append(edges, it.te.pair)

				continue
			}

			if it.op.mv == nil || it.op.mv.val == nil {
				return false
			}

			x.wroteIn(it.op.mv.val)

			e := &edge{val: it.op.mv.val, tag: it.op.mv.tag, changed: call}
			edges = append(edges, e)
			x.created(owner, e)
		}

		for _, e := range sp.removed {
			if e.pair != nil {
				e.pair.changed = call
			}
		}

		owner.edges = edges
	}

	return true
}
