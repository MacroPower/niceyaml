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

// SetKey renames the key of the entry. The value and its comments stay in
// place, and the key keeps its quotes. When the entry already has the key,
// as the decode reads it, SetKey leaves the text as it is. SetKey fails on
// a sequence item, because an item does not have a key. It also fails on a
// key that the mapping holds already.
//
//nolint:contextcheck // A rename writes text and reads no state that a context carries.
func (n *Node) SetKey(key string) error {
	d := n.d

	p, err := n.place()
	if err != nil || !p.exists {
		return err
	}

	why := d.protection(n.path)

	switch {
	case p.root || p.entry == nil:
		return d.fail(n, "%s renames this item, but an item of a sequence has no key", d.subject())
	case why != "":
		return d.fail(n, "%s renames %q, which %s", d.subject(), n.Key(), why)
	}

	ks, tk, ok := d.keySpan(p)
	if !ok {
		return d.fail(n, "%s cannot find the key of %s", d.subject(), n.path)
	}

	text, err := renderKey(context.Background(), key, tk, p.flow)
	if err != nil {
		return d.fail(n, "%s cannot write the key %q: %v", d.subject(), key, err)
	}

	name := keyName(text)
	if keyName(d.cur[ks.start:ks.end]) == name {
		return nil
	}

	err = d.editOnce(n, p)
	if err != nil {
		return err
	}

	d.recordEdit(n, p, fmt.Sprintf("renames %q to %q", n.Key(), key))

	parent, _ := n.path.Parent()

	if other := d.sibling(p, name); other != nil {
		otherKey, _ := d.node.Resolver().KeyName(other.Key)
		detail := d.node.Bind(niceyaml.NewError(fmt.Sprintf("%q is defined here", otherKey),
			niceyaml.AtPath(parent.Child(otherKey).Key())))

		err := n.bind(niceyaml.NewError(
			fmt.Sprintf("%s renames %q to %q, which already exists", d.subject(), n.Key(), key),
			niceyaml.AtPath(n.path.Key()),
			niceyaml.WithDetails(detail, errors.New("remove one of the two")),
		))
		d.record(err)

		return err
	}

	d.invalidate(n.path)

	// PathAt gives a path that ends in `~` for a key, and so does the note
	// of a key that an earlier call renamed.
	kept := d.keptNote(ks.start, ks.end)
	if last, ok := kept.path.Last(); kept.hasPath && (!ok || last.Kind != paths.SelectorKey) {
		kept.path = kept.path.Key()
	}

	err = d.splice(ks.start, ks.end, text, written(text, d.addNote(kept)))
	if err != nil {
		return d.bug(n, err)
	}

	d.moved(n.path, parent.Child(key))

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
// [start, end). The note holds the place in the user's text of the first
// byte, or the note of that byte when an earlier edit wrote it.
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
// lists the removal of a value of the user's text. When blank lines
// surround the lines that Remove deletes, Remove deletes one of them too.
// A block collection that it empties becomes "{}" or "[]". A place that
// does not hold a value stays as it is.
func (n *Node) Remove(opts ...RemoveOption) error {
	d := n.d

	var cfg removeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	p, err := n.place()
	if err != nil || !p.exists {
		return err
	}

	why := d.protection(n.path)

	switch {
	case p.root:
		return d.fail(n, "%s removes the root of the document", d.subject())
	case why != "":
		return d.fail(n, "%s removes %s, which %s", d.subject(), n.path, why)
	}

	err = d.editOnce(n, p)
	if err != nil {
		return err
	}

	if p.entry != nil {
		d.recordEdit(n, p, fmt.Sprintf("removes %q", n.Key()))
	} else {
		d.recordEdit(n, p, "removes this item")
	}

	d.recordRemoval(n, p, cfg.reason)
	d.invalidate(n.path)

	anchors := d.anchorsIn(p.value)

	if p.flow {
		err = d.removeFlow(p)
	} else {
		err = d.removeBlock(n, p)
	}

	if err != nil {
		return d.bug(n, err)
	}

	d.took(n.path, true)

	if p.seq != nil {
		parent, _ := n.path.Parent()
		d.shifted(parent, p.index)
	}

	return d.keepAnchors(n, anchors)
}

// recordRemoval records the removal of the value of p through h when the
// value holds text of the user's file.
func (d *Doc) recordRemoval(h *Node, p *place, reason string) {
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
func (d *Doc) removeBlock(h *Node, p *place) error {
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
func (d *Doc) bug(h *Node, err error) error {
	app := d.cfg.app
	if app == "" {
		app = "this program"
	}

	bound := h.bind(niceyaml.Unchecked(&engineError{
		msg: app + " cannot edit this value without changing it",
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
// that does not hold a scalar.
func scalarToken(node ast.Node) *token.Token {
	c := astnode.Content(node)
	if !astnode.IsScalar(c) || isImplicitNull(c) {
		return nil
	}

	return c.GetToken()
}
