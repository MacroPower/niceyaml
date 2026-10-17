package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Node is a handle on one place of a [Doc]: the root, an entry of a
// mapping, or an item of a sequence. It reads the value there and edits
// the document. Each edit keeps the path of the node up to date. A node on
// a value that an edit renamed or moved thus names its new place. Each
// edit of a node fails after an edit removed its place, either directly or
// by replacing a value above it.
//
// When a method cannot make its edit, it returns an error that names the
// problem. [Doc.Apply] then fails with that error, even when the [Func]
// continues and returns nil. A node takes edits until the Apply that gave
// it returns. After that, each method that edits or decodes returns an
// error. Exists, Kind, and Text then answer as they do for a place that
// does not hold a value.
//
// Receive instances as the argument of a [Func], or from [Node.At].
type Node struct {
	d *Doc
	// The error of the path that At took, which each method that edits or
	// decodes returns.
	err  error
	path paths.Path
	// Whether an edit removed the place of the node.
	gone bool
	// Whether the Apply that gave the node returned.
	ended bool
}

// newNode returns a node on the place at path, a `$` path of names and
// indexes, which might not hold a value.
func (d *Doc) newNode(path paths.Path) *Node {
	n := &Node{d: d, path: path}
	d.nodes = append(d.nodes, n)

	return n
}

// matches returns a node on each place that path selects, in document
// order. Matches that reach one place through an alias or a merge key share
// the node of the first. It skips protected values and the values inside
// them.
func (d *Doc) matches(path paths.Path) ([]*Node, error) {
	matches, err := d.node.Resolver().Matches(path)
	if err != nil {
		return nil, d.node.Bind(err) //nolint:wrapcheck // The binding is the error of the document.
	}

	type found struct {
		path  paths.Path
		start int
	}

	var (
		list []found
		seen = make(map[any]bool)
	)

	for _, m := range matches {
		if d.isProtected(m.Path) {
			continue
		}

		key := any(m.Node)
		if astnode.IsNil(m.Node) {
			key = m.Path.String()
		}

		if seen[key] {
			continue
		}

		seen[key] = true

		start := len(d.cur)
		if s, ok := d.nodeSpan(m.Node); ok {
			start = s.start
		}

		list = append(list, found{path: m.Path, start: start})
	}

	slices.SortStableFunc(list, func(a, b found) int { return a.start - b.start })

	nodes := make([]*Node, 0, len(list))
	for _, f := range list {
		nodes = append(nodes, d.newNode(f.path))
	}

	return nodes, nil
}

// isProtected reports whether path names a protected value or a value
// inside one.
func (d *Doc) isProtected(path paths.Path) bool {
	return slices.ContainsFunc(d.cfg.protected, func(p paths.Path) bool {
		_, ok := path.CutPrefix(p)

		return ok
	})
}

// protection returns the reason that edits cannot change the value at
// path, in the words that a message uses after "which". It returns "" for
// a value that edits can change. Edits cannot change a protected value, a
// value inside one, or a value that holds one.
func (d *Doc) protection(path paths.Path) string {
	switch {
	case slices.ContainsFunc(d.cfg.protected, path.Equal):
		return "is a protected value"
	case d.isProtected(path):
		return "lies inside a protected value"
	}

	holds := slices.ContainsFunc(d.cfg.protected, func(p paths.Path) bool {
		_, ok := p.CutPrefix(path)

		return ok
	})
	if holds {
		return "holds a protected value"
	}

	return ""
}

// Path returns the `$` path of the place as the document stands now.
func (n *Node) Path() paths.Path {
	return n.path
}

// Key returns the key of the entry, or "" for a sequence item or the
// root.
func (n *Node) Key() string {
	last, ok := n.path.Last()
	if !ok || last.Kind != paths.SelectorChild {
		return ""
	}

	return last.Name
}

// place returns the place of n, or the error that each method that edits
// or decodes returns for a node that cannot take the call.
func (n *Node) place() (*place, error) {
	switch {
	case n.ended:
		return nil, errEnded
	case n.err != nil:
		return nil, n.err
	case n.gone:
		return nil, n.d.fail(n, "%s cannot change this value, because an earlier edit took it away", n.d.subject())
	}

	p, err := n.d.resolve(n.path)
	if err != nil {
		return nil, n.d.fail(n, "%s", err.Error())
	}

	return p, nil
}

// readable reports whether a method that reads can read the place of n.
func (n *Node) readable() bool {
	return !n.ended && n.err == nil && !n.gone
}

// Exists reports whether the place holds a value.
func (n *Node) Exists() bool {
	if !n.readable() {
		return false
	}

	p, err := n.d.resolve(n.path)

	return err == nil && p.exists
}

// Kind returns the kind of the value, or [niceyaml.NodeNone] when the
// place does not hold a value.
func (n *Node) Kind() niceyaml.NodeKind {
	v, err := n.value()
	if err != nil {
		return niceyaml.NodeNone
	}

	return v.Kind()
}

// Text returns the text of a scalar as the document writes it, without its
// quotes, or "" for a mapping, a sequence, or a place that does not hold a
// value.
func (n *Node) Text() string {
	if !n.readable() {
		return ""
	}

	p, err := n.d.resolve(n.path)
	if err != nil || !p.exists {
		return ""
	}

	content := astnode.Content(p.value)
	if !astnode.IsScalar(content) || isImplicitNull(content) {
		return ""
	}

	if tk := content.GetToken(); tk != nil {
		return tk.Value
	}

	return ""
}

// Decode decodes the value as [niceyaml.Node.Decode] does, with opts and
// the settings of the [niceyaml.Source] of the document, such as its
// reference documents and its unmarshalers. The unmarshalers of
// [WithUnmarshalers] apply ahead of those of the Source. A [Func] should
// pass the ctx it got, so the warnings of the decode reach the
// [niceyaml.Warnings] on the context of [Doc.Apply]. Decode fails on a
// place that does not hold a value.
func (n *Node) Decode[T any](ctx context.Context, opts ...niceyaml.DecodeOption) (T, error) {
	var zero T

	doc, err := n.doc()
	if err != nil {
		return zero, err
	}

	return doc.Decode[T](ctx, opts...)
}

// doc returns the value of the place as a [*niceyaml.Node] of the current
// text, for a decode. It returns the error of a node that cannot decode.
func (n *Node) doc() (*niceyaml.Node, error) {
	p, err := n.place()
	if err != nil {
		return nil, err
	}

	if !p.exists {
		return nil, n.d.fail(n, "%s cannot decode %s, which holds no value", n.d.subject(), n.path)
	}

	return n.value()
}

// value returns the value of the place as a [*niceyaml.Node].
func (n *Node) value() (*niceyaml.Node, error) {
	if !n.readable() {
		return nil, errors.New("no value")
	}

	return n.d.node.At(n.path) //nolint:wrapcheck // The Node binds the error.
}

// At returns a node on the place at path, which might not hold a value. A
// `$` path reads from the root, and an `@` path reads from n. Exists
// reports false when the place does not hold a value, and only Set changes
// such a place.
//
// At fails the Apply on a path that selects more than one value, holds a
// `~`, or goes through an alias that does not resolve in the text. An
// alias of a reference document is such an alias. Each method of its node
// that edits or decodes then returns the same error.
func (n *Node) At(path paths.Path) *Node {
	full := path
	if !path.IsAbsolute() {
		full = n.path.Join(path)
	}

	if n.ended {
		return &Node{d: n.d, path: full, ended: true}
	}

	at := n.d.newNode(full)

	for sel := range full.Selectors() {
		switch sel.Kind {
		case paths.SelectorChild, paths.SelectorIndex:
		case paths.SelectorKey:
			at.err = n.d.fail(n, "%s reads %s, which selects a key", n.d.subject(), full)
		default:
			at.err = n.d.fail(n, "%s reads %s, which selects more than one value", n.d.subject(), full)
		}

		if at.err != nil {
			return at
		}
	}

	_, err := n.d.resolve(full)
	if err != nil {
		at.err = n.d.fail(n, "%s reads %s, which %s", n.d.subject(), full, err.Error())
	}

	return at
}

// bind binds err, an error that a [Func] returned, in the user's text. An
// error without a location binds at the value of n, and an `@` path in it
// reads from n.
func (n *Node) bind(err error) error {
	if err == nil {
		return nil
	}

	v, verr := n.d.node.At(n.path)
	if verr == nil {
		return v.Bind(err) //nolint:wrapcheck // The binding is the error of the Func.
	}

	if p, ok := n.path.Parent(); ok {
		v, verr := n.d.node.At(p)
		if verr == nil {
			return v.Bind(err) //nolint:wrapcheck // The binding is the error of the Func.
		}
	}

	return n.d.node.Bind(err) //nolint:wrapcheck // The binding is the error of the Func.
}

// subject returns the subject of the edits of the Apply running, as
// messages name it: its label, or "the edit".
func (d *Doc) subject() string {
	if d.label == "" {
		return "the edit"
	}

	return d.label
}

// fail returns the problem that format describes, bound at the place of
// n, and records it as the problem of the call.
func (d *Doc) fail(n *Node, format string, args ...any) error {
	err := n.bind(niceyaml.NewError(fmt.Sprintf(format, args...)))

	d.record(err)

	return err
}

// record keeps err as the problem of the call, unless the call already has
// one.
func (d *Doc) record(err error) {
	if d.callErr == nil {
		d.callErr = err
	}
}

// moved updates the nodes after the value at from moved to the path to.
func (d *Doc) moved(from, to paths.Path) {
	repoint(d.nodesAt(from), from, to)
}

// nodesAt returns the nodes at path and below it, except each node whose
// place an edit removed.
func (d *Doc) nodesAt(path paths.Path) []*Node {
	var out []*Node

	for _, n := range d.nodes {
		if _, ok := n.path.CutPrefix(path); ok && !n.gone {
			out = append(out, n)
		}
	}

	return out
}

// repoint moves each of nodes, a node at from or below it, to the same
// place below to.
func repoint(nodes []*Node, from, to paths.Path) {
	for _, n := range nodes {
		if rest, ok := n.path.CutPrefix(from); ok {
			n.path = to.Join(rest)
		}
	}
}

// took marks each node below path as gone, and the node at path too when
// self is true.
func (d *Doc) took(path paths.Path, self bool) {
	for _, n := range d.nodes {
		rest, ok := n.path.CutPrefix(path)
		if !ok {
			continue
		}

		if rest.Len() > 0 || self {
			n.gone = true
		}
	}
}

// shifted updates the nodes below the items of the sequence at seq after
// the item at index left it. Each later item takes the index before it.
func (d *Doc) shifted(seq paths.Path, index int) {
	for _, n := range d.nodes {
		if !n.gone {
			n.path = shiftedPath(n.path, seq, index)
		}
	}
}

// shiftedPath returns path after the item at index left the sequence at
// seq. A path below a later item takes the index before it, and any other
// path stays as it is.
func shiftedPath(path, seq paths.Path, index int) paths.Path {
	rest, ok := path.CutPrefix(seq)
	if !ok || rest.Len() == 0 {
		return path
	}

	var (
		sels  []paths.Selector
		moved bool
	)

	for sel := range rest.Selectors() {
		if len(sels) == 0 && sel.Kind == paths.SelectorIndex && sel.Index > index {
			sel.Index--
			moved = true
		}

		sels = append(sels, sel)
	}

	if !moved {
		return path
	}

	return appendSelectors(seq, sels)
}

// appendSelectors returns p with sels after it.
func appendSelectors(p paths.Path, sels []paths.Selector) paths.Path {
	for _, sel := range sels {
		switch sel.Kind {
		case paths.SelectorChild:
			p = p.Child(sel.Name)
		case paths.SelectorIndex:
			p = p.Index(sel.Index)
		case paths.SelectorKey:
			p = p.Key()
		case paths.SelectorChildAll:
			p = p.ChildAll()
		default:
			p = p.IndexAll()
		}
	}

	return p
}
