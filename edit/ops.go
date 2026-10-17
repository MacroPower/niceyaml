package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// SetKey renames the key of the entry. The value and its comments stay in
// place, and the key keeps its quotes. A decode then reads the key as the
// string key, so SetKey quotes a key that a decode reads as another value,
// as it writes "0x10" for a rename to 0x10. SetKey fails on a sequence item
// and on the root of the document, because neither has a key.
//
// SetKey leaves the text as it is when a decode reads the key as key and a
// path by key selects the entry already. The key then keeps its text.
// Otherwise it writes key. A place that does not hold a value stays as it
// is.
//
// Go-yaml's parser compares keys by their text, as a path names them.
// SetKey fails when the new key has the text of another key of its
// mapping. The problem binds at the key that the call renames, where the
// package documentation places an error at a renamed key. It does not
// match [ErrEngine], and a detail binds at the other key. Under
// [niceyaml.WithAllowDuplicateKeys], such a rename fails with
// [ErrUnsupported], as [ConstructRepeatedKey] names it. A rename in a
// mapping with a `<<` merge key that hides or reveals a merged entry fails
// with ErrUnsupported too, as the package documentation describes. A
// reference document of [niceyaml.WithReferences] that a merge key of the
// mapping reads decides which names the mapping reads. A rename there fails
// with a problem that matches [paths.ErrReferenceAlias].
func (n *Node) SetKey(key string) error {
	c := n.d.modelStart(n, fmt.Sprintf("renames %s to %q", n.path, key), false)
	r, err := n.setKey(c, key)

	return n.d.modelEnd(c, err, func() bool { return r.kept || c.rename(&r) })
}

// setKey renames the key of the entry, as [Node.SetKey] describes. The
// model reads the place of the rename as c gives it. It returns the rename
// as the model reads it.
//
//nolint:contextcheck // A rename writes text and reads no state that a context carries.
func (n *Node) setKey(c *modelCall, key string) (renaming, error) {
	d := n.d
	kept := renaming{kept: true}

	p, err := n.place(useKey, "renames")
	if err != nil {
		return kept, err
	}

	// The text lacks the place, so the rename does nothing, unless a step of
	// the path reaches a zone of the Apply.
	if !p.exists {
		return kept, n.stepProblem()
	}

	why := d.protection(p, false)

	switch {
	case p.root:
		return kept, d.fail(n, "%s renames the root of the document, which has no key", d.subject())
	case p.entry == nil:
		return kept, d.fail(n, "%s renames this item, but an item of a sequence has no key", d.subject())
	case why != "":
		return kept, d.fail(n, "%s renames %q, which %s", d.subject(), n.Key(), why)
	}

	ks, tk, ok := d.keySpan(p)
	if !ok {
		return kept, d.bug(n, fmt.Errorf("the key of %s has no text", n.path))
	}

	// The text keeps a key that a decode reads as key, when a path by key
	// selects the entry already.
	if d.keeps(p, key) {
		return kept, nil
	}

	text := renderKey(context.Background(), key, tk, p.flow)
	r := renaming{key: key, name: key}

	d.recordEdit(p, fmt.Sprintf("renames %q to %q", n.Key(), key), fmt.Sprintf("renames %s to %q", n.path, key))

	// The parse of the text refuses some keys that repeat a key, so the
	// rename fails before it writes. A Source that allows a mapping to
	// repeat a key parses such a text, so the rename then reaches a
	// repeated key, after the zones of the Apply and before the rule of
	// reference documents.
	var (
		rep     repeated
		repeats bool
	)

	if c != nil {
		rep, repeats = c.x.repeat(c.renames(&r))
	}

	if repeats && !hook.AllowsDuplicateKeys(d.user.Source()) {
		return kept, d.repeatProblem(c.x, rep, *d.lastEdit, n.path)
	}

	named := fmt.Sprintf("renames %s to %q", n.path, key)

	err = d.reachProblem(reachCall{
		kind: callKey, path: n.path, hidden: n.hidden, key: key, named: named, repeats: repeats,
	})
	if err != nil {
		return kept, err
	}

	err = d.ownKeysProblem(p.entry, p.container, key, true, named)
	if err != nil {
		return kept, err
	}

	if repeats {
		return kept, d.repeatRefusal(c.x, rep, p.entry, key, named)
	}

	// The paths at the entry and below it take the new key.
	at := d.reached(d.spotOf(n.path, p))

	// PathAt gives a path that ends in `~` for a key, and so does the note
	// of a key that an earlier call renamed. The note of a key that a move
	// wrote holds the path of the moved value, so a value that the user
	// wrote under a key gets the `~` of that key. An item of the user's text
	// does not have a key, and the note of a created value names the value
	// that holds it, so both keep their path.
	note := d.keptNote(ks.start, ks.end)
	if last, ok := note.path.Last(); note.hasPath && note.detail == "" && ok && last.Kind == paths.SelectorChild {
		note.path = note.path.Key()
	}

	origin := d.addNote(note)

	err = d.apply(edit{start: ks.start, end: ks.end, text: text, origins: written(text, origin)})
	if errors.Is(err, errParserRefuses) {
		err = &keyRefusalError{err: err, fix: fmt.Sprintf("write a key other than %q", key)}
	}

	if err != nil {
		return kept, d.bug(n, err)
	}

	at.rebase(func(head paths.Path) paths.Path {
		holder, _ := head.Parent()

		return holder.Child(key)
	})

	return r, nil
}

// keeps reports whether a rename of p, an entry, to key leaves the text as
// it is. It does so when a decode reads the key of p as key and a path by
// key selects p, as p reads it with [Doc.resolveFrom].
func (d *Doc) keeps(p *place, key string) bool {
	if keyOf(p.entry.Key, d.keyReader()) != key {
		return false
	}

	holder, _ := p.path.Parent()

	by, err := d.resolveFrom(p, holder.Child(key))

	return err == nil && by.exists && by.entry == p.entry
}

// keyContent returns the key of e under the "?" of an explicit key.
func keyContent(e *ast.MappingValueNode) ast.Node {
	if explicit, ok := e.Key.(*ast.MappingKeyNode); ok {
		return explicit.Value
	}

	return e.Key
}

// ownRepeat returns the problem of the Set of h when v holds a mapping
// with two keys that a decode reads as one, such as the int 1 and the
// string "1" of a map[any]any, or the int 1 and the float 1.0. The problem
// is about the value of the call, so it comes before the reach of the Set
// and the finder on the YAML of v. It fails before the Set writes, so the
// line breaks of the text do not change the problem. The problem binds
// where every problem of the Set binds before the Set writes the text of
// origin, as [Doc.setSite] gives the site. OwnRepeat returns nil for a v
// without such a mapping.
func (d *Doc) ownRepeat(h *Node, v *value, origin int) error {
	x := &model{}
	x.wroteIn(v)

	var (
		r  repeated
		ok bool
	)

	// The writes of each key of each mapping, in the order of the writes.
	writes := make(map[*value]map[string][]int)

	for i, w := range x.wrote {
		if writes[w.owner] == nil {
			writes[w.owner] = make(map[string][]int)
		}

		writes[w.owner][w.key] = append(writes[w.owner][w.key], i)
	}

	for i, w := range x.wrote {
		at := writes[w.owner][w.key]
		if j := slices.Index(at, i); j+1 < len(at) {
			r, ok = repeated{w: w, other: x.wrote[at[j+1]].e}, true

			break
		}
	}

	if !ok {
		return nil
	}

	desc := "sets " + h.path.String()
	if d.lastEdit != nil {
		desc = d.lastEdit.desc
	}

	msg := fmt.Sprintf("%s %s, but the value it writes holds two keys that a decode reads as %s",
		d.subject(), desc, keyLabel(r.w.key))

	at := d.setSite(h, origin)
	details := append([]error{errors.New("remove one of the two")}, d.callDetails(at)...)

	err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(at.path), niceyaml.AtRange(at.at),
		niceyaml.WithDetails(details...)))
	d.record(err)

	return err //nolint:wrapcheck // The binding is the problem of the call.
}

// repeatProblem returns the problem of the call of rec, as [model.repeat]
// finds it in r. The call makes w, an entry of the model whose key the call
// writes, read the key of other, another entry of its mapping. The problem
// binds at the key of the entry of the text that pairs with w, under the
// path of the key, as [Doc.keyPlace] places it. That key is a renamed key,
// a moved key, or a key that the call created. The detail of the problem
// binds at the key of other. Without such an entry, the problem binds at
// the path at. The Apply records the problem.
func (d *Doc) repeatProblem(x *model, r repeated, rec editRecord, at paths.Path) error {
	w, other := r.w, r.other

	var details []error

	// The key of other exists already.
	if place, ok := d.clashKey(x, other); ok {
		details = append(details, d.user.Bind(niceyaml.NewError(fmt.Sprintf("%s is defined here", keyLabel(other.key)),
			niceyaml.AtPath(place.path), niceyaml.AtRange(place.at), niceyaml.WithDetails(d.writerDetails(place)...))))
	}

	msg := fmt.Sprintf("%s %s, which already exists", d.subject(), rec.desc)
	fix := errors.New("remove one of the two")

	if !w.renamed {
		msg = fmt.Sprintf("%s %s, but %s already exists", d.subject(), rec.desc, keyLabel(w.key))
	}

	var s span

	ok := w.te != nil && w.te.entry != nil
	if ok {
		s, ok = d.nodeSpan(w.te.entry.Key)
	}

	if !ok {
		return d.fail(&Node{d: d, path: at}, "%s", msg)
	}

	place := d.keyPlace(s)
	details = append(append(details, fix), d.callDetails(place)...)

	err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(place.path), niceyaml.AtRange(place.at),
		niceyaml.WithDetails(details...)))
	d.record(err)

	return err //nolint:wrapcheck // The binding is the problem of the call.
}

// clashKey returns where a problem at the key of the entry of the current
// text that pairs with other, an entry of the model of x, binds, as
// [Doc.keyPlace] gives it. It reports false when the text holds no such
// entry.
func (d *Doc) clashKey(x *model, other *edge) (site, bool) {
	if other.seen != x.epoch || other.mate == nil || other.mate.entry == nil {
		return site{}, false
	}

	whole, ok := d.nodeSpan(other.mate.entry.Key)
	if !ok {
		return site{}, false
	}

	return d.keyPlace(whole), true
}

// keptNote returns the note of text that an edit writes over the bytes
// [start, end). The note holds the place in the user's text of the first
// byte, or the note of that byte when an earlier edit wrote it.
func (d *Doc) keptNote(start, end int) note {
	if start < len(d.origin) && d.origin[start] < 0 {
		n := d.notes[-1-d.origin[start]]
		n.header, n.text, n.str, n.hasStr = nil, nil, "", false

		return n
	}

	rng := d.userRange(start, end)
	n := note{at: rng, located: true}

	if path, ok := d.user.PathAt(rng.Start); ok {
		n.path, n.hasPath = path, true
	}

	return n
}

// RemoveOption configures one call of [Node.Remove].
//
// Options:
//   - [WithReason]
type RemoveOption func(*removeConfig)

// removeConfig holds the settings that each [RemoveOption] gives a
// removal.
type removeConfig struct {
	reason string
}

// WithReason is a [RemoveOption] that gives the reason for the removal,
// which [Removal.Reason] holds.
func WithReason(reason string) RemoveOption {
	return func(c *removeConfig) {
		c.reason = reason
	}
}

// Remove removes the entry or the item, with its comments. [Doc.Removals]
// lists the removal of a value of the user's text.
//
// In a flow collection, Remove deletes the text of the entry or the item
// with its comma. When a comment ends the last line of that text and no
// other text stays on that line, Remove deletes the comment too. A comma
// that starts that line and follows the entry or the item before does not
// count as such text. When no other text stays on the lines of that text,
// Remove deletes the lines. When other text stays only before that text on
// its line, Remove also deletes the spaces and tabs around that text. The
// line then does not end with them. A comment that stays right after other
// text keeps a space before it, so it still reads as a comment.
//
// Remove also deletes blank lines next to the lines that it deletes. When
// blank lines come both before and after those lines, it deletes one blank
// line after them. When only blank lines and a "..." document end marker
// follow them, it deletes the blank lines before them. When only blank
// lines, a "---" document header, and directives come before them, it
// deletes the blank lines after them. The document thus does not gain a
// blank line at its start or its end.
//
// A block collection that Remove empties becomes "{}" or "[]". At the
// root, the "{}" or "[]" takes the place of the lines of the collection,
// on a line of its own, and ends with the line break of the last of them.
// A text without a final newline thus still ends without one. The blank
// lines next to those lines stay. A later edit that adds to the "{}" or
// "[]" writes block lines in its place. A comment after the "{}" or "[]"
// stays on its line, and the block lines go below it. At the root and in
// an item of a block sequence, an anchor or a tag on the line of the "{}"
// or "[]" keeps it in flow style, as in "- &x {}". A root whose anchor or
// tag has a line of its own takes the block lines below that line.
//
// Remove fails with [ErrUnsupported] when an alias outside the entry or the
// item reads an anchor inside it, as [ConstructAliasCopy] names it. The
// engine does not write a copy of the value at the alias, so a program
// removes the aliases first, as the package documentation describes. A
// Remove of an entry of a mapping fails with a problem that matches
// [paths.ErrReferenceAlias] when a reference document of
// [niceyaml.WithReferences] decides which names the mapping reads, through
// a merge key that reads it.
//
// An entry or an item that starts on the line of the "-" of an item leaves
// the "-" in place. The next entry or item then takes that line, with its
// comments. When another comment line lies between the two, the "-" ends
// its line instead. A place that does not hold a value stays as it is.
func (n *Node) Remove(opts ...RemoveOption) error {
	c := n.d.modelStart(n, "removes "+n.path.String(), false)
	err := n.remove(opts...)

	return n.d.modelEnd(c, err, c.remove)
}

// remove removes the entry or the item, as [Node.Remove] describes.
func (n *Node) remove(opts ...RemoveOption) error {
	d := n.d

	var cfg removeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	p, err := n.place(useEdit, "removes")
	if err != nil {
		return err
	}

	// The text lacks the place, so the removal does nothing, unless a step
	// of the path reaches a zone of the Apply.
	if !p.exists {
		return n.stepProblem()
	}

	why := d.protection(p, true)

	switch {
	case p.root:
		return d.fail(n, "%s removes the root of the document", d.subject())
	case why != "":
		return d.fail(n, "%s removes %s, which %s", d.subject(), n.path, why)
	}

	err = d.reachProblem(reachCall{
		kind: callRemove, path: n.path, hidden: n.hidden, named: "removes " + n.path.String(),
	})
	if err != nil {
		return err
	}

	if p.entry != nil {
		err = d.ownKeysProblem(p.entry, p.container, "", false, "removes "+n.path.String())
		if err != nil {
			return err
		}
	}

	if p.entry != nil {
		d.recordEdit(p, fmt.Sprintf("removes %q", n.Key()), "removes "+n.path.String())
	} else {
		d.recordEdit(p, "removes this item", "removes "+n.path.String())
	}

	s := d.spotOf(n.path, p)

	d.recordRemoval(n, p, cfg.reason)

	// The paths that reach the value go, and those below the later items of
	// its sequence take the index before them.
	at := d.reached(s)

	var items *reached

	if p.seq != nil {
		items = d.reached(d.itemsOf(n.path, p))
	}

	// The anchors of the entry go with it, the anchor of its key too.
	taken := p.value
	if p.entry != nil {
		taken = p.entry
	}

	anchors := d.anchorsIn(taken)

	err = d.aliasCopyProblem(anchors, p.value, "removes "+n.path.String(), n.path)
	if err != nil {
		return err
	}

	if p.flow {
		err = d.removeFlow(p)
	} else {
		err = d.removeBlock(p)
	}

	if err != nil {
		return d.bug(n, err)
	}

	at.take(true, d.takenBy())

	if items != nil {
		items.shift(p.index)
	}

	return nil
}

// recordRemoval records the removal of the value of p through h when the
// value holds text of the user's file. The removal names the value as the
// user's text holds it, whatever renames and moves came before.
func (d *Doc) recordRemoval(h *Node, p *place, reason string) {
	start, ok := d.startOf(p)
	if !ok {
		return
	}

	end := d.valueEnd(p)

	// The removal binds at the key or the "-" of the value, or at the first
	// byte of an item of a flow sequence. When an edit wrote that byte with
	// a note of its own, as for a key that Move created, the removal binds
	// at the first text of the user in the value. The first byte of a value
	// that an edit wrote can hold the note of the key, the "-", or the item
	// that held the value, as [Doc.heldAt] finds it. The removal then binds
	// at that byte. So does a first byte that an edit wrote with the origin
	// of a byte of the user, such as the bracket or the null that a write
	// in flow style gives the origin of the "-" or the alias of the item.
	at := -1

	for i := start; i < end && i < len(d.origin); i++ {
		if _, held := d.heldAt(i); d.standsFor(i) || d.ownByte(i) || held || i == start && d.origin[i] >= 0 {
			at = i

			break
		}
	}

	if at < 0 {
		return
	}

	r := Removal{Reason: reason, Label: d.label}

	rng, path, ok := d.removedAt(p, at, start)
	if !ok {
		path = h.path
		if p.entry != nil {
			path = path.Key()
		}
	}

	switch last, _ := path.Last(); last.Kind {
	case paths.SelectorIndex:
		// An item runs from its "-" through its value, as the user's text
		// holds them. A move into a flow sequence leaves no "-" of a scalar
		// item in the current text, so the range starts at the "-" of the
		// user's text. The range ends where the user's text of the value
		// ends. An edit inside the value that took some of its text or
		// moved other text into it does not change that end. An item of a
		// flow sequence starts where the user's text of its value starts.
		// For a mapping that the user wrote without braces, that start
		// comes before the tag, the anchor, or the "?" of its first key.
		// The "{" that a move gives such a mapping binds at that key.
		r.Item = true

		span, spanned := d.userSpan(path)

		if dash, ok := d.userDash(path); ok {
			rng.Start = dash
		} else if spanned && comparePositions(span.Start, rng.Start) < 0 {
			rng.Start = span.Start
		}

		if spanned && comparePositions(span.End, rng.End) > 0 {
			rng.End = span.End
		}

	case paths.SelectorChild:
		// The byte lies in the value of an entry, so the range is that of
		// the key.
		r.Key, path = last.Name, path.Key()

		rs, err := d.user.Ranges(path)
		if err == nil && len(rs) > 0 {
			rng = rs[0]
		}

	case paths.SelectorKey:
		parent, _ := path.Parent()
		if name, ok := parent.Last(); ok && name.Kind == paths.SelectorChild {
			r.Key = name.Name
		}

	default:
		// A path of the user's text holds only names, indexes, and keys.
	}

	r.Path, r.Range = path, rng
	d.removals = append(d.removals, r)
}

// removedAt returns the range and the path in the user's text of the byte
// at off. That byte is the first byte of a value of p that holds text of
// the user or stands for it. The key or the "-" of p starts at start, and
// for that key or "-", the range covers all of it. It reports false when
// the user's text gives no path there.
func (d *Doc) removedAt(p *place, off, start int) (position.Range, paths.Path, bool) {
	if n, ok := d.heldAt(off); ok {
		return n.at, n.path, n.hasPath
	}

	if o := d.origin[off]; o < 0 {
		n := d.notes[-1-o]
		if p.entry == nil && off == start {
			n = d.bareNote(n)
		}

		return n.at, n.path, n.hasPath
	}

	// The range of a key covers all of it. The key of an explicit key
	// starts past the "?", and a renamed key stands for the key that the
	// user wrote.
	if ks, _, ok := d.keySpan(p); ok && off == start {
		n := d.keptNote(ks.start, ks.end)

		return n.at, n.path, n.hasPath
	}

	rng := d.userRange(off, off+1)

	path, ok := d.user.PathAt(rng.Start)
	if ok && p.entry == nil && off == start {
		path = d.bareItem(path)
	}

	return rng, path, ok
}

// heldAt returns the note of the key, the "-", or the item of a flow
// sequence that held a value before an edit wrote the byte at off. That
// byte is the first byte of the value, and a Set or [Doc.heldRun] gave it
// a note that holds the other. It reports false for a byte without such a
// note.
func (d *Doc) heldAt(off int) (note, bool) {
	if off >= len(d.origin) || d.origin[off] >= 0 {
		return note{}, false
	}

	n := d.notes[-1-d.origin[off]]
	if n.detail == "" || n.held >= 0 {
		return note{}, false
	}

	held := d.notes[-1-n.held]

	return held, held.located
}

// userDash returns the position of the "-" of the item at path, a path of
// the user's text. It reports false when path does not name an item of a
// block sequence there.
func (d *Doc) userDash(path paths.Path) (position.Position, bool) {
	last, ok := path.Last()
	if !ok || last.Kind != paths.SelectorIndex {
		return position.Position{}, false
	}

	parent, _ := path.Parent()

	node, err := d.user.Resolver().Node(parent)
	if err != nil {
		return position.Position{}, false
	}

	seq, ok := astnode.Content(node).(*ast.SequenceNode)
	if !ok || seq.IsFlowStyle || last.Index < 0 || last.Index >= len(seq.Entries) {
		return position.Position{}, false
	}

	e := seq.Entries[last.Index]
	if e.Start == nil || e.Start.Position == nil {
		return position.Position{}, false
	}

	return position.NewFromToken(e.Start), true
}

// userSpan returns the range in the user's text of the value at path, a
// path of the user's text, from its first token through its last. It
// reports false when the user's text does not hold such a value there.
func (d *Doc) userSpan(path paths.Path) (position.Range, bool) {
	node, ok := d.userValue(path)
	if !ok {
		return position.Range{}, false
	}

	lines := d.user.Source().Lines()

	first, last, ok := outerSpans(node, func(tk *token.Token) (position.Range, bool) {
		rs := lines.ContentRanges(tk)
		if len(rs) == 0 {
			return position.Range{}, false
		}

		return position.NewRange(rs[0].Start, rs[len(rs)-1].End), true
	})
	if !ok {
		return position.Range{}, false
	}

	rng := first
	if comparePositions(last.Start, rng.Start) < 0 {
		rng.Start = last.Start
	}

	if comparePositions(last.End, rng.End) > 0 {
		rng.End = last.End
	}

	return rng, true
}

// userValue returns the value at path, a path of the user's text, as the
// user wrote it there. An alias there thus stays an alias. It reports
// false when the user's text does not hold a value at path.
func (d *Doc) userValue(path paths.Path) (ast.Node, bool) {
	if path.IsRoot() {
		root := d.user.AST()

		return root, !astnode.IsNil(root)
	}

	parent, _ := path.Parent()
	last, _ := path.Last()

	r := d.user.Resolver()

	holder, err := r.Node(parent)
	if err != nil {
		return nil, false
	}

	switch last.Kind {
	case paths.SelectorIndex:
		seq, ok := astnode.Content(holder).(*ast.SequenceNode)
		if !ok || last.Index < 0 || last.Index >= len(seq.Values) {
			return nil, false
		}

		return seq.Values[last.Index], true

	case paths.SelectorChild:
		entries, _ := astnode.Entries(astnode.Content(holder))

		// The last entry of a key wins, as a path selects it.
		for _, e := range slices.Backward(entries) {
			if name, ok := r.KeyName(e.Key); ok && name == last.Name && !astnode.IsMergeKey(e.Key) {
				return e.Value, true
			}
		}

	default:
		// A path of the user's text holds only names, indexes, and keys,
		// and a key does not hold a value.
	}

	return nil, false
}

// removeBlock removes p, an entry or an item of a block collection.
func (d *Doc) removeBlock(p *place) error {
	if d.sharesLine(p) {
		return d.removeFirst(p)
	}

	r := d.blockRegion(p)

	if d.count(p) > 1 || p.container == nil {
		start, end := d.lineCut(r)
		end, text := d.emptiedAfter(start, end)

		cut := edit{start: start, end: end, text: text.text, origins: text.origins}

		return d.apply(cut)
	}

	empty := "{}"
	if p.seq != nil {
		empty = "[]"
	}

	// The "{}" or "[]" of the root takes the place of the lines, on a line
	// of its own, and ends with the line break of the last of them. The
	// line before them keeps its line break, and the blank lines next to
	// them stay.
	if p.container == astnode.Content(d.node.AST()) {
		text := empty + d.lineBreak(r.last)

		return d.splice(r.start, r.end, text, written(text, d.addNote(d.emptiedNote(nil))))
	}

	start, end := d.lineCut(r)

	// The collection goes after the key or the "-" that holds it. For a
	// path through an alias or a merge key, that key or "-" lies away from
	// the path.
	holder, ok := d.holderOf(p.container)
	if !ok {
		return errors.New("no entry or item holds the collection")
	}

	at, sep := d.afterProps(holder), " "
	if at == d.indicatorEnd(holder) {
		at, sep = d.separated(at)
	}

	text := sep + empty
	if at < len(d.cur) && d.cur[at] == '#' {
		text += " "
	}

	return d.apply(
		edit{start: at, end: at, text: text, origins: written(text, d.addNote(d.emptiedNote(holder)))},
		edit{start: start, end: end},
	)
}

// lineCut returns the bytes that a removal of the lines of r deletes: the
// lines with their line breaks, and the blank lines next to them that go
// with them, as [Doc.blankCut] finds them. A cut of the last lines of the
// text leaves the text ending as the text of the user ends. When that text
// lacks a final newline, the line before the cut loses its line break. A
// block scalar that needs that line break takes one back, as
// [Doc.keepStrings] writes it.
func (d *Doc) lineCut(r region) (int, int) {
	start, end := d.blankCut(r)

	if end == len(d.cur) && !endsLine(d.orig) && start > 0 {
		start = d.lineEnd(d.lineOf(start - 1))
	}

	return start, end
}

// emptiedAfter returns the end of the bytes that a cut of the lines
// [start, end) takes the place of, and the text that it writes there. A
// block scalar that ends above the cut then ends right above the blank
// lines after it. The parser reads a blank line that holds more spaces
// than the lines of that scalar indent by as text of the scalar, as
// [Doc.scalarEnd] tells. So the cut writes the blank lines up to the last
// such line empty, with their line breaks.
func (d *Doc) emptiedAfter(start, end int) (int, run) {
	if start == 0 || end >= len(d.cur) {
		return end, run{}
	}

	first := d.lineOf(end)
	if d.spacedBlanks(first) < first {
		return end, run{}
	}

	lit, held := d.scalarAbove(d.lineOf(start))
	if lit == nil {
		return end, run{}
	}

	indent := d.scalarIndent(lit, held)
	if indent < 0 {
		return end, run{}
	}

	spaced := -1

	for l := first; l < len(d.curLines) && d.lineStart(l) < len(d.cur) && d.isBlankLine(l); l++ {
		if len(d.lineText(l)) > indent {
			spaced = l
		}
	}

	if spaced < 0 {
		return end, run{}
	}

	var text run

	for l := first; l < spaced; l++ {
		text = appendRun(text, d.keptRun(d.lineEnd(l), d.lineStart(l+1)))
	}

	return d.lineEnd(spaced), text
}

// blankCut returns the lines of r with their line breaks, and the blank
// lines next to them that go with them. A blank line between two groups of
// lines stays, and the document does not gain a blank line at its start or
// its end. The blank lines after a block scalar with keep chomping belong
// to its string. When such a scalar ends before r, the blank lines before
// r stay with it, and the blank lines after r go, so that the scalar does
// not take them. The blank lines before r that hold text of a block scalar
// above r stay, as [Doc.scalarAbove] tells.
func (d *Doc) blankCut(r region) (int, int) {
	start, end := r.start, r.end

	before := r.first > 0 && d.isBlankLine(r.first-1)
	after := r.last+1 < len(d.curLines) && d.isBlankLine(r.last+1) && d.lineStart(r.last+1) < len(d.cur)
	held := -1

	if (before || after) && r.first > 0 {
		var lit *ast.LiteralNode

		lit, held = d.scalarAbove(r.first)
		if lit != nil && keepsChomping(lit) {
			return start, d.blanksEnd(r.last + 1)
		}
	}

	switch {
	case before && after:
		end = d.lineStart(r.last + 2)

	case before && held < r.first-1 && !d.contentAfter(r.last):
		l := r.first - 1
		for l > 0 && l-1 > held && d.isBlankLine(l-1) {
			l--
		}

		start = d.lineStart(l)

	case after && !d.contentBefore(r.first):
		l := r.last + 1
		for l+1 < len(d.curLines) && d.isBlankLine(l+1) && d.lineStart(l+1) < len(d.cur) {
			l++
		}

		end = d.lineStart(l + 1)
	}

	return start, end
}

// emptiedNote returns the note of the "{}" or "[]" that a removal writes
// for a collection that it empties. The text stands for the collection,
// so an error there binds where an error at the collection binds. Holder
// is the entry or the item that holds the collection, and the note binds
// at its key or its "-", under the path of the collection. An item of a
// flow sequence has neither, so the note binds at the item. For a nil
// holder, the collection is the root, which binds at its first key or
// item in the user's text. A key that an edit created gives its note, so
// an error there names that edit.
func (d *Doc) emptiedNote(holder *place) note {
	if holder == nil {
		s, ok := d.userRootSite()

		return note{path: paths.Doc(), hasPath: true, at: s.at, located: ok}
	}

	start, ok := d.startOf(holder)
	if !ok {
		return note{}
	}

	end := start + 1
	if ks, _, ok := d.keySpan(holder); ok && ks.end > end {
		end = ks.end
	}

	n := d.keptNote(start, end)
	n.moved = false

	if holder.seq != nil && holder.seq.IsFlowStyle {
		n = d.bareNote(n)
	}

	if last, ok := n.path.Last(); n.hasPath && ok && last.Kind == paths.SelectorKey {
		n.path, _ = n.path.Parent()
	}

	return n
}

// holderOf returns the entry or the item whose value is node, a
// collection of the current text, under its anchors and tags.
func (d *Doc) holderOf(node ast.Node) (*place, bool) {
	up := d.ancestors(node)
	value := node

	for len(up) > 0 && isProps(up[0]) {
		value, up = up[0], up[1:]
	}

	if len(up) == 0 {
		return nil, false
	}

	switch parent := up[0].(type) {
	case *ast.MappingValueNode:
		if parent.Value != value {
			return nil, false
		}

		return &place{entry: parent, value: value}, true

	case *ast.SequenceNode:
		i, ok := d.tree().item[value]
		if !ok || i >= len(parent.Values) || parent.Values[i] != value {
			return nil, false
		}

		return &place{seq: parent, index: i, value: value}, true
	}

	return nil, false
}

// removeFirst removes p, the first entry or item of a block collection
// that starts on the line of the "-" of an item. The "-" stays. The next
// entry or item takes its place on that line, or "{}" or "[]" does when p
// is the only one. When a line other than a blank one lies between p and
// the next entry or item, the "-" ends its line instead.
func (d *Doc) removeFirst(p *place) error {
	start, _ := d.startOf(p)
	r := d.blockRegion(p)

	next, ok := d.nextPlace(p)
	if !ok {
		empty := "{}"
		if p.seq != nil {
			empty = "[]"
		}

		// A root collection shares its line with its anchor or its tag, as
		// in "!!map a: 1", and no item holds it.
		var holder *place

		if p.container != astnode.Content(d.node.AST()) {
			holder, ok = d.holderOf(p.container)
			if !ok {
				return errors.New("no item holds the collection")
			}
		}

		return d.splice(start, d.lineEnd(r.last), empty, written(empty, d.addNote(d.emptiedNote(holder))))
	}

	nr := d.blockRegion(next)

	for l := r.last + 1; l < nr.first; l++ {
		if !d.isBlankLine(l) {
			cut := start
			for cut > 0 && d.cur[cut-1] == ' ' {
				cut--
			}

			return d.splice(cut, d.lineEnd(r.last), "", nil)
		}
	}

	return d.splice(start, d.lineStart(nr.first)+d.indentOf(nr.first), "", nil)
}

// nextPlace returns the entry or the item after p in its block collection.
func (d *Doc) nextPlace(p *place) (*place, bool) {
	if p.seq != nil {
		if p.index+1 >= len(p.seq.Values) {
			return nil, false
		}

		return &place{container: p.container, seq: p.seq, index: p.index + 1, value: p.seq.Values[p.index+1]}, true
	}

	entries, _ := astnode.Entries(p.container)

	i := slices.Index(entries, p.entry)
	if i < 0 || i+1 >= len(entries) {
		return nil, false
	}

	e := entries[i+1]

	return &place{container: p.container, entry: e, value: e.Value}, true
}

// contentAfter reports whether a line after line holds content: any text
// other than spaces and a "..." document end marker.
func (d *Doc) contentAfter(line int) bool {
	for l := line + 1; l < len(d.curLines) && d.lineStart(l) < len(d.cur); l++ {
		text := strings.TrimSpace(d.lineText(l))
		if text != "" && !isEndMarker(text) {
			return true
		}
	}

	return false
}

// contentBefore reports whether a line before line holds content: any
// text other than spaces, a "---" document header, and a directive.
func (d *Doc) contentBefore(line int) bool {
	for l := line - 1; l >= 0; l-- {
		text := strings.TrimSpace(d.lineText(l))
		if text != "" && !isHeader(text) {
			return true
		}
	}

	return false
}

// isHeader reports whether text, a line without the spaces around it, is
// a "---" document header or a directive.
func isHeader(text string) bool {
	return text == "---" || strings.HasPrefix(text, "--- ") || strings.HasPrefix(text, "%")
}

// isEndMarker reports whether text, a line without the spaces around it,
// is a "..." document end marker.
func isEndMarker(text string) bool {
	return text == "..." || strings.HasPrefix(text, "... ")
}

// count returns the number of entries or items of the container of p.
func (d *Doc) count(p *place) int {
	if p.seq != nil {
		return len(p.seq.Values)
	}

	entries, _ := astnode.Entries(p.container)

	return len(entries)
}

// indicatorEnd returns the offset past the ":" or the "-" of p.
func (d *Doc) indicatorEnd(p *place) int {
	switch {
	case p.entry != nil:
		if end, _, ok := d.colonEnd(p.entry); ok {
			return end
		}

	case p.seq != nil && p.index >= 0 && p.index < len(p.seq.Entries) && p.seq.Entries[p.index].Start != nil:
		if s, ok := d.tokenSpan(p.seq.Entries[p.index].Start); ok {
			return s.end
		}
	}

	return 0
}

// afterProps returns the offset past the ":" or the "-" of p and past the
// anchor and the tag on its line, where an empty collection goes.
func (d *Doc) afterProps(p *place) int {
	at := d.indicatorEnd(p)
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

// propsSpan returns the bytes of the anchor or the tag that node writes,
// including the name of an anchor.
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
// comma after it, or the comma before it for the last one. A mapping that
// the user wrote without braces in a flow sequence, as in "[a: 1]", holds
// one entry. Without that entry, the item becomes "{}", as a mapping with
// braces keeps its braces.
func (d *Doc) removeFlow(p *place) error {
	start, ok := d.startOf(p)
	if !ok {
		return nil
	}

	end := d.valueEnd(p)

	if p.entry != nil && isBareMapping(p.container) {
		holder, ok := d.holderOf(p.container)
		if !ok {
			return errors.New("no item holds the mapping")
		}

		return d.splice(start, end, "{}", written("{}", d.addNote(d.emptiedNote(holder))))
	}

	cuts, _ := d.flowCuts(p, start, end)

	return d.apply(cuts...)
}

// flowCuts returns the edits that remove p, an entry or an item of a flow
// collection whose text is the bytes [start, end), with its comma. That is
// the comma after it, or the comma before it for the last one. A comment
// can come between the text and the comma. When the comma comes before p,
// a comment between that comma and p belongs to the entry or the item
// before p. That comment stays, so the comma takes an edit of its own.
// The second result is the start of the comment that goes with p, or -1
// for none.
func (d *Doc) flowCuts(p *place, start, end int) ([]edit, int) {
	itemEnd := end

	if i := d.skipBlank(end, len(d.cur)); i < len(d.cur) && d.cur[i] == ',' {
		end = i + 1
		for end < len(d.cur) && (d.cur[end] == ' ' || d.cur[end] == '\t') {
			end++
		}

		cut := d.flowCut(start, end)

		return []edit{cut}, d.commentIn(itemEnd, cut.end)
	}

	comma, ok := d.commaBefore(p, start)
	if ok && !strings.Contains(d.cur[comma:start], "#") {
		cut := d.flowCut(comma, end)

		return []edit{cut}, d.commentIn(itemEnd, cut.end)
	}

	cut := d.flowCut(start, end)
	cuts := []edit{cut}

	if ok {
		cuts = append(cuts, d.cutBefore(comma, comma+1))
	}

	return cuts, d.commentIn(itemEnd, cut.end)
}

// commaBefore returns the offset of the comma before p, an entry or an
// item of a flow collection that starts at start. That comma follows the
// entry or the item before p. It reports false for the first entry or
// item, and when no comma comes between the two.
func (d *Doc) commaBefore(p *place, start int) (int, bool) {
	var prev ast.Node

	switch {
	case p.seq != nil:
		if p.index > 0 && p.index <= len(p.seq.Values) {
			prev = p.seq.Values[p.index-1]
		}

	case p.entry != nil:
		entries, _ := astnode.Entries(p.container)
		if i := slices.Index(entries, p.entry); i > 0 {
			prev = entries[i-1]
		}
	}

	if prev == nil {
		return 0, false
	}

	s, ok := d.nodeSpan(prev)
	if !ok {
		return 0, false
	}

	i := d.skipBlank(s.end, start)

	return i, i < start && d.cur[i] == ','
}

// commentIn returns the start of the comment that follows off on its line,
// past spaces and a comma, when that comment starts before end. It returns
// -1 when no such comment does.
func (d *Doc) commentIn(off, end int) int {
	end = min(end, d.lineEnd(d.lineOf(off)))

	for i := off; i < end; i++ {
		switch d.cur[i] {
		case ' ', '\t', ',':
		case '#':
			return i
		default:
			return -1
		}
	}

	return -1
}

// flowCut returns the edit that a removal from a flow collection makes for
// the bytes [start, end) of an entry or an item with its comma. A comment
// that ends the last line of those bytes goes with them when no other text
// stays on that line. A comma that starts the line before those bytes
// separates them from the entry or the item before, on a line above, so it
// does not count as other text there. When the lines of those bytes then
// hold no other text, the removal deletes the lines, as [Doc.lineCut] does
// for the lines of a block collection. Otherwise, a line that the removal
// ends does not end with spaces. A comment that stays after other text
// keeps a space before it, as [Doc.cutBefore] keeps it.
func (d *Doc) flowCut(start, end int) edit {
	first, last := d.lineOf(start), d.lineOf(end)
	lineEnd := d.lineEnd(last)
	before, after := d.cur[d.lineStart(first):start], d.cur[end:lineEnd]

	rest := after
	if lead := strings.Trim(before, " \t"); first == last && lead != "," {
		rest = before + after
	}

	if strings.HasPrefix(strings.TrimLeft(rest, " \t"), "#") {
		end, after = lineEnd, ""
	}

	switch {
	case strings.Trim(after, " \t") != "":
		return d.cutBefore(start, end)

	case strings.Trim(before, " \t") != "":
		return edit{start: d.trimEnd(d.lineStart(first), start), end: lineEnd}
	}

	start, end = d.lineCut(region{start: d.lineStart(first), end: d.lineStart(last + 1), first: first, last: last})

	return edit{start: start, end: end}
}

// cutBefore returns the edit that deletes the bytes [start, end) of a flow
// collection. A "#" starts a comment only after a space, a tab, or the
// start of its line. So when a "#" follows those bytes and other text
// comes right before them, a space stays in their place. It is the last
// of the bytes when they end in one. Without it, the comment becomes part
// of a plain scalar or an alias before it.
func (d *Doc) cutBefore(start, end int) edit {
	cut := edit{start: start, end: end}

	if end >= len(d.cur) || d.cur[end] != '#' || start == 0 || strings.ContainsRune(" \t\r\n", rune(d.cur[start-1])) {
		return cut
	}

	if text := d.cur[start:end]; strings.HasSuffix(text, " ") || strings.HasSuffix(text, "\t") {
		cut.end--

		return cut
	}

	cut.text, cut.origins = " ", []int{d.origin[end-1]}

	return cut
}

// bug returns the problem of an edit whose text the engine cannot
// parse, which shows a bug in the engine.
func (d *Doc) bug(h *Node, err error) error {
	at, err := d.refusedCall(err)
	if at != nil {
		bound := d.user.Bind(d.engineProblem(err, at...))
		d.record(bound)

		return bound //nolint:wrapcheck // The binding is the problem of the edit.
	}

	bound := d.engineAt(h.path, err)

	// No path selects an entry that a later entry of its key hides, so a
	// problem below such an entry binds at the deepest value on the path
	// of h that the text holds below it, as [Node.bind] binds an error.
	for p, ok := h.path, true; ok && len(through(h.hidden, h.path, p)) > 0; p, ok = p.Parent() {
		at := &Node{d: d, path: p, hidden: through(h.hidden, h.path, p)}

		v, err2 := d.nodePlace(at)
		if err2 == nil && v.exists {
			s := d.placeRange(at, v)
			bound = d.user.Bind(d.engineProblem(err, niceyaml.AtPath(s.path), niceyaml.AtRange(s.at),
				niceyaml.WithDetails(d.callDetails(s)...)))

			break
		}
	}

	d.record(bound)

	return bound //nolint:wrapcheck // The binding is the problem of the edit.
}

// refusedCall returns err, a problem of the call running, as [Doc.bug]
// binds it. When go-yaml's parser refuses the text that the call wrote, or
// panics on it, as [Doc.parse] gives such a problem, the message names the
// edit and the call first, as the messages of the other problems of a call
// do. A refusal binds where the parser refuses the text, under the path of
// its site, and refusedCall returns the options of that place first. It
// returns nil options for any other place.
func (d *Doc) refusedCall(err error) ([]niceyaml.ErrorOption, error) {
	if d.callNamed == "" || !errors.Is(err, errParserRefuses) && !errors.Is(err, errParserPanicked) {
		return nil, err
	}

	b, bound := errors.AsType[*niceyaml.SourceError](err)
	rng, ranged := b.Range()

	if !bound || !ranged {
		return nil, fmt.Errorf("%s %s, and %w", d.subject(), d.callNamed, err)
	}

	// The problem takes the place of the refusal, so it holds the message of
	// the refusal without its place.
	refused := &callRefusalError{
		msg: fmt.Sprintf("%s %s, and %s", d.subject(), d.callNamed,
			strings.Replace(err.Error(), b.Error(), b.Unwrap().Error(), 1)),
	}

	if m, ok := errors.AsType[*misreadError](err); ok {
		refused.fix = m.fix
	} else if k, ok := errors.AsType[*keyRefusalError](err); ok {
		refused.fix = k.fix
	}

	// A refusal at a byte that an edit created binds at the site of that
	// byte, whose path the binding holds.
	path, ok := b.Path()
	if !ok {
		path, _ = d.userSitePath(rng.Start, false)
	}

	s := site{path: path, at: rng}

	if p, ok := errors.AsType[*placedError](err); ok {
		s.writer = p.at.writerAt(rng)
	}

	at := []niceyaml.ErrorOption{
		niceyaml.AtPath(path), niceyaml.AtRange(rng), niceyaml.WithDetails(d.callDetails(s)...),
	}

	return at, refused
}

// callRefusalError is the refusal of go-yaml's parser of the text that the call
// running wrote, as [Doc.refusedCall] gives it, with what the user can
// write so that the parser takes the text, or "".
type callRefusalError struct {
	msg, fix string
}

// Error returns the message of the refusal.
func (e *callRefusalError) Error() string {
	return e.msg
}

// keyRefusalError is the refusal of go-yaml's parser of the text after a rename,
// which the parser took before it. The parser thus refuses the key that the
// rename writes, and the fix asks for another key.
type keyRefusalError struct {
	err error
	fix string
}

// Error returns the message of the refusal.
func (e *keyRefusalError) Error() string {
	return e.err.Error()
}

// Unwrap returns the refusal.
func (e *keyRefusalError) Unwrap() error {
	return e.err
}

// engineAt returns err as the problem of a bug in the engine, bound at the
// place of path in the current text, as an error at path binds there. The
// root binds at the site that [Doc.rootSite] gives. When the current text
// does not hold path, the problem binds at path in the text that the call
// running found, which holds the place of the call. A path that a call
// creates is in neither text, so the problem binds at the site that
// [Doc.tokenlessSite] gives, under the path of that site.
func (d *Doc) engineAt(path paths.Path, err error) error {
	if bound, ok := d.engineIn(path, err); ok {
		return bound
	}

	if d.calling {
		now := d.save()
		d.show(d.callStart)

		bound, ok := d.engineIn(path, err)

		d.show(now)

		if ok {
			return bound
		}
	}

	if s, ok := d.tokenlessSite(path); ok && !path.IsRoot() {
		return d.bindSite(s, func(opts ...niceyaml.ErrorOption) error { return d.engineProblem(err, opts...) })
	}

	return d.bindOwn(d.engineProblem(err, niceyaml.AtPath(path)))
}

// engineIn returns err as the problem of a bug in the engine, bound at the
// place of path in the current text. It reports false when the current
// text does not hold path, as the places of the edit resolve it. No such
// place is a `<<` merge key, though the root package resolves a path
// through one.
func (d *Doc) engineIn(path paths.Path, err error) (error, bool) {
	opts, ok := d.engineOpts(path)
	if !ok {
		return nil, false
	}

	return d.bindOwn(d.engineProblem(err, opts...)), true
}

// engineOpts returns the options that bind a problem at the place of path in
// the current text, as [Doc.engineIn] binds it. It reports false when the
// current text does not hold path.
func (d *Doc) engineOpts(path paths.Path) ([]niceyaml.ErrorOption, bool) {
	opts := []niceyaml.ErrorOption{niceyaml.AtPath(path)}

	if path.IsRoot() {
		rng, ok := rootRange(d.node)

		return append(opts, niceyaml.AtRange(rng)), ok
	}

	p, err := d.resolve(path)

	held := err == nil && p.exists
	if !held {
		return nil, false
	}

	// An empty value has no range, so the problem binds past the ":" of its
	// key or past its "-", as an error at the value binds.
	rs, err := d.node.Ranges(path)

	held = err == nil
	if !held {
		return nil, false
	}

	if len(rs) > 0 {
		opts = append(opts, niceyaml.AtRange(rs[0]))
	}

	return opts, true
}

// rootRange returns the range of doc where an error at the root binds: its
// first key or item, as [niceyaml.Node.Ranges] gives it. The first item of
// a block sequence can be empty, without text of its own, so an error
// there binds at the "-" of that item. A first item that holds a
// collection binds at the first token of the collection under its anchors
// and tags. That token is the first "-" of a block sequence, or the "{" or
// the "[" of a flow collection. Ranges gives the "{" or the "[" of a flow root, so an
// error there binds past it, at the key of the first entry or at the first
// item. Each of those comes from the syntax tree, so a later entry that
// repeats the first key does not take its place.
func rootRange(doc *niceyaml.Node) (position.Range, bool) {
	var first *token.Token

	switch x := astnode.Content(doc.AST()).(type) {
	case *ast.MappingNode:
		if x.IsFlowStyle && len(x.Values) > 0 {
			first = astnode.KeyToken(x.Values[0])
		}

	case *ast.SequenceNode:
		if len(x.Values) == 0 {
			break
		}

		if x.IsFlowStyle {
			first = astnode.PathToken(x.Values[0], astnode.Parent{Sequence: x})

			break
		}

		switch item := astnode.Content(x.Values[0]).(type) {
		case *ast.SequenceNode:
			first = item.Start
			if !item.IsFlowStyle && len(item.Entries) > 0 {
				first = item.Entries[0].Start
			}

		case *ast.MappingNode:
			if item.IsFlowStyle {
				first = item.Start
			}
		}
	}

	if first != nil && first.Position != nil {
		if rs := doc.Source().Lines().ContentRanges(first); len(rs) > 0 {
			return rs[0], true
		}
	}

	rs, err := doc.Ranges(paths.Doc())
	if err == nil && len(rs) > 0 {
		return rs[0], true
	}

	seq, ok := astnode.Content(doc.AST()).(*ast.SequenceNode)
	if !ok || seq.IsFlowStyle || len(seq.Entries) == 0 {
		return position.Range{}, false
	}

	dash := seq.Entries[0].Start
	if dash == nil || dash.Position == nil {
		return position.Range{}, false
	}

	at := position.NewFromToken(dash)

	return position.NewRange(at, position.New(at.Line, at.Col+1)), true
}

// engineProblem returns err as the problem of a bug in the engine, at the
// place that opts give. The problem has the message of err and matches
// [ErrEngine]. Its detail asks the user to report the problem, unless err
// holds a [*misreadError] of the user's text, a [*callRefusalError] with a
// fix, or a [*marshalerError], whose fix the detail gives. The details that
// opts give come after it.
func (d *Doc) engineProblem(err error, opts ...niceyaml.ErrorOption) error {
	// A misread of go-yaml in the user's text is no bug, and the user can
	// rewrite the text as the misread tells.
	detail := fmt.Errorf("report this to the authors of %s", d.appName())

	m, misread := errors.AsType[*misreadError](err)
	r, refused := errors.AsType[*callRefusalError](err)
	me, marshaled := errors.AsType[*marshalerError](err)

	switch {
	case misread:
		detail = errors.New(m.fix)
	case refused && r.fix != "":
		detail = errors.New(r.fix)
	case marshaled:
		detail = errors.New(me.fix)
	}

	opts = append([]niceyaml.ErrorOption{niceyaml.WithDetails(detail)}, opts...)

	return niceyaml.Unchecked(&engineError{err: err}, opts...)
}

// engineMisread returns m, a misread of go-yaml in the user's text, as the
// problem of New at the place that opts give. The problem has the message
// of m, and its detail gives the fix of m. It matches [ErrMisread] and
// [ErrEngine].
func (d *Doc) engineMisread(m *misreadError, opts ...niceyaml.ErrorOption) error {
	opts = append(opts, niceyaml.WithDetails(errors.New(m.fix)))

	return niceyaml.Unchecked(&engineError{err: m, misread: true}, opts...)
}

// engineError is the problem of a bug in the engine, which matches
// [ErrEngine] and has the message of the error behind it. So a report
// that lists the problems of an error reads one problem, which names
// go-yaml where go-yaml is at fault. A misread of go-yaml in the user's
// text, as misread tells, matches [ErrMisread] too.
type engineError struct {
	err     error
	misread bool
}

// Error returns the message of the error behind e.
func (e *engineError) Error() string {
	return e.err.Error()
}

// Is reports whether target is [ErrEngine], or [ErrMisread] for a misread
// of go-yaml in the user's text.
func (e *engineError) Is(target error) bool {
	return target == ErrEngine || e.misread && target == ErrMisread
}

// Unwrap returns the error behind e.
func (e *engineError) Unwrap() error {
	return e.err
}

// scalarToken returns the token of the scalar of node, or nil for a node
// that does not hold a scalar.
func scalarToken(node ast.Node) *token.Token {
	c := astnode.Content(node)
	if !astnode.IsScalar(c) || isImplicitNull(c) {
		return nil
	}

	return c.GetToken()
}
