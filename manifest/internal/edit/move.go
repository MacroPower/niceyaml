package edit

import (
	"context"
	"fmt"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Move moves the value of h, with its comments, to target, a `$` path,
// which it creates as [Handle.Set] does. The value keeps its text,
// indented for its new place.
func (h *Handle) Move(target paths.Path) error {
	d := h.d

	p, err := h.place()
	if err != nil || !p.exists {
		return err
	}

	name := h.Key()
	if name == "" {
		name = h.path.String()
	}

	switch {
	case !target.IsAbsolute() || !plain(target) || target.IsRoot():
		return d.fail(h, "%s moves %q to %s, which is no $ path of names and indexes", d.label, name, target)
	case p.root:
		return d.fail(h, "%s moves the root of the document", d.label)
	case d.holdsTypeField(h.path):
		return d.fail(h, "%s moves %q, which holds a type field", d.label, name)
	case d.holdsTypeField(target):
		return d.fail(h, "%s moves %q to %s, a type field", d.label, name, target)
	case p.shared:
		return d.fail(h, "%s moves %q to %s, but an alias or a merge key also reads it", d.label, name, target)
	}

	if _, inside := target.CutPrefix(h.path); inside {
		return d.fail(h, "%s moves %q to %s, which lies inside it", d.label, name, target)
	}

	tp, err := d.resolve(target)
	if err != nil {
		return d.fail(h, "%s moves %q to %s, which %s", d.label, name, target, err.Error())
	}

	if tp.exists {
		return d.fail(h, "%s moves %q to %s, which already exists", d.label, name, target)
	}

	pl := d.extract(h, p)
	anchors := d.anchorsIn(p.value)

	d.recordEdit(h, p, fmt.Sprintf("moves %q to %s", name, target))

	d.invalidate(h.path)
	d.invalidate(target)

	if p.flow {
		err = d.removeFlow(p)
	} else {
		err = d.removeBlock(h, p)
	}

	if err != nil {
		return d.bug(h, err)
	}

	from := h.path

	// The handles to move come first, because the shift below moves the
	// handle of the next item to the place the value left.
	moving := d.handlesAt(from)

	if p.seq != nil {
		parent, _ := from.Parent()
		d.shifted(parent, p.index)

		// The target names a place of the document before the removal.
		target = shiftedPath(target, parent, p.index)
	}

	err = d.insertAt(h, target, pl)
	if err != nil {
		return err
	}

	repoint(moving, from, target)

	return d.keepAnchors(h, anchors)
}

// plain reports whether path holds names and indexes alone.
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
func (d *Doc) extract(h *Handle, p *place) payload {
	start, _ := d.startOf(p)
	keyLine := d.lineOf(start)
	keyCol := d.column(start)
	vs, ve := d.valueStart(p), d.valueEnd(p)

	var pl payload

	if p.flow {
		r := d.keptRun(vs, ve)
		pl.inline, pl.flow = &r, r

		return pl
	}

	reg := d.blockRegion(p)

	for l := reg.first; l < keyLine; l++ {
		pl.above = append(pl.above, d.keptLine(l, keyCol))
	}

	from := d.afterProps(p)
	if d.lineOf(max(vs, ve-1)) == keyLine {
		from = max(from, ve)
	}

	if c := d.trailingComment(from); c >= 0 && d.lineOf(c) == keyLine {
		r := d.keptRun(c, d.lineEnd(keyLine))
		pl.comment = &r
	}

	switch {
	case p.value == nil || isImplicitNull(p.value):

	case d.lineOf(vs) == keyLine && d.lineOf(max(vs, ve-1)) == keyLine:
		r := d.keptRun(vs, ve)
		pl.inline = &r

	case d.lineOf(vs) == keyLine:
		// A block collection of an item starts on the line of its "-".
		first := d.keptRun(vs, d.lineEnd(keyLine))
		if pl.comment != nil {
			first = d.keptRun(vs, d.trailingComment(vs))
		}

		pl.lines = append(pl.lines, line{text: first, indent: d.column(vs) - keyCol})

		for l := keyLine + 1; l <= reg.last; l++ {
			pl.lines = append(pl.lines, d.keptLine(l, keyCol))
		}

	default:
		colonEnd := d.valueEnd(&place{entry: p.entry, seq: p.seq, index: p.index})
		if props := d.afterProps(p); props > colonEnd {
			if s, ok := d.propsStart(p); ok {
				r := d.keptRun(s, props)
				pl.inline = &r
			}
		}

		for l := keyLine + 1; l <= reg.last; l++ {
			pl.lines = append(pl.lines, d.keptLine(l, keyCol))
		}
	}

	pl.mapping = isMapping(p.value)
	pl.flow = d.flowOf(h, p)

	return pl
}

// keptLine returns line l as a line of a payload, with its indentation
// relative to col.
func (d *Doc) keptLine(l, col int) line {
	indent := d.indentOf(l)
	start := d.lineStart(l) + indent

	return line{text: d.keptRun(start, d.lineEnd(l)), indent: indent - col}
}

// propsStart returns the offset of the first anchor or tag of the value of
// p on the line of its key.
func (d *Doc) propsStart(p *place) (int, bool) {
	switch p.value.(type) {
	case *ast.AnchorNode, *ast.TagNode:
		s, ok := d.propsSpan(p.value)

		return s.start, ok
	}

	return 0, false
}

// isMapping reports whether node holds a mapping under its anchors and
// tags.
func isMapping(node ast.Node) bool {
	_, ok := astnode.Entries(astnode.Content(node))

	return ok && node != nil && !isNull(node)
}

// flowOf returns the value of p in flow style, for a flow collection that
// a move puts it in.
func (d *Doc) flowOf(h *Handle, p *place) run {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	origin := d.addNote(d.keptNote(vs, ve))

	n, err := d.node.At(p.path)
	if err != nil {
		return newRun("null", origin)
	}

	v, err := n.Decode[any](context.Background())
	if err != nil {
		return newRun("null", origin)
	}

	r, err := render(context.Background(), v, d.stepOf(h.path), true)
	if err != nil {
		return newRun("null", origin)
	}

	return newRun(r.flow, origin)
}

// insertAt inserts pl at target, a place that holds no value, and creates
// the mappings above it that the document lacks.
func (d *Doc) insertAt(h *Handle, target paths.Path, pl payload) error {
	tp, err := d.resolve(target)
	if err != nil {
		return d.fail(h, "%s moves %q to %s, which %s", d.label, h.Key(), target, err.Error())
	}

	var sels []paths.Selector

	for sel := range target.Selectors() {
		sels = append(sels, sel)
	}

	holderPath := appendSelectors(paths.Doc(), sels[:tp.found])

	holder, err := d.resolve(holderPath)
	if err != nil || !holder.exists {
		return d.fail(h, "%s cannot find %s", d.label, holderPath)
	}

	content := astnode.Content(holder.value)
	origin := d.addNote(d.createdNote(holder))
	holderName := d.describe(holderPath)

	// The index past the last item of a sequence appends the value there,
	// as Set does.
	if next := sels[tp.found]; next.Kind == paths.SelectorIndex {
		seq, ok := content.(*ast.SequenceNode)
		if !ok || next.Index != len(seq.Values) || tp.found != len(sels)-1 {
			return d.fail(h, "%s moves %q to %s, but %s holds no such item", d.label, h.Key(), target, holderName)
		}

		return d.appendPayload(h, holderPath, holder, seq, pl, origin)
	}

	var keys []run

	for _, sel := range sels[tp.found:] {
		if sel.Kind != paths.SelectorChild {
			return d.fail(h, "%s moves %q to %s, but %s holds no such item", d.label, h.Key(), target, holderName)
		}

		key, err := renderKey(context.Background(), sel.Name, nil, holder.flow || isFlow(content))
		if err != nil {
			return d.fail(h, "%s cannot write the key %q: %v", d.label, sel.Name, err)
		}

		keys = append(keys, newRun(key, origin))
	}

	entries, ok := astnode.Entries(content)
	if !ok {
		return d.fail(h, "%s moves %q to %s, but %s holds %s", d.label, h.Key(), target, holderName,
			kindOf(content))
	}

	step := d.stepOf(holderPath)

	for i := len(keys) - 1; i > 0; i-- {
		pl = nest(keys[i], pl, step, origin)
	}

	return d.insertPayload(h, holderPath, holder, content, entries, keys[0], pl, origin)
}

// nest returns pl as the value of the entry key of a new block mapping,
// whose lines indent by step.
func nest(key run, pl payload, step, origin int) payload {
	var out payload

	out.mapping = true
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
