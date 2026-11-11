package edit

import (
	"context"
	"errors"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/edithook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// init sets the hooks of [edithook]. The manifest package calls those
// hooks, and they are not part of the API of edit.
//
//nolint:gochecknoinits // edithook cannot import edit, so edit sets the hooks when it loads.
func init() {
	edithook.Locator = func(d any) func(niceyaml.Located) niceyaml.Place {
		doc, _ := d.(*Doc) //nolint:errcheck // The hook takes a *Doc.

		return doc.locate
	}
	edithook.Bounds = func(d any) (int, int) {
		doc, _ := d.(*Doc) //nolint:errcheck // The hook takes a *Doc.

		return doc.start, doc.end
	}
	edithook.SetField = func(ctx context.Context, d any, label string, path paths.Path, text string) error {
		doc, _ := d.(*Doc) //nolint:errcheck // The hook takes a *Doc.

		doc.begin(label)

		return doc.setField(ctx, path, text)
	}
}

// setField sets the type field at path to text, as [edithook.SetField]
// describes. The limits of the Source and the model of the write then check
// the text, as the check of an Apply does after its last call. A failed
// write leaves the Doc as it found it, as a failed Apply does.
func (d *Doc) setField(ctx context.Context, path paths.Path, text string) error {
	saved := d.save()
	x := d.syncModel()
	d.callStart, d.calling = saved, true
	d.mergesAt = d.mergeState()
	x.keepsAnchor = false

	// The write runs as a Set of path runs, and replaces the value there.
	at, rerr := d.resolve(path)
	if rerr != nil {
		at = nil
	}

	d.startCall("sets "+path.String(), at)

	change, err := d.writeTypeField(ctx, path, text)

	// A write that Set does not make records the edit at the field that it
	// wrote, as Set records its edit.
	if d.lastEdit == nil && err == nil {
		p, rerr := d.resolve(path)
		if rerr == nil && p.exists {
			d.recordEdit(p, "sets "+path.String(), "")
		}
	}

	if d.lastEdit == nil {
		desc := "sets " + path.String()
		d.lastEdit = &editRecord{desc: desc, named: desc, path: path.Key()}
	}

	err = d.modelEnd(&modelCall{x: x, path: path}, err, change.apply)

	d.checking = true

	if err == nil {
		err = d.limitProblem(saved.cur)
	}

	if err == nil {
		err = d.checkModel()
	}

	d.checking = false

	if err != nil {
		d.restore(saved)
	}

	d.endCalls()

	return err
}

// fieldChange is the change that a write of a type field makes in the
// model. The write sets the value at a place, or it creates the field at
// the place. A write that leaves the text as it is makes no change.
type fieldChange struct {
	// The place that the write sets, as the model reads it, or nil.
	at *modelCall
	// The value that the write writes, as the model reads it. It is nil for
	// a value that the model cannot read.
	value *value
	// Where the entry that the write creates goes in its mapping.
	place modelPlace
}

// apply makes the change c in the model. It reports false when the model
// cannot take the change.
func (c fieldChange) apply() bool {
	if c.at == nil {
		return true
	}

	return c.at.set(&edge{val: c.value}, c.place)
}

// writeTypeField writes the type field at path with text. It returns the
// change of the write in the model, which it reads before the write
// changes the text. A lookup of path that stops at an alias of the text
// that does not resolve fails with the problem of the document, as a read
// of path does.
func (d *Doc) writeTypeField(ctx context.Context, path paths.Path, text string) (fieldChange, error) {
	n := &Node{d: d, path: path}

	// A lookup through an alias of a reference document fails as a read of
	// path does, because the document does not hold the text that the write
	// would go in.
	p, err := d.resolve(path)
	if errors.Is(err, paths.ErrReferenceAlias) || errors.As(err, new(*aliasError)) {
		return fieldChange{}, d.readFail(n, fmt.Errorf("%s sets %s, which %w", d.subject(), path, err))
	}

	if err != nil {
		return fieldChange{}, d.bug(n, fmt.Errorf("%s %w", path, err))
	}

	// The write replaces or creates the value as Set does, so it fails where
	// it reaches a zone of the text, as Set fails there.
	c := reachCall{kind: callSet, path: path, named: "sets " + path.String()}

	err = d.reachProblem(c)
	if err == nil {
		err = d.setRules(c, false)
	}

	if err != nil {
		return fieldChange{}, err
	}

	if p.exists {
		return d.replaceField(ctx, n, p, text)
	}

	return d.insertField(ctx, n, p, text)
}

// fieldScalar returns the value of the model for a type field that holds
// the string text. A load of the field reads the string that go-yaml's
// decoder reads from its scalar, as [readsAs] reads it. So the
// field holds text, whatever style the write gives it, when that string is
// text, as [sameScalar] compares it.
func fieldScalar(text string) *value {
	return newScalarValue(vScalar, nil, scalar{val: text, decoded: true, field: true})
}

// replaceField replaces the value of p, the place of the type field of n,
// with text. It returns the change as [Doc.writeTypeField] describes.
func (d *Doc) replaceField(ctx context.Context, n *Node, p *place, text string) (fieldChange, error) {
	// The field holds the string that go-yaml's decoder reads, as a load of
	// the field reads it, so a block scalar that holds text keeps its text.
	if readsAs(p.value, text) {
		return fieldChange{}, nil
	}

	tk := scalarToken(p.value)

	newText, err := fieldText(ctx, text, tk, p.flow)
	if err != nil {
		return fieldChange{}, d.bug(n, err)
	}

	at := d.model.placeOf(p)
	change := fieldChange{at: &at, value: fieldScalar(text)}

	vs, ve := d.valueStart(p), d.valueEnd(p)

	// The field keeps the anchor of its value, so each alias of the anchor
	// reads the new value. A tag after the anchor goes with the old value.
	space := isImplicitNull(p.value)
	if s, ok := d.keptAnchorSpan(p); ok {
		vs, space = s.end, true
		d.model.keepsAnchor = true
	}

	// The new value stands for the old one, as the value of a Set does, and
	// its note names the write as the call that wrote it. The write checks
	// its text before it writes, as a Set checks its value, so a character
	// that YAML does not allow fails the write where it fails a Set.
	origin := d.addNote(d.setNote(p, "sets "+n.path.String()))

	if what, ok := unprintableIn(newText); ok {
		return change, d.valueBug(n, origin, unprintableError(what))
	}

	if space {
		newText = " " + newText
	}

	err = d.apply(edit{start: vs, end: ve, text: newText, origins: written(newText, origin)})
	if err != nil {
		return change, d.bug(n, err)
	}

	return change, nil
}

// insertField inserts the type field of n, whose place is p, first in the
// mapping that holds it, as a Set with [First] creates a value. It returns
// the change as [Doc.writeTypeField] describes. Where the document lacks
// that mapping, the field goes in as Set creates a value, with the
// mappings above it that the document lacks. Those mappings go after the
// last entry of the mapping that takes them. Where the values above the
// field leave no mapping to hold it, the write fails as such a Set fails.
func (d *Doc) insertField(ctx context.Context, n *Node, p *place, text string) (fieldChange, error) {
	at := d.model.placeOf(p)

	var where placement

	if p.found == n.path.Len()-1 {
		where.kind = placeFirst
	}

	_, place, err := n.set(ctx, text, true, setConfig{at: where})

	return fieldChange{at: &at, value: fieldScalar(text), place: place}, err
}
