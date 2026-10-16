package edit

import (
	"context"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/edithook"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// init fills in the calls of [edithook], which the manifest package uses
// and which stay out of the API of edit.
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
// describes.
func (d *Doc) setField(ctx context.Context, path paths.Path, text string) error {
	n := &Node{d: d, path: path}

	p, err := d.resolve(path)
	if err != nil {
		return d.bug(n, err)
	}

	if p.exists {
		tk := scalarToken(p.value)
		if tk != nil && tk.Value == text {
			return nil
		}

		newText, err := fieldText(ctx, text, tk)
		if err != nil {
			return d.bug(n, err)
		}

		vs, ve := d.valueStart(p), d.valueEnd(p)
		kept := d.keptNote(vs, max(vs+1, ve))
		origin := d.addNote(kept)
		anchors := d.anchorsIn(p.value)

		if isImplicitNull(p.value) {
			newText = " " + newText
		}

		err = d.apply(edit{start: vs, end: ve, text: newText, origins: written(newText, origin)})
		if err != nil {
			return d.bug(n, err)
		}

		return d.keepAnchors(n, anchors)
	}

	return d.insertField(ctx, n, p, path, text)
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

// insertField inserts the type field at path, which p resolved, first in
// the mapping that holds it.
func (d *Doc) insertField(ctx context.Context, n *Node, p *place, path paths.Path, text string) error {
	value, err := fieldText(ctx, text, nil)
	if err != nil {
		return d.bug(n, err)
	}

	parent, _ := path.Parent()

	holder, err := d.resolve(parent)
	if err != nil || !holder.exists || p.found < path.Len()-1 {
		if !d.holdsMapping(path, p.found) {
			return nil
		}

		// The mappings above the field go with it, as Set creates them.
		return n.set(ctx, text, true)
	}

	entries, ok := astnode.Entries(astnode.Content(holder.value))
	if !ok || isNull(holder.value) {
		return nil
	}

	if len(entries) == 0 {
		return n.set(ctx, text, true)
	}

	key, err := renderKey(ctx, fieldOf(path), nil, holder.flow || isFlow(astnode.Content(holder.value)))
	if err != nil {
		return d.bug(n, err)
	}

	first, err := d.resolve(parent.Child(keyOf(d, entries[0])))
	if err != nil {
		return d.bug(n, err)
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
		return d.bug(n, err)
	}

	return nil
}
