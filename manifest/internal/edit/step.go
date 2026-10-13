package edit

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// BeginStep starts the calls of a step of the version that messages name
// label, or the edits of the type fields of that version.
func (d *Doc) BeginStep(label string) {
	d.label = label
	d.written = nil
	d.stepEdits = nil
	d.merges = d.mergeViews(nil)
	d.lastEdit = nil
}

// EndCall ends one call of a step on the handle h, and takes err, the
// error that the step returned. It returns the problem that fails the
// document. EndCall binds err at h and returns it, or else returns the
// problem of a method that could not make its edit. Without either, it
// returns nil.
func (d *Doc) EndCall(h *Handle, err error) error {
	callErr := d.callErr
	d.callErr = nil

	if err != nil {
		return h.Bind(err)
	}

	return callErr
}

// Check checks the text once the calls of a step return. The text must
// decode, and each value that a call wrote must read back as written. A
// difference shows a bug in the engine, and the problem names the value.
func (d *Doc) Check(ctx context.Context) error {
	written := d.written
	d.written = nil

	err := d.checkMerges()
	if err != nil {
		return err
	}

	_, err = d.node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return d.bug(d.rootHandle(), err)
	}

	for _, w := range written {
		h := &Handle{d: d, path: w.path}

		n, err := d.node.At(w.path)
		if err != nil {
			continue
		}

		got, err := n.Decode[any](ctx, niceyaml.WithSelfValidation(false))
		if err != nil {
			return d.bug(h, err)
		}

		want, err := canonical(ctx, w.value)
		if err != nil {
			continue
		}

		if !reflect.DeepEqual(got, want) {
			return d.bug(h, fmt.Errorf("%s reads %v, not %v", w.path, got, want))
		}
	}

	return nil
}

// invalidate drops each value that an earlier call wrote and that an edit
// at path changes: a value at path, below it, or above it.
func (d *Doc) invalidate(path paths.Path) {
	d.written = slices.DeleteFunc(d.written, func(w writtenValue) bool {
		_, below := w.path.CutPrefix(path)
		_, above := path.CutPrefix(w.path)

		return below || above
	})
}

// rootHandle returns a handle on the root, which binds a problem of the
// whole text.
func (d *Doc) rootHandle() *Handle {
	return &Handle{d: d, path: paths.Doc()}
}

// canonical returns v as a decode into any reads the text that the encoder
// writes for it.
func canonical(ctx context.Context, v any) (any, error) {
	data, err := encoder.Marshal(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("encode the value: %w", err)
	}

	var out any

	err = yaml.Unmarshal(data, &out)
	if err != nil {
		return nil, fmt.Errorf("decode the value: %w", err)
	}

	return out, nil
}

// SetTypeField sets the type field at path to text, as the next version
// of the document declares it. A field that the document writes keeps the
// style of its scalar where text allows it, and a field that the document
// lacks goes first in its mapping, after the comment lines at the top of
// the document. A document whose values above path leave no mapping to
// hold the field, such as one whose root is a sequence, stays as it is, so
// the checks of the version report the value that the document holds.
func (d *Doc) SetTypeField(ctx context.Context, path paths.Path, text string) error {
	h := &Handle{d: d, path: path}

	p, err := d.resolve(path)
	if err != nil {
		return d.bug(h, err)
	}

	if p.exists {
		tk := scalarToken(p.value)
		if tk != nil && tk.Value == text {
			return nil
		}

		newText, err := typeFieldText(ctx, text, tk)
		if err != nil {
			return d.bug(h, err)
		}

		vs, ve := d.valueStart(p), d.valueEnd(p)
		n := d.keptNote(vs, max(vs+1, ve))
		origin := d.addNote(n)
		anchors := d.anchorsIn(p.value)

		if isImplicitNull(p.value) {
			newText = " " + newText
		}

		err = d.apply(edit{start: vs, end: ve, text: newText, origins: written(newText, origin)})
		if err != nil {
			return d.bug(h, err)
		}

		return d.keepAnchors(h, anchors)
	}

	return d.insertTypeField(ctx, h, p, path, text)
}

// holdsMapping reports whether the value at the first found selectors of
// path is a mapping, where the mappings of the rest of path can go.
func (d *Doc) holdsMapping(path paths.Path, found int) bool {
	var sels []paths.Selector

	for sel := range path.Selectors() {
		sels = append(sels, sel)
	}

	p, err := d.resolve(appendSelectors(paths.Doc(), sels[:min(found, len(sels))]))

	return err == nil && p.exists && isMapping(p.value)
}

// insertTypeField inserts the type field at path, which p resolved, first
// in the mapping that holds it.
func (d *Doc) insertTypeField(ctx context.Context, h *Handle, p *place, path paths.Path, text string) error {
	value, err := typeFieldText(ctx, text, nil)
	if err != nil {
		return d.bug(h, err)
	}

	parent, _ := path.Parent()

	holder, err := d.resolve(parent)
	if err != nil || !holder.exists || p.found < path.Len()-1 {
		if !d.holdsMapping(path, p.found) {
			return nil
		}

		// The mappings above the field go with it, as Set creates them.
		return h.set(ctx, text, true)
	}

	entries, ok := astnode.Entries(astnode.Content(holder.value))
	if !ok || isNull(holder.value) {
		return nil
	}

	if len(entries) == 0 {
		return h.set(ctx, text, true)
	}

	key, err := renderKey(ctx, fieldOf(path), nil, holder.flow || isFlow(astnode.Content(holder.value)))
	if err != nil {
		return d.bug(h, err)
	}

	first, err := d.resolve(parent.Child(keyOf(d, entries[0])))
	if err != nil {
		return d.bug(h, err)
	}

	origin := d.addNote(d.createdNote(holder))
	entry := key + ": " + value

	var e edit

	if isFlow(astnode.Content(holder.value)) {
		at, _ := d.startOf(first)
		entry += ", "
		e = edit{start: at, end: at, text: entry, origins: written(entry, origin)}
	} else {
		reg := d.blockRegion(first)
		col := d.keyColumn(first)
		brk := d.breakBefore(reg.first)
		entry = indentLines([]string{entry}, col, brk) + brk
		at := d.lineStart(reg.first)
		e = edit{start: at, end: at, text: entry, origins: written(entry, origin)}
	}

	err = d.apply(e)
	if err != nil {
		return d.bug(h, err)
	}

	return nil
}

// stepEdit is a place that a call of the step running edited.
type stepEdit struct {
	// The alias or the merge key that the call went through, as the text
	// writes it, or "" for a call that reached the place directly.
	via string
	// The path of the place, and its range in the user's text.
	path paths.Path
	at   position.Range
	// The range of via in the user's text.
	viaAt position.Range
}

// editOnce records an edit of p through h, and returns the problem of a
// place that an earlier call of the step edited too, where one of the
// calls reached it through an alias or a merge key. A step that reaches a
// shared value from several calls thus never edits it twice.
func (d *Doc) editOnce(h *Handle, p *place) error {
	start, ok := d.startOf(p)
	if !ok || start >= len(d.origin) {
		return nil
	}

	key := d.origin[start]
	cur := d.stepEdit(h, p)

	prev, seen := d.stepEdits[key]
	if !seen {
		if d.stepEdits == nil {
			d.stepEdits = make(map[int]stepEdit)
		}

		d.stepEdits[key] = cur

		return nil
	}

	if prev.via == "" && cur.via == "" {
		return nil
	}

	at := cur
	if at.via == "" {
		at = prev
	}

	first := d.user.Bind(niceyaml.NewError(d.label+" changes it here first",
		niceyaml.AtPath(prev.path), niceyaml.AtRange(prev.at)))

	err := d.user.Bind(niceyaml.NewError(d.label+" changes this value a second time",
		niceyaml.AtPath(cur.path), niceyaml.AtRange(at.viaAt),
		niceyaml.WithDetails(first, fmt.Errorf("write the value out in place of %s", at.via)),
	))
	d.record(err)

	return err
}

// stepEdit returns the record of an edit of p through h.
func (d *Doc) stepEdit(h *Handle, p *place) stepEdit {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	at := d.userRange(vs, max(vs, ve))
	e := stepEdit{path: d.userPath(h.path, at.Start), at: at}

	if p.via == nil {
		return e
	}

	// The value lies under another path in the text, so the path of the
	// handle names the place that the call reached.
	e.path = h.path

	if s, ok := d.nodeSpan(p.via); ok {
		e.via = strings.TrimSpace(d.cur[s.start:s.end])
		e.viaAt = d.userRange(s.start, s.end)
	}

	return e
}
