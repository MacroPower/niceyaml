package edit

import (
	"errors"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Handle names one place of a [Doc]: the root, an entry of a mapping, or
// an item of a sequence. Each edit of the Doc keeps the path of the handle
// up to date, so a handle on a value that an edit renamed or moved names
// its new place. A handle whose place an edit took away is gone.
//
// Receive instances from [Doc.Matches] or [Doc.Handle].
type Handle struct {
	d *Doc
	// The error of the path that At took, which each method that edits or
	// decodes returns.
	err  error
	path paths.Path
	gone bool
}

// Handle returns a handle on the place at path, a `$` path of names and
// indexes, which might hold no value.
func (d *Doc) Handle(path paths.Path) *Handle {
	h := &Handle{d: d, path: path}
	d.handles = append(d.handles, h)

	return h
}

// Matches returns a handle on each place that path selects, in document
// order. Matches that reach one place through an alias or a merge key share
// the handle of the first. The type fields are no match.
func (d *Doc) Matches(path paths.Path) ([]*Handle, error) {
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
		if d.isTypeField(m.Path) {
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

	handles := make([]*Handle, 0, len(list))
	for _, f := range list {
		handles = append(handles, d.Handle(f.path))
	}

	return handles, nil
}

// isTypeField reports whether path names a type field.
func (d *Doc) isTypeField(path paths.Path) bool {
	return slices.ContainsFunc(d.opts.TypeFields, path.Equal)
}

// holdsTypeField reports whether the value at path holds a type field, or
// is one.
func (d *Doc) holdsTypeField(path paths.Path) bool {
	return slices.ContainsFunc(d.opts.TypeFields, func(tf paths.Path) bool {
		_, ok := tf.CutPrefix(path)

		return ok
	})
}

// Path returns the `$` path of the place as the document stands now.
func (h *Handle) Path() paths.Path {
	return h.path
}

// Key returns the key of the entry, or "" for an item, the root, or a place
// that holds no value.
func (h *Handle) Key() string {
	last, ok := h.path.Last()
	if !ok || last.Kind != paths.SelectorChild {
		return ""
	}

	return last.Name
}

// place returns the place of h, or the error that each method that edits
// or decodes returns for a handle that cannot take the call.
func (h *Handle) place() (*place, error) {
	switch {
	case h.err != nil:
		return nil, h.err
	case h.gone:
		return nil, h.d.fail(h, "%s cannot edit this value, because an earlier edit took it away", h.d.label)
	}

	p, err := h.d.resolve(h.path)
	if err != nil {
		return nil, h.d.fail(h, "%s", err.Error())
	}

	return p, nil
}

// Gone reports whether an edit took the place of h away: a removal of the
// place or of a value above it, or a replacement of a value above it.
func (h *Handle) Gone() bool {
	return h.gone
}

// Exists reports whether the place holds a value.
func (h *Handle) Exists() bool {
	if h.err != nil || h.gone {
		return false
	}

	p, err := h.d.resolve(h.path)

	return err == nil && p.exists
}

// Kind returns the kind of the value, or [niceyaml.NodeNone] for a place
// that holds none.
func (h *Handle) Kind() niceyaml.NodeKind {
	n, err := h.node()
	if err != nil {
		return niceyaml.NodeNone
	}

	return n.Kind()
}

// Text returns the text of a scalar as the document writes it, without
// its quotes, or "" for a mapping, a sequence, or a place that holds no
// value.
func (h *Handle) Text() string {
	if h.err != nil || h.gone {
		return ""
	}

	p, err := h.d.resolve(h.path)
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

// Node returns the value of the place as a [*niceyaml.Node] of the current
// text, for a decode. It returns the error of a handle that cannot decode.
func (h *Handle) Node() (*niceyaml.Node, error) {
	p, err := h.place()
	if err != nil {
		return nil, err
	}

	if !p.exists {
		return nil, h.d.fail(h, "%s cannot decode %s, which holds no value", h.d.label, h.path)
	}

	return h.node()
}

// node returns the value of the place as a [*niceyaml.Node].
func (h *Handle) node() (*niceyaml.Node, error) {
	if h.err != nil || h.gone {
		return nil, errors.New("no value")
	}

	return h.d.node.At(h.path) //nolint:wrapcheck // The Node binds the error.
}

// At returns a handle on the place at path, which might hold no value. A
// `$` path reads from the root, and an `@` path from h. A path that selects
// more than one value, holds a `~`, or goes through an alias that does not
// resolve fails the call. Each method of the handle that edits or decodes
// then returns the same error.
func (h *Handle) At(path paths.Path) *Handle {
	full := path
	if !path.IsAbsolute() {
		full = h.path.Join(path)
	}

	n := h.d.Handle(full)

	for sel := range full.Selectors() {
		switch sel.Kind {
		case paths.SelectorChild, paths.SelectorIndex:
		case paths.SelectorKey:
			n.err = h.d.fail(h, "%s reads %s, which selects a key", h.d.label, full)
		default:
			n.err = h.d.fail(h, "%s reads %s, which selects more than one value", h.d.label, full)
		}

		if n.err != nil {
			return n
		}
	}

	_, err := h.d.resolve(full)
	if err != nil {
		n.err = h.d.fail(h, "%s reads %s, which %s", h.d.label, full, err.Error())
	}

	return n
}

// Bind binds err, an error that a step returned, in the user's text. An
// error without a location binds at the value of h, and an `@` path in it
// reads from h.
func (h *Handle) Bind(err error) error {
	if err == nil {
		return nil
	}

	n, nerr := h.d.node.At(h.path)
	if nerr == nil {
		return n.Bind(err) //nolint:wrapcheck // The binding is the error of the step.
	}

	if p, ok := h.path.Parent(); ok {
		n, nerr := h.d.node.At(p)
		if nerr == nil {
			return n.Bind(err) //nolint:wrapcheck // The binding is the error of the step.
		}
	}

	return h.d.node.Bind(err) //nolint:wrapcheck // The binding is the error of the step.
}

// fail returns the problem that format describes, bound at the place of
// h, and records it as the problem of the call.
func (d *Doc) fail(h *Handle, format string, args ...any) error {
	err := h.Bind(niceyaml.NewError(fmt.Sprintf(format, args...)))

	d.record(err)

	return err
}

// record keeps err as the problem of the call, unless the call has one.
func (d *Doc) record(err error) {
	if d.callErr == nil {
		d.callErr = err
	}
}

// moved updates the handles after the value at from moved to the path to.
func (d *Doc) moved(from, to paths.Path) {
	for _, h := range d.handles {
		if h.gone {
			continue
		}

		if rest, ok := h.path.CutPrefix(from); ok {
			h.path = to.Join(rest)
		}
	}
}

// took marks each handle below path as gone, and the handle at path too
// when self is set.
func (d *Doc) took(path paths.Path, self bool) {
	for _, h := range d.handles {
		rest, ok := h.path.CutPrefix(path)
		if !ok {
			continue
		}

		if rest.Len() > 0 || self {
			h.gone = true
		}
	}
}

// shifted updates the handles below the items of the sequence at seq after
// the item at index left it, so each later item takes the index before it.
func (d *Doc) shifted(seq paths.Path, index int) {
	for _, h := range d.handles {
		if h.gone {
			continue
		}

		rest, ok := h.path.CutPrefix(seq)
		if !ok || rest.Len() == 0 {
			continue
		}

		var (
			sels  []paths.Selector
			first = true
			moved bool
		)

		for sel := range rest.Selectors() {
			if first && sel.Kind == paths.SelectorIndex && sel.Index > index {
				sel.Index--
				moved = true
			}

			first = false

			sels = append(sels, sel)
		}

		if moved {
			h.path = appendSelectors(seq, sels)
		}
	}
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
