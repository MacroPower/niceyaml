package edit

import (
	"context"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// run is text with the origin of each of its bytes.
type run struct {
	text    string
	origins []int
}

// newRun returns text whose bytes all have origin.
func newRun(text string, origin int) run {
	return run{text: text, origins: written(text, origin)}
}

// keptRun returns the bytes [start, end) of the current text with their
// origins.
func (d *Doc) keptRun(start, end int) run {
	return run{text: d.cur[start:end], origins: append([]int(nil), d.origin[start:end]...)}
}

// line is one line of a payload, with its indentation relative to the key
// or the "-" of the place it goes to.
type line struct {
	text   run
	indent int
}

// payload is a value with its comments, as an edit writes it at a place.
type payload struct {
	// The text of the value in flow style, for a flow collection.
	flow run
	// The value on the line of its key or its "-", or nil for a block
	// collection.
	inline *run
	// The comment at the end of that line, or nil.
	comment *run
	// The comment lines above the entry or the item.
	above []line
	// The lines of a block collection, or of a block scalar after its
	// indicator.
	lines []line
	// The value is a mapping, so an item writes its first line after the
	// "-".
	mapping bool
}

// Set replaces the value with v, or creates it and the mappings above it.
// It writes v as [go.jacobcolvin.com/niceyaml/encoder.Marshal] does, keeps
// the comments of the entry, and drops the tag of the old value and the
// comments inside it. A Go string keeps the quotes of the scalar that it
// replaces. Set at the index past the last item of a sequence appends an
// item.
func (n *Node) Set(ctx context.Context, v any) error {
	return n.set(ctx, v, false)
}

// set is [Node.Set], which sets a protected value when protected is set.
func (n *Node) set(ctx context.Context, v any, protected bool) error {
	d := n.d

	p, err := n.place()
	if err != nil {
		return err
	}

	why := d.protection(n.path)

	switch {
	case p.root:
		return d.fail(n, "%s replaces the root of the document", d.subject())
	case !protected && why != "":
		return d.fail(n, "%s sets %s, which %s", d.subject(), n.path, why)
	}

	if p.exists {
		err = d.editOnce(n, p)
		if err != nil {
			return err
		}

		d.recordEdit(n, p, "sets "+n.path.String())

		err = d.replaceValue(ctx, n, p, v)
	} else {
		err = d.createValue(ctx, n, p, v)
	}

	if err != nil {
		return err
	}

	if !p.exists {
		created, err := d.resolve(n.path)
		if err == nil && created.exists {
			d.recordEdit(n, created, "sets "+n.path.String())
		}
	}

	d.invalidate(n.path)

	d.written = append(d.written, writtenValue{path: n.path, value: v})
	d.took(n.path, false)

	return nil
}

// renderPayload returns v as a payload for a place whose block collections
// indent by step.
func (d *Doc) renderPayload(ctx context.Context, h *Node, v any, step, origin int) (payload, error) {
	seqIndent := d.sequenceIndent()

	r, err := render(ctx, v, step, seqIndent)
	if err != nil {
		return payload{}, d.fail(h, "%s cannot write the value of %s: %v", d.subject(), h.path, err)
	}

	pl := payload{flow: newRun(r.flow, origin), mapping: r.mapping}

	switch {
	case r.block == nil:
		inline := newRun(r.flow, origin)
		pl.inline = &inline

	case !r.collection:
		inline := newRun(r.block[0], origin)
		pl.inline = &inline

		for _, l := range r.block[1:] {
			pl.lines = append(pl.lines, renderedLine(l, 0, origin))
		}

	default:
		shift := step
		if !r.mapping && !seqIndent {
			shift = 0
		}

		for _, l := range r.block {
			pl.lines = append(pl.lines, renderedLine(l, shift, origin))
		}
	}

	return pl, nil
}

// renderedLine returns text, one line of the encoder, as a line of a
// payload indented by shift past its own indentation.
func renderedLine(text string, shift, origin int) line {
	trimmed := strings.TrimLeft(text, " ")

	return line{text: newRun(trimmed, origin), indent: shift + len(text) - len(trimmed)}
}

// replaceValue replaces the value of p with v.
func (d *Doc) replaceValue(ctx context.Context, h *Node, p *place, v any) error {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	origin := d.addNote(d.setNote(vs, ve, true))
	keyStart, _ := d.startOf(p)
	keyCol := d.column(keyStart)
	step := d.stepOf(h.path)

	pl, err := d.renderPayload(ctx, h, v, step, origin)
	if err != nil {
		return err
	}

	if s, ok := v.(string); ok && pl.inline != nil && pl.lines == nil {
		if text, ok := scalarStyle(s, scalarToken(p.value)); ok {
			inline := newRun(text, origin)
			pl.inline = &inline
		}
	}

	anchors := d.anchorsIn(p.value)
	oldBlock := !p.flow && p.value != nil && isBlockCollection(p.value)

	// A flow collection holds flow style, and so does a collection that
	// replaces a scalar or a flow collection.
	if p.flow || !oldBlock && pl.inline == nil {
		pl.inline, pl.lines = &pl.flow, nil
	}

	keyLine := d.lineOf(keyStart)
	brk := d.breakBefore(keyLine + 1)

	var edits []edit

	switch {
	case !oldBlock || d.lineOf(vs) == keyLine:
		var text run

		if pl.inline == nil {
			// A block collection of an item starts on the line of its "-".
			col := d.column(vs) - pl.lines[0].indent
			text = appendRun(pl.lines[0].text, prefixRun(brk, d.joinLines(pl.lines[1:], col, brk), origin))
		} else {
			text = d.joinValue(pl, keyCol, brk)
		}

		if p.value == nil || isImplicitNull(p.value) {
			text = prefixRun(" ", text, origin)
		}

		edits = append(edits, edit{start: vs, end: ve, text: text.text, origins: text.origins})

	default:
		// The old value starts below its key. Its lines go, and the new
		// value takes the line of the key or the lines below it. The anchor
		// and the tag of the old value go too.
		at := d.valueEnd(&place{entry: p.entry, seq: p.seq, index: p.index})
		lineEnd := d.lineEnd(keyLine)

		cut := lineEnd
		if c := d.trailingComment(d.afterProps(p)); c >= 0 {
			cut = c
		}

		var head, tail run

		if cut < lineEnd {
			head = newRun(" ", origin)
		}

		if pl.inline != nil {
			head = prefixRun(" ", appendRun(*pl.inline, head), origin)

			if len(pl.lines) > 0 {
				tail = prefixRun(brk, d.joinLines(pl.lines, keyCol, brk), origin)
			}
		} else {
			col := d.column(vs)
			if pl.mapping && col <= keyCol {
				col = keyCol + step
			}

			tail = prefixRun(brk, d.joinLines(pl.lines, col-pl.lines[0].indent, brk), origin)
		}

		edits = append(edits,
			edit{start: at, end: cut, text: head.text, origins: head.origins},
			edit{start: lineEnd, end: d.lineEnd(d.blockRegion(p).last), text: tail.text, origins: tail.origins},
		)
	}

	err = d.apply(edits...)
	if err != nil {
		return d.bug(h, err)
	}

	return d.keepAnchors(h, anchors)
}

// setNote returns the note of the text that Set writes over the bytes
// [start, end) of the current text.
func (d *Doc) setNote(start, end int, replaced bool) note {
	n := d.keptNote(start, max(start+1, end))
	n.detail = d.subject()
	n.replaced = replaced

	return n
}

// joinValue returns the text of pl as the value of a place whose value
// starts at column col, on the line of its key or its "-".
func (d *Doc) joinValue(pl payload, col int, brk string) run {
	var out run

	if pl.inline != nil {
		out = appendRun(out, *pl.inline)
	}

	if len(pl.lines) > 0 {
		lines := d.joinLines(pl.lines, col, brk)
		if pl.inline != nil {
			lines = prefixRun(brk, lines, -1)
		}

		out = appendRun(out, lines)
	}

	return out
}

// joinLines returns lines, each indented by col past its own indentation,
// joined with brk.
func (d *Doc) joinLines(lines []line, col int, brk string) run {
	var out run

	for i, l := range lines {
		if i > 0 {
			out = appendRun(out, newRun(brk, originOf(l.text)))
		}

		if l.text.text != "" {
			out = appendRun(out, newRun(strings.Repeat(" ", max(0, col+l.indent)), originOf(l.text)))
		}

		out = appendRun(out, l.text)
	}

	return out
}

// originOf returns the origin that the text around r takes: the origin of
// its first byte.
func originOf(r run) int {
	if len(r.origins) == 0 {
		return -1
	}

	return r.origins[0]
}

// appendRun returns a with b after it.
func appendRun(a, b run) run {
	return run{text: a.text + b.text, origins: append(append([]int(nil), a.origins...), b.origins...)}
}

// prefixRun returns r after text, whose bytes take origin, or the origin
// of r for a negative origin.
func prefixRun(text string, r run, origin int) run {
	if origin == -1 {
		origin = originOf(r)
	}

	return appendRun(newRun(text, origin), r)
}

// createValue creates the value of p, and the mappings above it that the
// document lacks.
func (d *Doc) createValue(ctx context.Context, h *Node, p *place, v any) error {
	var sels []paths.Selector

	for sel := range h.path.Selectors() {
		sels = append(sels, sel)
	}

	holderPath := appendSelectors(paths.Doc(), sels[:p.found])

	holder, err := d.resolve(holderPath)
	if err != nil || !holder.exists {
		return d.fail(h, "%s cannot find %s", d.subject(), holderPath)
	}

	content := astnode.Content(holder.value)
	if _, ok := content.(*ast.AliasNode); ok {
		target, err := d.node.Resolver().Deref(content)
		if err != nil {
			return d.fail(h, "%s sets %s, which goes through an alias that does not resolve", d.subject(), h.path)
		}

		content = astnode.Content(target)
	}

	next := sels[p.found]
	holderName := d.describe(holderPath)

	value := v

	for i := len(sels) - 1; i > p.found; i-- {
		if sels[i].Kind != paths.SelectorChild {
			return d.fail(h, "%s sets %s, but %s holds no item %d", d.subject(), h.path, holderName, sels[i].Index)
		}

		value = map[string]any{sels[i].Name: value}
	}

	origin := d.addNote(d.createdNote(holder))

	switch next.Kind {
	case paths.SelectorChild:
		entries, ok := astnode.Entries(content)
		if !ok {
			return d.fail(h, "%s sets %s, but %s holds %s", d.subject(), h.path, holderName, kindOf(content))
		}

		return d.insertEntry(ctx, h, holderPath, holder, content, entries, next.Name, value, origin)

	default:
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return d.fail(h, "%s sets %s, but %s holds %s", d.subject(), h.path, holderName, kindOf(content))
		}

		if next.Index != len(seq.Values) || p.found != len(sels)-1 {
			return d.fail(h, "%s sets %s, but %s holds %d items", d.subject(), h.path, holderName, len(seq.Values))
		}

		return d.appendItem(ctx, h, holderPath, holder, seq, value, origin)
	}
}

// describe returns the name of the value at path as a message gives it:
// the key of its entry, or "the document" for the root.
func (d *Doc) describe(path paths.Path) string {
	last, ok := path.Last()

	switch {
	case !ok:
		return "the document"
	case last.Kind == paths.SelectorChild:
		return last.Name
	default:
		return path.String()
	}
}

// kindOf returns what node holds, as a message names it.
func kindOf(node ast.Node) string {
	switch {
	case isNull(node):
		return "null"
	case astnode.IsScalar(node):
		return "a scalar"
	default:
		if _, ok := node.(*ast.SequenceNode); ok {
			return "a sequence"
		}

		return "a mapping"
	}
}

// createdNote returns the note of text that Set creates below holder. The
// note binds at the key of the nearest mapping above that the user wrote.
func (d *Doc) createdNote(holder *place) note {
	for p := holder; p != nil; {
		if start, ok := d.startOf(p); ok && start < len(d.origin) && d.origin[start] >= 0 {
			n := d.keptNote(start, d.valueEnd(p))
			n.detail = d.subject()

			return n
		}

		parent, ok := p.path.Parent()
		if !ok {
			break
		}

		next, err := d.resolve(parent)
		if err != nil {
			break
		}

		p = next
	}

	return note{detail: d.subject()}
}

// insertEntry inserts the entry key with value into the mapping content at
// holderPath, after its last entry.
func (d *Doc) insertEntry(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key string,
	value any,
	origin int,
) error {
	flow := holder.flow || isFlow(content)

	keyText, err := renderKey(ctx, key, nil, flow)
	if err != nil {
		return d.fail(h, "%s cannot write the key %q: %v", d.subject(), key, err)
	}

	step := d.stepOf(holderPath)

	pl, err := d.renderPayload(ctx, h, value, step, origin)
	if err != nil {
		return err
	}

	return d.insertPayload(h, holderPath, holder, content, entries, newRun(keyText, origin), pl, origin)
}

// insertPayload inserts the entry key with the payload pl into the mapping
// content at holderPath, after its last entry.
func (d *Doc) insertPayload(
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key run,
	pl payload,
	origin int,
) error {
	var e edit

	if isFlow(content) {
		block, ok := d.blockOpening(holder, content)
		if !ok {
			text := appendRun(appendRun(key, newRun(": ", origin)), pl.flow)
			e = d.flowInsert(content, len(entries), text, origin)

			return d.applyEdit(h, e)
		}

		step := d.stepOf(holderPath)
		col := d.column(block.keyStart) + step
		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)
		text := prefixRun(brk, d.entryLines(key, pl, col, brk), origin)
		e = edit{start: block.start, end: block.end, text: text.text, origins: text.origins}

		return d.applyEdit(h, e)
	}

	first, err := d.resolve(holderPath.Child(keyOf(d, entries[0])))
	if err != nil {
		return d.bug(h, err)
	}

	last, err := d.resolve(holderPath.Child(keyOf(d, entries[len(entries)-1])))
	if err != nil {
		return d.bug(h, err)
	}

	col := d.keyColumn(first)
	end := d.valueEnd(last)

	if d.keepsTrailingLines(last) {
		end = d.blockRegion(last).end
	}

	lastLine := d.lineOf(max(0, end-1))
	at := d.lineStart(lastLine + 1)
	brk := d.breakBefore(lastLine + 1)
	text := appendRun(d.entryLines(key, pl, col, brk), newRun(brk, origin))

	if at == len(d.cur) && !strings.HasSuffix(d.cur, "\n") {
		text = run{text: brk + strings.TrimSuffix(text.text, brk), origins: append(written(brk, origin),
			text.origins[:len(text.origins)-len(brk)]...)}
	}

	return d.applyEdit(h, edit{start: at, end: at, text: text.text, origins: text.origins})
}

// keyOf returns the name of the key of entry.
func keyOf(d *Doc, entry *ast.MappingValueNode) string {
	name, _ := d.node.Resolver().KeyName(entry.Key)

	return name
}

// applyEdit applies e, and returns the problem of text the engine cannot
// parse.
func (d *Doc) applyEdit(h *Node, e edit) error {
	err := d.apply(e)
	if err != nil {
		return d.bug(h, err)
	}

	return nil
}

// entryLines returns the lines of a block entry with key and pl, at column
// col, joined with brk.
func (d *Doc) entryLines(key run, pl payload, col int, brk string) run {
	var out run

	pad := strings.Repeat(" ", col)

	for _, l := range pl.above {
		out = appendRun(out, newRun(strings.Repeat(" ", col+l.indent), originOf(l.text)))
		out = appendRun(out, l.text)
		out = appendRun(out, newRun(brk, originOf(l.text)))
	}

	out = appendRun(out, newRun(pad, originOf(key)))
	out = appendRun(out, key)
	out = appendRun(out, newRun(":", originOf(key)))

	if pl.inline != nil {
		out = appendRun(out, newRun(" ", originOf(*pl.inline)))
		out = appendRun(out, *pl.inline)
	}

	if pl.comment != nil {
		out = appendRun(out, newRun(" ", originOf(*pl.comment)))
		out = appendRun(out, *pl.comment)
	}

	if len(pl.lines) > 0 {
		out = appendRun(out, newRun(brk, originOf(key)))
		out = appendRun(out, d.joinLines(pl.lines, col, brk))
	}

	return out
}

// opening is an empty flow collection that an edit wrote in block
// context, which takes block style when a later edit adds to it.
type opening struct {
	// The bytes that the block lines replace: the collection and the space
	// before it.
	start int
	end   int
	// The start of the key or the "-" of the place that holds it.
	keyStart int
}

// blockOpening returns the empty flow collection content, the value of
// holder, when an edit wrote it in block context.
func (d *Doc) blockOpening(holder *place, content ast.Node) (opening, bool) {
	if holder.flow || holder.root {
		return opening{}, false
	}

	s, ok := d.nodeSpan(content)
	if !ok || s.end-s.start != 2 || d.origin[s.start] >= 0 {
		return opening{}, false
	}

	keyStart, ok := d.startOf(holder)
	if !ok || holder.entry == nil {
		return opening{}, false
	}

	start := s.start
	for start > 0 && d.cur[start-1] == ' ' {
		start--
	}

	return opening{start: start, end: s.end, keyStart: keyStart}, true
}

// flowInsert returns the edit that inserts text as the last of n items or
// entries of the flow collection content.
func (d *Doc) flowInsert(content ast.Node, n int, text run, origin int) edit {
	closing := 0

	switch c := content.(type) {
	case *ast.MappingNode:
		if s, ok := d.tokenSpan(c.End); ok {
			closing = s.start
		}

	case *ast.SequenceNode:
		if s, ok := d.tokenSpan(c.End); ok {
			closing = s.start
		}
	}

	if n == 0 {
		return edit{start: closing, end: closing, text: text.text, origins: text.origins}
	}

	at := closing
	for at > 0 && strings.ContainsRune(" \t\r\n", rune(d.cur[at-1])) {
		at--
	}

	text = prefixRun(", ", text, origin)

	return edit{start: at, end: at, text: text.text, origins: text.origins}
}

// appendItem appends value as the last item of the sequence seq, the
// value of holder.
func (d *Doc) appendItem(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	value any,
	origin int,
) error {
	step := d.stepOf(holderPath)

	pl, err := d.renderPayload(ctx, h, value, step, origin)
	if err != nil {
		return err
	}

	return d.appendPayload(h, holderPath, holder, seq, pl, origin)
}

// appendPayload appends the payload pl as the last item of the sequence
// seq, the value of holder.
func (d *Doc) appendPayload(
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	pl payload,
	origin int,
) error {
	step := d.stepOf(holderPath)

	if seq.IsFlowStyle {
		block, ok := d.blockOpening(holder, seq)
		if !ok {
			return d.applyEdit(h, d.flowInsert(seq, len(seq.Values), pl.flow, origin))
		}

		col := d.column(block.keyStart)
		if d.sequenceIndent() {
			col += step
		}

		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)
		text := prefixRun(brk, d.itemLines(pl, col, brk), origin)

		return d.applyEdit(h, edit{start: block.start, end: block.end, text: text.text, origins: text.origins})
	}

	last, err := d.resolve(holderPath.Index(len(seq.Values) - 1))
	if err != nil {
		return d.bug(h, err)
	}

	col := d.keyColumn(last)
	end := d.valueEnd(last)
	lastLine := d.lineOf(max(0, end-1))
	at := d.lineStart(lastLine + 1)
	brk := d.breakBefore(lastLine + 1)
	text := appendRun(d.itemLines(pl, col, brk), newRun(brk, origin))

	if at == len(d.cur) && !strings.HasSuffix(d.cur, "\n") {
		text = run{text: brk + strings.TrimSuffix(text.text, brk), origins: append(written(brk, origin),
			text.origins[:len(text.origins)-len(brk)]...)}
	}

	return d.applyEdit(h, edit{start: at, end: at, text: text.text, origins: text.origins})
}

// itemLines returns the lines of a block sequence item with pl, its "-" at
// column col, joined with brk.
func (d *Doc) itemLines(pl payload, col int, brk string) run {
	var out run

	for _, l := range pl.above {
		out = appendRun(out, newRun(strings.Repeat(" ", col+l.indent), originOf(l.text)))
		out = appendRun(out, l.text)
		out = appendRun(out, newRun(brk, originOf(l.text)))
	}

	origin := originOf(pl.flow)
	out = appendRun(out, newRun(strings.Repeat(" ", col)+"- ", origin))

	switch {
	case pl.inline != nil:
		out = appendRun(out, *pl.inline)

		if pl.comment != nil {
			out = appendRun(out, newRun(" ", origin))
			out = appendRun(out, *pl.comment)
		}

		if len(pl.lines) > 0 {
			out = appendRun(out, newRun(brk, origin))
			out = appendRun(out, d.joinLines(pl.lines, col+2, brk))
		}

	case len(pl.lines) > 0:
		base := pl.lines[0].indent
		first := pl.lines[0]
		out = appendRun(out, first.text)

		for _, l := range pl.lines[1:] {
			out = appendRun(out, newRun(brk, origin))
			l.indent -= base

			out = appendRun(out, d.joinLines([]line{l}, col+2, brk))
		}
	}

	return out
}

// fieldOf returns the name of the last selector of path, for a message.
func fieldOf(path paths.Path) string {
	last, ok := path.Last()
	if !ok {
		return "$"
	}

	if last.Kind == paths.SelectorChild {
		return last.Name
	}

	return fmt.Sprint(last.Index)
}
