package edit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// SetKey renames the key of the entry of h to key. The value and its
// comments stay in place, and the key keeps its quotes. A key that the
// entry already has, as the decode reads it, changes nothing.
func (h *Handle) SetKey(key string) error {
	d := h.d

	p, err := h.place()
	if err != nil || !p.exists {
		return err
	}

	switch {
	case p.root || p.entry == nil:
		return d.fail(h, "%s renames this item, but an item of a sequence has no key", d.label)
	case d.holdsTypeField(h.path):
		return d.fail(h, "%s renames %q, which holds a type field", d.label, h.Key())
	}

	ks, tk, ok := d.keySpan(p)
	if !ok {
		return d.fail(h, "%s cannot find the key of %s", d.label, h.path)
	}

	text, err := renderKey(context.Background(), key, tk, p.flow)
	if err != nil {
		return d.fail(h, "%s cannot write the key %q: %v", d.label, key, err)
	}

	name := keyName(text)
	if keyName(d.cur[ks.start:ks.end]) == name {
		return nil
	}

	err = d.editOnce(h, p)
	if err != nil {
		return err
	}

	d.recordEdit(h, p, fmt.Sprintf("renames %q to %q", h.Key(), key))

	parent, _ := h.path.Parent()

	if other := d.sibling(p, name); other != nil {
		otherKey, _ := d.node.Resolver().KeyName(other.Key)
		detail := d.node.Bind(niceyaml.NewError(fmt.Sprintf("%q is defined here", otherKey),
			niceyaml.AtPath(parent.Child(otherKey).Key())))

		err := h.Bind(niceyaml.NewError(
			fmt.Sprintf("%s renames %q to %q, which already exists", d.label, h.Key(), key),
			niceyaml.AtPath(h.path.Key()),
			niceyaml.WithDetails(detail, errors.New("remove one of the two")),
		))
		d.record(err)

		return err
	}

	d.invalidate(h.path)

	// PathAt gives a path that ends in `~` for a key, and so does the note
	// of a key that an earlier call renamed.
	n := d.keptNote(ks.start, ks.end)
	if last, ok := n.path.Last(); n.hasPath && (!ok || last.Kind != paths.SelectorKey) {
		n.path = n.path.Key()
	}

	err = d.splice(ks.start, ks.end, text, written(text, d.addNote(n)))
	if err != nil {
		return d.bug(h, err)
	}

	d.moved(h.path, parent.Child(key))

	return nil
}

// sibling returns the entry of the mapping of p, other than the entry of p,
// whose key the decode reads as name, or nil.
func (d *Doc) sibling(p *place, name string) *ast.MappingValueNode {
	entries, _ := astnode.Entries(p.container)

	for _, e := range entries {
		if e == p.entry || astnode.IsMergeKey(e.Key) {
			continue
		}

		s, ok := d.nodeSpan(e.Key)
		if !ok {
			continue
		}

		if keyName(strings.TrimSpace(d.cur[s.start:s.end])) == name {
			return e
		}
	}

	return nil
}

// keptNote returns the note of text that an edit writes over the bytes
// [start, end): the place in the user's text of the first byte, or the
// note of that byte when an earlier edit wrote it.
func (d *Doc) keptNote(start, end int) note {
	if start < len(d.origin) && d.origin[start] < 0 {
		return d.notes[-1-d.origin[start]]
	}

	rng := d.userRange(start, end)
	n := note{at: rng, located: true}

	if path, ok := d.user.PathAt(rng.Start); ok {
		n.path, n.hasPath = path, true
	}

	return n
}

// Remove removes the entry or the item of h, with its comments, and
// records the removal with reason. When blank lines surround the lines it
// deletes, one of them goes too, and a block collection that it empties
// becomes "{}" or "[]".
func (h *Handle) Remove(reason string) error {
	d := h.d

	p, err := h.place()
	if err != nil || !p.exists {
		return err
	}

	switch {
	case p.root:
		return d.fail(h, "%s removes the root of the document", d.label)
	case d.holdsTypeField(h.path):
		return d.fail(h, "%s removes %s, which holds a type field", d.label, h.path)
	}

	err = d.editOnce(h, p)
	if err != nil {
		return err
	}

	if p.entry != nil {
		d.recordEdit(h, p, fmt.Sprintf("removes %q", h.Key()))
	} else {
		d.recordEdit(h, p, "removes this item")
	}

	d.recordRemoval(h, p, reason)
	d.invalidate(h.path)

	anchors := d.anchorsIn(p.value)

	if p.flow {
		err = d.removeFlow(p)
	} else {
		err = d.removeBlock(h, p)
	}

	if err != nil {
		return d.bug(h, err)
	}

	d.took(h.path, true)

	if p.seq != nil {
		parent, _ := h.path.Parent()
		d.shifted(parent, p.index)
	}

	return d.keepAnchors(h, anchors)
}

// recordRemoval records the removal of the value of p, unless it holds no
// text of the user's file.
func (d *Doc) recordRemoval(h *Handle, p *place, reason string) {
	start, ok := d.startOf(p)
	if !ok {
		return
	}

	end := d.valueEnd(p)

	kept := false

	for i := start; i < end && i < len(d.origin); i++ {
		if d.origin[i] >= 0 {
			kept = true

			break
		}
	}

	if !kept {
		return
	}

	r := Removal{Reason: reason, Label: d.label}

	if p.entry != nil {
		ks, _, ok := d.keySpan(p)
		if !ok {
			ks = span{start: start, end: start + 1}
		}

		r.Range = d.userRange(ks.start, ks.end)
		r.Key = h.Key()
		r.Path = d.userPath(h.path.Key(), r.Range.Start)

		if last, ok := r.Path.Last(); !ok || last.Kind != paths.SelectorKey {
			r.Path = r.Path.Key()
		}
	} else {
		r.Range = d.userRange(d.valueStart(p), end)
		r.Path = d.userPath(h.path, r.Range.Start)
		r.Item = true
	}

	d.removals = append(d.removals, r)
}

// removeBlock removes p, an entry or an item of a block collection.
func (d *Doc) removeBlock(h *Handle, p *place) error {
	r := d.blockRegion(p)
	start, end := r.start, r.end

	if r.first > 0 && d.isBlankLine(r.first-1) && r.last+1 < len(d.curLines) && d.isBlankLine(r.last+1) &&
		d.lineStart(r.last+1) < len(d.cur) {
		end = d.lineStart(r.last + 2)
	}

	// The last line of a file without a final newline gives its place to
	// the line before it, which loses its line break.
	if end == len(d.cur) && !strings.HasSuffix(d.cur, "\n") && start > 0 {
		start--
		if start > 0 && d.cur[start-1] == '\r' {
			start--
		}
	}

	if d.count(p) > 1 || p.container == nil {
		return d.splice(start, end, "", nil)
	}

	parent, _ := h.path.Parent()

	empty := "{}"
	if p.seq != nil {
		empty = "[]"
	}

	if parent.IsRoot() {
		text := empty + d.breakBefore(r.first)

		return d.splice(start, end, text, written(text, d.addNote(note{})))
	}

	holder, err := d.resolve(parent)
	if err != nil {
		return err
	}

	at := d.afterProps(holder)
	text := " " + empty

	return d.apply(
		edit{start: at, end: at, text: text, origins: written(text, d.addNote(note{}))},
		edit{start: start, end: end},
	)
}

// count returns the number of entries or items of the container of p.
func (d *Doc) count(p *place) int {
	if p.seq != nil {
		return len(p.seq.Values)
	}

	entries, _ := astnode.Entries(p.container)

	return len(entries)
}

// afterProps returns the offset past the ":" or the "-" of p and past the
// anchor and the tag on its line, where an empty collection goes.
func (d *Doc) afterProps(p *place) int {
	at := 0

	switch {
	case p.entry != nil && p.entry.Start != nil:
		if s, ok := d.tokenSpan(p.entry.Start); ok {
			at = s.end
		}

	case p.seq != nil && p.index >= 0 && p.index < len(p.seq.Entries) && p.seq.Entries[p.index].Start != nil:
		if s, ok := d.tokenSpan(p.seq.Entries[p.index].Start); ok {
			at = s.end
		}
	}

	line := d.lineOf(at)

	for n := p.value; n != nil; {
		var next ast.Node

		switch x := n.(type) {
		case *ast.AnchorNode:
			next = x.Value
		case *ast.TagNode:
			next = x.Value
		default:
			return at
		}

		props, ok := d.propsSpan(n)
		if !ok || d.lineOf(props.start) != line {
			return at
		}

		at = props.end
		n = next
	}

	return at
}

// propsSpan returns the bytes of the anchor or the tag that node writes, the
// name of an anchor included.
func (d *Doc) propsSpan(node ast.Node) (span, bool) {
	switch x := node.(type) {
	case *ast.AnchorNode:
		s, ok := d.tokenSpan(x.Start)
		if !ok {
			return span{}, false
		}

		if name, ok := d.tokenSpan(astnode.Token(x.Name)); ok {
			s.end = name.end
		}

		return s, true

	case *ast.TagNode:
		return d.tokenSpan(x.Start)
	}

	return span{}, false
}

// removeFlow removes p, an entry or an item of a flow collection, with the
// comma after it, or the comma before it for the last one.
func (d *Doc) removeFlow(p *place) error {
	start, ok := d.startOf(p)
	if !ok {
		return nil
	}

	end := d.valueEnd(p)

	i := end
	for i < len(d.cur) && strings.ContainsRune(" \t\r\n", rune(d.cur[i])) {
		i++
	}

	if i < len(d.cur) && d.cur[i] == ',' {
		j := i + 1
		for j < len(d.cur) && (d.cur[j] == ' ' || d.cur[j] == '\t') {
			j++
		}

		return d.splice(start, j, "", nil)
	}

	j := start - 1
	for j >= 0 && strings.ContainsRune(" \t\r\n", rune(d.cur[j])) {
		j--
	}

	if j >= 0 && d.cur[j] == ',' {
		return d.splice(j, end, "", nil)
	}

	return d.splice(start, end, "", nil)
}

// bug returns the problem of an edit whose text the engine cannot
// parse, which shows a bug in the engine.
func (d *Doc) bug(h *Handle, err error) error {
	app := d.opts.App
	if app == "" {
		app = "this program"
	}

	bound := h.Bind(niceyaml.Place(&engineError{
		msg: app + " cannot migrate this value without changing it",
		err: err,
	}, niceyaml.WithDetails(fmt.Errorf("report this to the authors of %s", app))))
	d.record(bound)

	return bound
}

// engineError is the problem of a bug in the engine, which wraps
// [ErrEngine].
type engineError struct {
	err error
	msg string
}

// Error returns the message of e.
func (e *engineError) Error() string {
	return e.msg
}

// Unwrap returns [ErrEngine] and the error behind it.
func (e *engineError) Unwrap() []error {
	return []error{ErrEngine, e.err}
}

// scalarToken returns the token of the scalar of node, or nil for a node
// that holds no scalar.
func scalarToken(node ast.Node) *token.Token {
	c := astnode.Content(node)
	if !astnode.IsScalar(c) || isImplicitNull(c) {
		return nil
	}

	return c.GetToken()
}
