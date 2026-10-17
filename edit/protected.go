package edit

import (
	"context"
	"errors"
	"fmt"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/edithook"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// init sets the hooks of [edithook]. The manifest package calls those
// hooks, and they are not part of the API of edit.
//
//nolint:gochecknoinits // edithook cannot import edit, so edit sets the hooks when it loads.
func init() {
	edithook.Locator = func(d any) func(hook.Located) hook.Place {
		doc, _ := d.(*Doc) //nolint:errcheck // The hook takes a *Doc.

		return doc.locate
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

	err = d.modelEnd(&modelCall{x: x, path: path}, err, func() bool { return change.apply(x) })

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
// model. The write sets the value at a place, or it puts a new entry first
// in a mapping. A write that leaves the text as it is makes no change.
type fieldChange struct {
	// The place that the write sets, as the model reads it, or nil.
	at *modelCall
	// The mapping of the model that takes the new entry, or nil, and the
	// name of the entry.
	holder *value
	// The value that the write writes, as the model reads it. It is nil for
	// a value that the model cannot read.
	value *value
	name  string
}

// apply makes the change c in x. It reports false when the model cannot
// take the change.
func (c fieldChange) apply(x *model) bool {
	switch {
	case c.holder != nil:
		return x.insert(c.holder, c.name, c.value)
	case c.at != nil:
		return c.at.set(&edge{val: c.value})
	default:
		return true
	}
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

	return d.insertField(ctx, n, p, path, text)
}

// fieldScalar returns the value of the model for a type field that holds
// the string text. A load of the field reads the string that go-yaml's
// decoder reads from its scalar, as [readsAs] reads it. So the
// field holds text, whatever style the write gives it, when that string is
// text, as [sameScalar] compares it.
func fieldScalar(text string) *value {
	return newScalarValue(vScalar, nil, scalar{val: text, decoded: true, field: true})
}

// modelValue returns the value of the model that pairs with node, a value
// of the current text, or nil.
func (d *Doc) modelValue(node ast.Node) *value {
	x := d.model
	if x == nil || x.text == nil {
		return nil
	}

	if v := x.text.values[astnode.Content(node)]; v != nil {
		return v.pair
	}

	return nil
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

// insertField inserts the type field at path, which p resolved, first in
// the mapping that holds it. It returns the change as [Doc.writeTypeField]
// describes. Where the document lacks that mapping, or the mapping holds no
// entry, the field goes in as Set creates a value, with the mappings above
// it that the document lacks. Where the values above the field leave no
// mapping to hold it, the write fails as such a Set fails.
func (d *Doc) insertField(ctx context.Context, n *Node, p *place, path paths.Path, text string) (fieldChange, error) {
	parent, _ := path.Parent()

	holder, err := d.resolve(parent)
	if err == nil && holder.exists && p.found == path.Len()-1 {
		entries, ok := readEntries(astnode.Converted(holder.value))
		if ok && len(entries) > 0 {
			return d.writeField(ctx, n, holder, entries[0], text)
		}
	}

	// The field goes in as Set writes it.
	at := d.model.placeOf(p)

	_, err = n.set(ctx, text, true)

	return fieldChange{at: &at, value: fieldScalar(text)}, err
}

// newEntry returns the change of a write that puts the entry name with v
// into h, the mapping of the model that pairs with the mapping of the write,
// as its first entry. Both values are values of the model. When the model
// does not hold the mapping, the model cannot take the change.
func newEntry(h *value, name string, v *value) fieldChange {
	if h == nil {
		return fieldChange{at: &modelCall{}}
	}

	return fieldChange{holder: h, name: name, value: v}
}

// writeField writes the type field of n with text into the mapping of
// holder, before first, the first entry of the mapping. It returns the
// change as [Doc.writeTypeField] describes.
func (d *Doc) writeField(
	ctx context.Context,
	n *Node,
	holder *place,
	first *ast.MappingValueNode,
	text string,
) (fieldChange, error) {
	content := astnode.Content(holder.value)
	flow := isFlow(content)

	value, err := fieldText(ctx, text, nil, holder.flow || flow)
	if err != nil {
		return fieldChange{}, d.bug(n, err)
	}

	change := newEntry(d.modelValue(holder.value), fieldOf(n.path), fieldScalar(text))

	key := renderKey(ctx, fieldOf(n.path), nil, holder.flow || flow)
	origin := d.addNote(d.createdNote(holder, "sets "+n.path.String()))
	entry := key + ": " + value
	at := entryPlace(content, first)

	var start int

	if flow {
		start, _ = d.startOf(at)
		entry += ", "
	} else {
		reg := d.blockRegion(at)
		col := d.keyColumn(at)
		brk := d.breakBefore(reg.first)
		entry = indentLines([]string{entry}, col, brk) + brk
		start = d.lineStart(reg.first)
	}

	e := edit{start: start, end: start, text: entry, origins: written(entry, origin)}

	err = d.apply(e)
	if err != nil {
		return change, d.bug(n, err)
	}

	return change, nil
}
