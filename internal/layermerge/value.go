package layermerge

import (
	"slices"

	"go.jacobcolvin.com/niceyaml/paths"
)

// valueKind is the kind of a [Value].
type valueKind int

// [valueKind] constants. A value of the zero kind is a null.
const (
	kindNull valueKind = iota
	kindScalar
	kindSequence
	kindMapping
)

// Value is a value of the document that a merge of layers gives. A
// [Reader] reads one from each layer, with every alias and `<<` merge key
// of the layer resolved. [Value.Merge] puts the value of a higher layer
// over it.
type Value struct {
	names   map[string]int
	tag     string
	text    string
	items   []*Value
	entries []*mapEntry
	// Layer is the index of the layer the value comes from, among the
	// layers the merge reads. A mapping that several layers hold takes the
	// index of the highest one, and so does a sequence that several layers
	// append to.
	Layer int
	// The index of the value in the sequence that holds it in its own
	// layer. A sequence that several layers append to holds the element
	// at another index.
	index   int
	kind    valueKind
	written bool
}

// Appends names the sequences that [Value.Merge] appends to the sequence
// below them. It holds paths of names and `.*` selectors, and each path
// reads from the value that Merge merges. The zero Appends does not name
// a sequence.
//
// Create instances with [NewAppends].
type Appends struct {
	// The selectors that each path still holds below the value, for each
	// path that matches every key down to that value.
	rest [][]paths.Selector
}

// NewAppends creates a new [Appends] that names the sequence at each of
// rels. Each path reads from the value that [Value.Merge] merges. It
// holds `.name` and `.*` selectors only, and Merge does not match a path
// that holds any other selector.
func NewAppends(rels ...paths.Path) Appends {
	var a Appends

	for _, rel := range rels {
		a.rest = append(a.rest, slices.Collect(rel.Selectors()))
	}

	return a
}

// here reports whether a names the sequence at the value itself.
func (a Appends) here() bool {
	return slices.ContainsFunc(a.rest, func(rest []paths.Selector) bool { return len(rest) == 0 })
}

// under returns the sequences that a names below the entry of the given
// name. A `.name` selector matches the name of the entry, and a `.*`
// selector matches every name.
func (a Appends) under(name string) Appends {
	var below Appends

	for _, rest := range a.rest {
		if len(rest) == 0 {
			continue
		}

		sel := rest[0]
		if sel.Kind == paths.SelectorChildAll || (sel.Kind == paths.SelectorChild && sel.Name == name) {
			below.rest = append(below.rest, rest[1:])
		}
	}

	return below
}

// mapEntry is an entry of a mapping in a [Value].
type mapEntry struct {
	value *Value
	// The text a `.name` selector matches the key by, as
	// [paths.Resolver.KeyName] gives it.
	name string
	// The YAML text of the key, with its tag, as the layer of value
	// spells it.
	key string
	// Whether a written null of a layer keeps the entry out of the merged
	// document. Such a null removes the entry that the layers below hold,
	// and it does not add an entry where they do not hold one. The value
	// field holds that null, so an error about the key binds at it. For an
	// entry that a merged layer removed, value is a null of that layer.
	// That null stands for the null in a layer of its own.
	removed bool
}

// merge puts upper, the entry a higher layer holds under the name of e,
// over e. A removed upper removes e, and so does a null that removes, as
// [Value.removes] reports. Any other null keeps a removed e and a null of
// e that removes. Any other value replaces a removed e, or merges over
// the value of e as [Value.Merge] describes, with the sequences that
// appends names below e.
func (e *mapEntry) merge(upper *mapEntry, appends Appends) {
	value := upper.value

	switch {
	case upper.removed, value.removes():
		e.value, e.removed = value, true

	case value.kind == kindNull && (e.removed || e.value.removes()):
		// The null keeps the removal, or the null below that removes.

	case e.removed:
		value.strip()

		e.value, e.removed = value, false

	default:
		e.value = e.value.Merge(value, appends)
	}

	// The key reads as the layer of the value spells it, so a path to the
	// key resolves in that layer.
	if e.value.Layer == value.Layer {
		e.key = upper.key
	}
}

// removes reports whether v is a null that removes the value the layers
// below hold under its key: a written null without a tag. A tagged null
// keeps that value, written or not, because the merged text must write
// both forms as a written null. It must do so because the go-yaml parser
// does not parse an entry behind a tag without a value.
func (v *Value) removes() bool {
	return v.kind == kindNull && v.written && v.tag == ""
}

// strip marks as removed each entry of v whose null removes, as
// [Value.removes] reports. It serves a value of a higher layer that the
// merged document takes whole. The layers below do not hold a value for
// such a null to remove, and the null does not add its key either. The
// entries of each mapping inside v take the same mark. A sequence keeps
// each null it holds, because an element never merges with an element of
// a layer below.
func (v *Value) strip() {
	for _, entry := range v.entries {
		entry.strip()
	}
}

// strip marks e as removed when its null removes, and otherwise strips
// its value as [Value.strip] describes.
func (e *mapEntry) strip() {
	if e.value.removes() {
		e.removed = true

		return
	}

	e.value.strip()
}

// Merge returns upper merged over v, where upper comes from a higher
// layer. Two mappings merge entry by entry, as [mapEntry.merge]
// describes, and v then holds the result. Two sequences that appends
// names put the elements of upper behind the elements of v, and v then
// holds them all. Each element keeps its own layer, and the sequence
// takes the layer of upper. A null in upper keeps v, unless v is a null
// too. Any other upper replaces v. Either value can be nil. A nil value
// stands for a layer that does not hold a value there.
//
// The paths of appends read from v. Merge matches them against the name
// of each entry on the way down, so they name sequences under the keys of
// mappings only.
//
// For a nil v, the result is upper as its layer wrote it, with each null
// it holds. Over any other v, the null of an entry of upper that removes,
// as [Value.removes] reports, does not reach the merged document. It
// removes the entry that v holds under its key, and it does not add an
// entry where v does not hold one. Its entry stays in the result as a
// removed one. A null inside a sequence of upper stays.
//
// Merge does not let a null remove v itself. [mapEntry.merge] decides
// removal for the value under a key, and the root of a layer does not
// have a key.
func (v *Value) Merge(upper *Value, appends Appends) *Value {
	switch {
	case v == nil:
		return upper
	case upper == nil:
		return v
	case upper.kind == kindNull:
		if v.kind == kindNull {
			return upper
		}

		return v

	case v.kind == kindSequence && upper.kind == kindSequence && appends.here():
		v.items = append(v.items, upper.items...)

	case v.kind != kindMapping || upper.kind != kindMapping:
		upper.strip()

		return upper
	}

	for _, entry := range upper.entries {
		i, ok := v.names[entry.name]
		if !ok {
			entry.strip()

			v.names[entry.name] = len(v.entries)
			v.entries = append(v.entries, entry)

			continue
		}

		v.entries[i].merge(entry, appends.under(entry.name))
	}

	v.Layer = upper.Layer

	if upper.tag != "" {
		v.tag = upper.tag
	}

	return v
}

// block reports whether v takes lines of its own in the merged document.
// A sequence that is not empty does, and so does a mapping that holds an
// entry that is not removed.
func (v *Value) block() bool {
	return len(v.items) > 0 || slices.ContainsFunc(v.entries, func(e *mapEntry) bool { return !e.removed })
}

// inline returns the text of a value that is not a block, with its tag in
// front. With keyed set, the text goes behind the ":" of a key. There an
// unwritten null without a tag has empty text, as its layer spells it. A
// written null there removes a value when the merged document is a
// layer.
func (v *Value) inline(keyed bool) string {
	text := v.text

	switch v.kind {
	case kindMapping:
		text = "{}"
	case kindSequence:
		text = "[]"
	case kindNull, kindScalar:
	}

	switch {
	case v.tag != "":
		return v.tag + " " + text
	case keyed && v.kind == kindNull && !v.written:
		return ""
	}

	return text
}

// Find returns the value rel selects in v, where rel is a path relative
// to v, and true. It follows rel while each value holds the next
// selector. When a value does not hold it, Find returns the last value
// it reached and false. For a nil v, Find returns nil and false.
func (v *Value) Find(rel paths.Path) (*Value, bool) {
	if v == nil {
		return nil, false
	}

	at := v

	for sel := range rel.Selectors() {
		next := at.child(sel)
		if next == nil {
			return at, false
		}

		at = next
	}

	return at, true
}

// LayerPath returns rel, a path relative to v, as the layer at the given
// index holds it. A sequence that several layers append to holds each
// element at another index than the layer of that element does. So each
// `[n]` selector of rel that selects an element of that layer takes the
// index the element has there. Every other selector stays as it is. The
// result differs from rel only when rel goes through such an element.
func (v *Value) LayerPath(rel paths.Path, layer int) paths.Path {
	var held paths.Path

	at := v

	for sel := range rel.Selectors() {
		if at != nil {
			at = at.child(sel)
		}

		if sel.Kind == paths.SelectorIndex && at != nil && at.Layer == layer {
			sel.Index = at.index
		}

		held = with(held, sel)
	}

	return held
}

// Element returns the first value on rel, a path relative to v, that is
// an element of a sequence that appends names. Every value at or below
// that element comes from the layer of the element. Element returns nil
// when rel does not go through such an element, and when v does not hold
// the element.
func (v *Value) Element(rel paths.Path, appends Appends) *Value {
	at := v

	for sel := range rel.Selectors() {
		if at == nil {
			return nil
		}

		named := at.kind == kindSequence && appends.here()

		at = at.child(sel)

		switch sel.Kind {
		case paths.SelectorIndex:
			if named {
				return at
			}

			appends = Appends{}

		case paths.SelectorChild:
			appends = appends.under(sel.Name)

		case paths.SelectorKey:

		case paths.SelectorChildAll, paths.SelectorIndexAll, paths.SelectorRecursive, paths.SelectorRecursiveAll:
			return nil
		}
	}

	return nil
}

// with returns p with sel behind its selectors.
func with(p paths.Path, sel paths.Selector) paths.Path {
	switch sel.Kind {
	case paths.SelectorChild:
		return p.Child(sel.Name)
	case paths.SelectorIndex:
		return p.Index(sel.Index)
	case paths.SelectorChildAll:
		return p.ChildAll()
	case paths.SelectorIndexAll:
		return p.IndexAll()
	case paths.SelectorRecursive:
		return p.Recursive(sel.Name)
	case paths.SelectorRecursiveAll:
		return p.RecursiveAll()
	case paths.SelectorKey:
		return p.Key()
	}

	return p
}

// child returns the value sel selects in v, or nil when v does not hold
// one. The `~` selector of a key selects v itself, because the key of an
// entry belongs to the layer of its value. The name of an entry that a
// layer removed selects the null that removed it.
func (v *Value) child(sel paths.Selector) *Value {
	switch sel.Kind {
	case paths.SelectorKey:
		return v

	case paths.SelectorChild:
		if i, ok := v.names[sel.Name]; ok {
			return v.entries[i].value
		}

	case paths.SelectorIndex:
		if v.kind == kindSequence && sel.Index >= 0 && sel.Index < len(v.items) {
			return v.items[sel.Index]
		}

	case paths.SelectorChildAll, paths.SelectorIndexAll, paths.SelectorRecursive, paths.SelectorRecursiveAll:
	}

	return nil
}

// Carry adds to v each entry that held marks as removed, where v is a
// value a [Reader] read for the layer at the given index. It serves a
// layer that is a node of a merged document, and held is the value that
// the layers of that document hold at the node. The reader read v from
// the merged text, which leaves out each key that a layer removed. Carry
// puts each such entry where held has it, as an entry that the layer
// removes, so the layer removes what its own layers do. Carry does not
// change v for a nil held.
func (v *Value) Carry(held *Value, layer int) {
	if held == nil || v.kind != kindMapping || held.kind != kindMapping {
		return
	}

	entries := make([]*mapEntry, 0, len(held.entries))

	// The text holds the entries of held that no layer removed, in the
	// order held has them, and next is the first of them still to place.
	next := 0

	for _, entry := range held.entries {
		switch {
		case entry.removed:
			entries = append(entries, &mapEntry{
				name:    entry.name,
				key:     entry.key,
				value:   &Value{Layer: layer},
				removed: true,
			})

		case next < len(v.entries):
			v.entries[next].value.Carry(entry.value, layer)

			entries = append(entries, v.entries[next])
			next++
		}
	}

	v.entries = append(entries, v.entries[next:]...)

	for i, entry := range v.entries {
		v.names[entry.name] = i
	}
}
