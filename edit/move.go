package edit

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// Move moves the value and its comments to target, a `$` path, which it
// creates as [Node.Set] does. A target at the index past the last item of a
// sequence thus appends an item. A place that does not hold a value stays
// as it is. The comments include the comment lines
// that [Node.Remove] takes with the entry or the item, such as those that
// the user indented past its key or its "-". Move deletes blank lines next
// to the old lines of the value as Remove does.
//
// A Move makes the checks of a Remove of the value and an insertion of the
// same text at target. It passes where that pair passes, except where it
// refuses the move itself. It fails when target cannot take the value:
//
//   - Target holds a value or lies inside the value, through an alias too.
//   - The deepest value on target that the document holds cannot take the
//     rest of target, such as a scalar, or a sequence where target names a
//     key.
//
// Move fails with [ErrUnsupported] where it reaches a construct outside the
// YAML that the package documentation supports, as each call does. It also
// fails with ErrUnsupported where:
//
//   - Target lies in a flow collection, and the value is a block value
//     that holds a byte of the user's text, such as a scalar or a comment,
//     as [ConstructFlowLayout] names it. Move refuses such a value also
//     where the engine wrote the flow collection.
//   - An alias reads one anchor in the text that the move finds and
//     another anchor, or none, in the text that it leaves. An alias that
//     the move puts before its anchor does so. The engine does not write a
//     copy of a value at its alias.
//   - The moved text makes YAML that the engine does not edit, such as an
//     alias inside the value of the anchor that it reads.
//   - The value holds a part of the text that a call can take only whole,
//     such as a flow collection with a comment inside it or an entry with
//     an explicit key. Move writes the value again at target.
//
// The paths and the checks of Move bind each alias by the rule of
// [go.jacobcolvin.com/niceyaml/paths.Resolver], in the text before the
// move and after it, as a decode does. When go-yaml's decoder alone read
// the text that the Apply found as the decode does, it must also read the
// text after the move so. Otherwise the Move fails with [ErrEngine], and
// the error names go-yaml's decoder.
//
// A value that Move reaches through an alias or a `<<` merge key leaves the
// anchored value, for every place that reads it, as it does for Remove. A
// target through one goes into the anchored value, as a Set there does. A
// reference document of [go.jacobcolvin.com/niceyaml.WithReferences] that
// a merge key reads decides which names the mapping of the merge key reads.
// A Move of an entry of such a mapping, or into it, fails with a problem
// that matches [paths.ErrReferenceAlias].
//
// The value keeps its text, indented for its new place, with its anchors
// and its aliases. An empty value does not have text, so its key or its
// "-" ends the line there, or comes before the comment of the value. A
// value that leaves a flow collection takes the comment that [Node.Remove]
// deletes with it.
//
// In a flow collection, the value takes flow style and drops the comments
// of the entry and of the value. Each string there, as a key or a value,
// takes the text that go-yaml's flow encoder writes for it, unless the
// user quoted it on one line. Such a string keeps its quotes. So x:y and
// the YAML 1.1 words y, n, yes, and no take double quotes. A scalar of
// several lines, and a block scalar that the engine wrote, can come out
// plain. Each other scalar, such as 0x10, keeps its text, and an empty
// value becomes null.
//
// The value leaves its place before it goes in at target. The path to
// target can read an alias of an anchor that the value takes, such as a
// `<<` merge key after the mapping that holds target. When the text
// without the value resolves target to another place, with another value
// that holds it, the value goes in at target first and then leaves its
// place.
func (n *Node) Move(target paths.Path) error {
	c := n.d.modelStart(n, fmt.Sprintf("moves %s to %s", n.path, target), false)

	var to *modelCall

	if c != nil {
		to = n.d.modelAt(target)
	}

	err := n.move(target)

	return n.d.modelEnd(c, err, func() bool { return c.move(to) })
}

// move moves the value to target, as [Node.Move] describes.
//
//nolint:contextcheck // A move decodes the text after it, which reads no state that a context carries.
func (n *Node) move(target paths.Path) error {
	d := n.d

	p, err := n.place(useEdit, "moves")
	if err != nil {
		return err
	}

	// The text lacks the place, so the move does nothing, unless a step of
	// the path reaches a zone of the Apply.
	if !p.exists {
		return n.stepProblem()
	}

	// A message quotes the name of a key and names an item by its path.
	name := n.path.String()
	if last, ok := n.path.Last(); ok && last.Kind == paths.SelectorChild {
		name = strconv.Quote(last.Name)
	}

	why := d.protection(p, true)

	switch {
	case !target.IsAbsolute() || !plain(target):
		return d.fail(n, "%s moves %s to %s, which is no $ path of names and indexes", d.subject(), name, target)
	case target.IsRoot():
		return d.fail(n, "%s moves %s to $, but a value cannot move to the document root", d.subject(), name)
	case p.root:
		return d.fail(n, "%s moves the root of the document", d.subject())
	case why != "":
		return d.fail(n, "%s moves %s, which %s", d.subject(), name, why)
	}

	if _, inside := target.CutPrefix(n.path); inside {
		return d.fail(n, "%s moves %s to %s, which lies inside it", d.subject(), name, target)
	}

	// A step of the path to target before its problem can reach a zone of
	// the Apply, which decides the problem first, as [Doc.stepProblem]
	// tells.
	targetStep := reachCall{path: target, named: d.callNamed}

	tp, err := d.resolve(target)
	if err != nil {
		refused := d.stepProblem(targetStep)
		if refused != nil {
			return refused
		}

		return d.readFail(n, fmt.Errorf("%s moves %s to %s, which %w", d.subject(), name, target, err))
	}

	if whyTarget := d.protection(tp, true); whyTarget != "" {
		return d.fail(n, "%s moves %s to %s, which %s", d.subject(), name, target, whyTarget)
	}

	named := fmt.Sprintf("moves %s to %s", n.path, target)

	// A target that a kept merge brings in is a key that the Move would
	// hide, which rule M2 of the merge rules refuses before the target
	// holds a value. The zones that the Move reaches come first.
	reach := reachCall{kind: callMove, path: n.path, hidden: n.hidden, target: target, named: named}

	tw, walkErr := d.walkCall(target, nil)
	if walkErr == nil && tp.exists {
		if k, key, ok := d.mergedTarget(tw); ok {
			err = d.reachProblem(reach)
			if err != nil {
				return err
			}

			return d.mergeKeysProblem(k, new(d.holderSite(p)), key, true, named)
		}
	}

	switch {
	case tp.exists && p.entry != nil:
		// The problem is about the key that the move writes, so it binds at
		// the key of the entry.
		err := d.entryProblem(p.entry, n.path.Key(),
			fmt.Sprintf("%s moves %s to %s, which already exists", d.subject(), name, target))
		d.record(err)

		return err

	case tp.exists:
		// The problem is about the "-" that the move writes for the value, so
		// it binds where the "-" of the item binds.
		at := d.holderSite(p)

		err := d.user.Bind(niceyaml.NewError(
			fmt.Sprintf("%s moves %s to %s, which already exists", d.subject(), name, target),
			niceyaml.AtPath(at.path), niceyaml.AtRange(at.at), niceyaml.WithDetails(d.callDetails(at)...),
		))
		d.record(err)

		return err //nolint:wrapcheck // The binding is the problem of the edit.
	}

	th, why, ok := d.targetHolder(target, tp)
	if ok && why != "" {
		refused := d.stepProblem(targetStep)
		if refused != nil {
			return refused
		}
	}

	switch {
	case ok && why != "":
		return d.fail(n, "%s moves %s to %s, but %s", d.subject(), name, target, why)
	case ok && (th.content == astnode.Content(p.value) || d.liesIn(th.content, p.value)):
		// The path to target reads the value through an alias.
		return d.fail(n, "%s moves %s to %s, which lies inside it", d.subject(), name, target)
	}

	err = d.reachProblem(reach)
	if err != nil {
		return err
	}

	if p.entry != nil {
		err = d.ownKeysProblem(p.entry, p.container, "", false, named)
		if err != nil {
			return err
		}
	}

	if ok && d.inFlow(th.content) && d.userBlock(p.value) {
		return d.flowMoveProblem(p.value, named)
	}

	ctx := context.Background()

	d.recordEdit(p, fmt.Sprintf("moves %s to %s", name, target), fmt.Sprintf("moves %s to %s", n.path, target))

	// The paths at the value and below it move with it, and those below
	// the later items of its sequence take the index before them.
	from := n.path
	s := d.spotOf(from, p)

	var (
		moving = d.reached(s)
		items  *reached
	)

	if p.seq != nil {
		items = d.reached(d.itemsOf(from, p))
	}

	// The text of the value goes to target with its bytes, and the Move
	// writes no copy. Each alias must then read the anchor that it read.
	saved, callErr := d.save(), d.callErr
	bound := d.aliasBindings()

	moved, index, err := d.moveValue(n, p, target, named)

	moves := -1
	if err == nil {
		moves = d.rebound(bound)
	}

	// Go-yaml's decoder alone can read an alias of the text of the value
	// at target otherwise than the decode of the text does.
	apart := err == nil && moves < 0 && d.ownApart(ctx)

	if err != nil || moves >= 0 || apart {
		d.restore(saved)

		d.callErr = callErr
	}

	switch {
	case err != nil:
		return err
	case moves >= 0:
		return d.moveOrderProblem(moves, named)
	case apart:
		return d.bug(n, fmt.Errorf("%s %s, but go-yaml's decoder reads an alias of the text after the move "+
			"otherwise than the decode of the text does", d.subject(), named))
	}

	if items != nil {
		items.shift(index)
	}

	// The value takes the place of target, which a path selects, so a node
	// on the value or below it reads no entry that a later entry of its
	// key hides above target.
	for m, k := range moving.nodes {
		m.hidden = slices.DeleteFunc(slices.Clone(m.hidden), func(h hiddenEntry) bool { return h.depth <= k })
	}

	moving.rebase(func(paths.Path) paths.Path { return moved })

	return nil
}

// moveValue moves the text of the value of p, the place of n, to target, a
// place that does not hold a value, as [Node.Move] describes. Named says
// what the Move does, as in "moves $.a to $.b". MoveValue returns target as
// the text after the move spells it, and the index of the item of p in its
// sequence.
func (d *Doc) moveValue(n *Node, p *place, target paths.Path, named string) (paths.Path, int, error) {
	pl := d.extract(p)
	saved := d.save()

	// The value that holds target before the removal. The text that the
	// removal edits gives that place.
	holder, held := d.targetHolderID(target)

	// The target names a place of the document before the removal.
	moved := target
	if p.seq != nil {
		moved = d.shiftedTarget(target, p.seq, p.index)
	}

	// The value leaves its place first.
	err := d.removeMoved(n, p)
	if err != nil {
		return target, 0, err
	}

	if after, ok := d.targetHolderID(moved); held && ok && after.same(holder) {
		err = d.insertAt(n, moved, pl, named)
		if err != nil {
			return target, 0, err
		}

		return moved, p.index, nil
	}

	// The path to target reads an anchor that the value took, so the text
	// without the value resolves target to another place. The value goes
	// in at target while the text still holds the anchor, and then it
	// leaves its place.
	d.restore(saved)

	err = d.insertAt(n, target, pl, named)
	if err != nil {
		return target, 0, err
	}

	p, err = d.nodePlace(n)
	if err != nil || !p.exists {
		return target, 0, d.bug(n, fmt.Errorf("%s does not resolve after the insert", n.path))
	}

	err = d.removeMoved(n, p)
	if err != nil {
		return target, 0, err
	}

	return moved, p.index, nil
}

// shiftedTarget returns target, a path of the current text, as the text
// without the item at index of seq reads it. Each index of target into seq
// past that item takes the index before it, whether target reads seq
// through the path of the item or through an alias or a `<<` merge key.
func (d *Doc) shiftedTarget(target paths.Path, seq *ast.SequenceNode, index int) paths.Path {
	sels := slices.Collect(target.Selectors())

	for i, sel := range sels {
		if sel.Kind != paths.SelectorIndex || sel.Index <= index {
			continue
		}

		p, err := d.resolve(appendSelectors(paths.Doc(), sels[:i+1]))
		if err == nil && p.seq == seq {
			sels[i].Index--
		}
	}

	return appendSelectors(paths.Doc(), sels)
}

// holderID names the value that holds a place across the edits of a
// call: the origin of the key or the "-" that holds it, and the number of
// selectors of the path of the place that resolve.
type holderID struct {
	origin, found int
	// Whether the root holds the place.
	root bool
}

// same reports whether h and o name one value that holds a place.
func (h holderID) same(o holderID) bool {
	return h.root == o.root && h.origin == o.origin && h.found == o.found
}

// targetHolderID returns the [holderID] of the place at target, a place
// that the current text does not hold. It reports false when the text
// holds a value at target, or when no key or "-" holds the value that
// holds target.
func (d *Doc) targetHolderID(target paths.Path) (holderID, bool) {
	p, err := d.resolve(target)
	if err != nil || p.exists {
		return holderID{}, false
	}

	if p.container == astnode.Content(d.node.AST()) {
		return holderID{found: p.found, root: true}, true
	}

	start, ok := d.holderStart(p.container)
	if !ok || start >= len(d.origin) {
		return holderID{}, false
	}

	return holderID{origin: d.origin[start], found: p.found}, true
}

// removeMoved removes p, the place of the value that a move of n takes.
func (d *Doc) removeMoved(n *Node, p *place) error {
	err := d.removePlace(p)
	if err != nil {
		return d.bug(n, err)
	}

	return nil
}

// removePlace removes p, the place of the value that a move takes, and
// returns the problem of the parse of the text without it, which leaves
// the text as it was.
func (d *Doc) removePlace(p *place) error {
	if p.flow {
		return d.removeFlow(p)
	}

	return d.removeBlock(p)
}

// plain reports whether path holds only names and indexes.
func plain(path paths.Path) bool {
	for sel := range path.Selectors() {
		if sel.Kind != paths.SelectorChild && sel.Kind != paths.SelectorIndex {
			return false
		}
	}

	return true
}

// extract returns the value of p with its comments as a payload, with the
// origins of its bytes.
func (d *Doc) extract(p *place) payload {
	start, _ := d.startOf(p)
	keyLine := d.lineOf(start)
	keyCol := d.column(start)
	vs, ve := d.valueStart(p), d.valueEnd(p)

	var pl payload

	if n, ok := d.standIn(p); ok && n.detail == "" {
		n.moved = true
		origin := d.addNote(n)
		pl.holder = &origin
	} else if ok {
		pl.writer = n.written()
	}

	if p.flow {
		return d.flowPayload(p, pl, start, vs, ve)
	}

	reg := d.blockRegion(p)

	for l := reg.first; l < keyLine; l++ {
		pl.above = append(pl.above, d.keptLine(l, keyCol))
	}

	switch {
	case p.value == nil || isImplicitNull(p.value) || d.lineOf(vs) > keyLine:
		// The value starts below its key, or the place does not hold one.
		pl.comment = d.lineComment(d.afterProps(p), keyLine)
		pl.lines = d.keptLines(keyLine+1, reg.last, keyCol)

	case isBlockCollection(p.value) && d.lineOf(d.contentStart(p)) == keyLine:
		// A block collection of an item starts on the line of its "-", even
		// when it fits on that line.
		first := line{text: d.keptRun(vs, d.trimEnd(vs, d.lineEnd(keyLine))), indent: d.column(vs) - keyCol}
		pl.lines = append([]line{first}, d.keptLines(keyLine+1, reg.last, keyCol)...)

	case d.lineOf(max(vs, ve-1)) == keyLine:
		// The comment lines after the line that the user indented past the
		// key or the "-" belong to the entry or the item, so they move with
		// it, as a removal takes them.
		r := d.keptRun(vs, ve)
		pl.inline = &r
		pl.comment = d.lineComment(ve, keyLine)
		pl.lines = d.keptLines(keyLine+1, reg.last, keyCol)

	default:
		// The value starts on the line of its key and goes on below it. A
		// block scalar does so after its indicator, and a block collection
		// after its anchor or its tag. The rest of the line, with its
		// comment, stays on the line of the key.
		r := d.keptRun(vs, d.trimEnd(vs, d.lineEnd(keyLine)))
		pl.inline = &r
		pl.lines = d.keptLines(keyLine+1, reg.last, keyCol)
		pl.collection = isBlockCollection(p.value)
	}

	pl.mapping = isMapping(p.value)
	pl.scalar = endScalar(p.value) != nil
	pl.keeps = d.keepsBlanks(p.value, reg.last)
	pl.tail = d.scalarTail(p.value, reg.last)

	if ve <= vs {
		pl.flow = d.emptyFlow(p, start, pl.holder)

		return pl
	}

	// The bytes that the flow text adds take the origin of the first byte
	// of the value.
	pl.flow = d.flowRun(p.value, originOf(d.keptRun(vs, ve)))

	// An item of a flow sequence has no "-". The bracket that opens a
	// collection there, past its anchors and its tags, thus stands for the
	// key or the "-" that held the value, as the "-" of a block item does.
	switch {
	case pl.holder == nil:
	case isCollection(p.value):
		pl.flow.origins[d.propsWidth(p.value)] = *pl.holder
	default:
		pl.flow = d.heldRun(pl.flow, p.value, *pl.holder)
	}

	return pl
}

// propsWidth returns the number of bytes of the anchors and the tags of
// node, a node of the current text, and of the space after each, as
// [Doc.flowRun] writes them before the content of node.
func (d *Doc) propsWidth(node ast.Node) int {
	width := 0

	for isProps(node) {
		if s, ok := d.propsSpan(node); ok {
			width += s.end - s.start + 1
		}

		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		}
	}

	return width
}

// emptyFlow returns the flow text of the empty value of p, whose key or
// "-" starts at start: a null. The value has no byte of its own, so the
// null takes the origin of the key or the "-". With holder set, its first
// byte stands for the key or the "-" that held the value, as the bracket
// that opens a collection does in a flow sequence.
func (d *Doc) emptyFlow(p *place, start int, holder *int) run {
	origin := -1
	if start < len(d.origin) {
		origin = d.origin[start]
	}

	r := d.flowRun(p.value, origin)

	if holder != nil {
		r.origins[0] = *holder
	}

	return r
}

// flowPayload returns the value of p, an entry or an item of a flow
// collection, with its comment as a payload. The text of p starts at
// start, and the value is the bytes [vs, ve). The holder of pl is the
// origin of the key or the "-" that a move writes for the value, or nil.
func (d *Doc) flowPayload(p *place, pl payload, start, vs, ve int) payload {
	r := d.keptRun(vs, ve)

	if pl.holder != nil {
		r = d.heldRun(r, p.value, *pl.holder)
	}

	pl.inline, pl.flow = &r, r

	// An empty value has no text, so nothing follows its key or its "-"
	// at a block place. Its flow text is null, as for an empty value of
	// a block collection.
	if ve <= vs {
		pl.inline, pl.flow = nil, d.emptyFlow(p, start, pl.holder)
	}

	// The comment that the removal of the entry or the item takes goes
	// with the value, as the comment on the line of a block entry does.
	// The flow text drops it.
	pl.comment = d.flowComment(p, start, ve)

	return pl
}

// heldRun returns r, the flow text of value, with a first byte whose note
// also holds holder, the origin of the key or the "-" that held the value.
// An item of a flow sequence has neither, so a later move out of the
// sequence writes a key or a "-" that stands for holder again. A first
// byte of the user's text keeps its origin, because the user's text gives
// the key or the "-" that holds it, as [Doc.userHolder] finds it. An alias
// keeps its origin too, and so does a value with an anchor or a tag. The
// checks of anchors know each alias and each anchor by that origin.
func (d *Doc) heldRun(r run, value ast.Node, holder int) run {
	if len(r.origins) == 0 || r.origins[0] >= 0 || isProps(value) {
		return r
	}

	if _, ok := value.(*ast.AliasNode); ok {
		return r
	}

	n := d.notes[-1-r.origins[0]]
	n.held = holder

	return startingWith(r, d.addNote(n))
}

// standIn returns the note of the key or the "-" that a move writes for
// the value of p at its target. That key or "-" stands for the key or the
// "-" of p, as a renamed key stands for the old key. An error there thus
// binds where the user wrote the key or the "-" of p. A note that names an
// edit stands for a key or a "-" that the edit wrote. The move writes its
// own key for it, and that note gives the writer of the key.
//
// An item of a flow sequence has neither. Its first byte can stand for
// the key or the "-" that held it before an earlier move, as the bracket
// of a collection does. The note of the token of the item, which an edit
// wrote, can hold that key or "-" too, as [Doc.itemHolder] reads it.
// Otherwise, the key or "-" stands for the key or the "-" that holds the
// text of the item in the user's text, as [Doc.userHolder] finds it. A
// value that the user wrote as an item of a flow sequence has neither
// there, so the key or "-" stands for the item. For an item that an edit
// wrote, the note of the token of the item gives the writer.
//
// It reports false for the root.
func (d *Doc) standIn(p *place) (note, bool) {
	start, ok := d.startOf(p)
	if !ok || start >= len(d.origin) || p.root {
		return note{}, false
	}

	var n note

	switch {
	case p.entry != nil || p.seq != nil && !p.seq.IsFlowStyle:
		// The key stands for the key, after the "?" of an explicit key.
		from, end := start, start+1
		if ks, _, ok := d.keySpan(p); ok && ks.end > ks.start {
			from, end = ks.start, ks.end
		}

		n = d.keptNote(from, end)

	case d.itemHolder(p) < 0:
		n = d.notes[-1-d.itemHolder(p)]

	case d.standsFor(start):
		n = d.bareNote(d.notes[-1-d.origin[start]])

	default:
		n, ok = d.userHolder(start)
		if !ok {
			n, ok = d.writtenToken(p)
		}
	}

	return n, ok
}

// writtenToken returns the note of the token of the value of p, as
// [Doc.tokenOf] finds it, when that note names the edit that wrote the
// token. It reports false for any other token.
func (d *Doc) writtenToken(p *place) (note, bool) {
	off, ok := d.tokenOf(p)
	if !ok || d.originAt(off) >= 0 || d.originAt(off) == math.MinInt {
		return note{}, false
	}

	n := d.notes[-1-d.origin[off]]

	return n, n.detail != ""
}

// itemHolder returns the origin of the key or the "-" that the token of the
// value of p holds, or 0 for none. The place p is an item of a flow
// sequence, and [Doc.heldRun] and a Set over the item write that origin.
// The token lies under the tags of the value, as [Doc.tokenOf] finds it.
func (d *Doc) itemHolder(p *place) int {
	off, ok := d.tokenOf(p)
	if !ok || d.originAt(off) >= 0 || d.originAt(off) == math.MinInt {
		return 0
	}

	return d.notes[-1-d.origin[off]].held
}

// userHolder returns the note of the text of the user that holds the
// value whose first byte is the byte at off of the current text. That
// text is the key of the entry of the value in the user's text, or the
// "-" of its item in a block sequence. An item of a flow sequence has
// neither, so for such an item, the note names the item. It reports false
// when the byte does not come from the user's text, and when the user's
// text gives no path there.
func (d *Doc) userHolder(off int) (note, bool) {
	if off >= len(d.origin) || d.origin[off] < 0 {
		return note{}, false
	}

	pos := d.userPos(off)

	path, ok := d.user.PathAt(pos)
	if !ok {
		return note{}, false
	}

	path = d.bareItem(path)

	last, ok := path.Last()
	if !ok || last.Kind == paths.SelectorKey {
		return note{}, false
	}

	n := note{path: path, hasPath: true, located: true}

	if last.Kind == paths.SelectorIndex {
		if dash, ok := d.userDash(path); ok {
			n.at = position.NewRange(dash, position.New(dash.Line, dash.Col+1))

			return n, true
		}

		n.flowItem = true
	} else {
		n.path = path.Key()
	}

	rs, err := d.user.Ranges(n.path)
	if err != nil || len(rs) == 0 {
		return note{}, false
	}

	n.at = rs[0]

	// The range of a value starts past its anchor and its tag. The item of
	// a flow sequence starts at its first byte, which holds them.
	if last.Kind == paths.SelectorIndex && comparePositions(pos, n.at.Start) < 0 {
		n.at = position.NewRange(pos, n.at.End)
	}

	return n, true
}

// lineComment returns the comment that ends line past off, or nil.
func (d *Doc) lineComment(off, line int) *run {
	c := d.trailingComment(off)
	if c < 0 || d.lineOf(c) != line {
		return nil
	}

	r := d.keptRun(c, d.lineEnd(line))

	return &r
}

// flowComment returns the comment that a removal of p, an entry or an item
// of a flow collection, takes with it, or nil for none. The text of p is
// the bytes [start, end).
func (d *Doc) flowComment(p *place, start, end int) *run {
	_, c := d.flowCuts(p, start, end)
	if c < 0 {
		return nil
	}

	r := d.keptRun(c, d.lineEnd(d.lineOf(c)))

	return &r
}

// trimEnd returns end, the end of a run that starts at start, before the
// spaces and tabs that end the run.
func (d *Doc) trimEnd(start, end int) int {
	for end > start && (d.cur[end-1] == ' ' || d.cur[end-1] == '\t') {
		end--
	}

	return end
}

// keptLines returns the lines from first through last as lines of a
// payload, with their indentation relative to col.
func (d *Doc) keptLines(first, last, col int) []line {
	var out []line

	for l := first; l <= last; l++ {
		out = append(out, d.keptLine(l, col))
	}

	return out
}

// keptLine returns line l as a line of a payload, with its indentation
// relative to col. A block scalar reads the spaces of a line of spaces
// alone past its indentation. So such a line deeper than col keeps its last
// space as its text, and the spaces before it as its indentation.
func (d *Doc) keptLine(l, col int) line {
	indent := d.indentOf(l)
	start, end := d.lineStart(l)+indent, d.lineEnd(l)

	if start == end && indent > col {
		return line{text: d.keptRun(end-1, end), indent: indent - col - 1}
	}

	return line{text: d.keptRun(start, end), indent: indent - col}
}

// bareItem returns path, a path of the user's text that PathAt gives, as
// the path of an item of a flow sequence. It does so when path is the key
// of the first entry of that item. A mapping that the user wrote without
// braces in a flow sequence, as in "[a: 1]", starts at that key. It
// returns any other path as it is.
func (d *Doc) bareItem(path paths.Path) paths.Path {
	return bareItemIn(d.user, path)
}

// bareNote returns n, the note of the first byte of an item of a flow
// sequence, as the note of that item. It does so when n names the key of
// the first entry of a mapping that the user wrote without braces there.
// Such an item starts at that key, as in "[a: 1]", so the note of a
// renamed key there stands for the item too. It returns any other note as
// it is.
func (d *Doc) bareNote(n note) note {
	if !n.hasPath {
		return n
	}

	item := d.bareItem(n.path)
	if item.Equal(n.path) {
		return n
	}

	n.path, n.flowItem = item, true

	rs, err := d.user.Ranges(item)
	if err == nil && len(rs) > 0 {
		n.at, n.located = rs[0], true
	}

	return n
}

// bareItemIn returns path, a path of doc that PathAt gives, as the path of
// an item of a flow sequence, as [Doc.bareItem] does for the user's text.
func bareItemIn(doc *niceyaml.Node, path paths.Path) paths.Path {
	if last, ok := path.Last(); !ok || last.Kind != paths.SelectorKey {
		return path
	}

	entry, _ := path.Parent()

	item, ok := entry.Parent()
	if last, isItem := item.Last(); !ok || !isItem || last.Kind != paths.SelectorIndex {
		return path
	}

	r := doc.Resolver()

	node, err := r.Node(item)
	if err != nil {
		return path
	}

	// A block mapping is a mapping without braces too, so the item must lie
	// in a flow sequence.
	seqPath, _ := item.Parent()

	seq, err := r.Node(seqPath)
	if err != nil || !isFlow(astnode.Content(seq)) {
		return path
	}

	entries, ok := astnode.Entries(node)
	if !isBareMapping(node) || !ok || len(entries) == 0 {
		return path
	}

	if name, ok := r.KeyName(entries[0].Key); !ok || entry.String() != item.Child(name).String() {
		return path
	}

	return item
}

// isBareMapping reports whether node is a mapping of a flow collection
// without braces, such as the item of "[a: 1]".
func isBareMapping(node ast.Node) bool {
	m, ok := node.(*ast.MappingNode)

	return ok && !m.IsFlowStyle
}

// isMapping reports whether node holds a mapping under its anchors and
// tags, as a decode reads it. A tag that converts a collection holds a
// scalar, as [astnode.Converted] reads such a tag.
func isMapping(node ast.Node) bool {
	_, ok := readEntries(astnode.Converted(node))

	return ok
}

// moveHolder is the value that holds the target of a move: the deepest
// value on the path of the target that the document holds.
type moveHolder struct {
	// The content of the value as a decode reads it, as [Doc.readValue]
	// gives it.
	content ast.Node
	// The place of the value.
	place *place
	// The selectors of the target below the value.
	rest []paths.Selector
	// The path of the value.
	path paths.Path
}

// targetHolder returns the value that holds target, a place that does not
// hold a value, whose place is tp. It also returns the reason that a move
// cannot put a value at target, or "" when a move can. The reason reads as
// the words that a message puts after "but", such as "f holds a sequence".
// It reports false when the value does not resolve.
func (d *Doc) targetHolder(target paths.Path, tp *place) (moveHolder, string, bool) {
	sels := slices.Collect(target.Selectors())
	h := moveHolder{path: appendSelectors(paths.Doc(), sels[:tp.found]), rest: sels[tp.found:]}

	holder, err := d.resolve(h.path)
	if err != nil || !holder.exists {
		return h, "", false
	}

	h.place, h.content = holder, tp.container

	// The move creates the value as Set does.
	return h, d.createProblem(holder, h.path, h.content, h.rest), true
}

// insertAt inserts pl at target, a place that does not hold a value, and
// creates the mappings above it that the document lacks. Named says what
// the Move does, for the note of the text that it creates. The caller
// checks with [Doc.targetHolder] that target can take a value before it
// changes the text.
func (d *Doc) insertAt(h *Node, target paths.Path, pl payload, named string) error {
	tp, err := d.resolve(target)
	if err != nil {
		return d.bug(h, fmt.Errorf("%s %w", target, err))
	}

	// The checks of the move found no value at target.
	if tp.exists {
		return d.bug(h, fmt.Errorf("%s holds a value", target))
	}

	th, why, ok := d.targetHolder(target, tp)

	switch {
	case !ok:
		return d.bug(h, fmt.Errorf("%s does not resolve", th.path))
	case why != "":
		return d.bug(h, fmt.Errorf("%s: %s", target, why))
	}

	origin := d.addNote(d.createdNote(th.place, named))

	if seq, ok := th.content.(*ast.SequenceNode); ok {
		return d.appendPayload(h, th.path, th.place, seq, pl, origin)
	}

	var keys []run

	for _, sel := range th.rest {
		keys = append(
			keys,
			newRun(renderKey(context.Background(), sel.Name, nil, th.place.flow || isFlow(th.content)), origin),
		)
	}

	// The key that holds the value stands for the key or the "-" that held
	// it. Where an edit wrote that key or "-", the key names that edit as
	// its writer, and binds where the text that the move creates binds.
	switch last := len(keys) - 1; {
	case pl.holder != nil:
		keys[last] = newRun(keys[last].text, *pl.holder)
	case pl.writer != nil:
		n := d.createdNote(th.place, named)
		n.detail, n.call = pl.writer.detail, pl.writer.call
		keys[last] = newRun(keys[last].text, d.addNote(n))
	}

	step := d.stepOf(th.place, th.path)

	for i := len(keys) - 1; i > 0; i-- {
		pl = nest(keys[i], pl, step, origin)
	}

	entries, _ := readEntries(th.content)

	return d.insertPayload(h, th.path, th.place, th.content, entries, keys[0], pl, origin)
}

// nest returns pl as the value of the entry key of a new block mapping,
// whose lines indent by step.
func nest(key run, pl payload, step, origin int) payload {
	var out payload

	out.mapping = true
	out.scalar, out.keeps, out.tail = pl.scalar, pl.keeps, pl.tail
	out.flow = appendRun(appendRun(appendRun(newRun("{", origin), key), newRun(": ", origin)),
		appendRun(pl.flow, newRun("}", origin)))

	for _, l := range pl.above {
		l.indent += step
		out.lines = append(out.lines, l)
	}

	head := appendRun(key, newRun(":", origin))

	if pl.inline != nil {
		head = appendRun(appendRun(head, newRun(" ", origin)), *pl.inline)
	}

	if pl.comment != nil {
		head = appendRun(appendRun(head, newRun(" ", origin)), *pl.comment)
	}

	out.lines = append(out.lines, line{text: head, indent: step})

	for _, l := range pl.lines {
		l.indent += step
		out.lines = append(out.lines, l)
	}

	return out
}
