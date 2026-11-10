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
	// index of the highest one.
	Layer   int
	kind    valueKind
	written bool
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
// the value of e as [Value.Merge] describes.
func (e *mapEntry) merge(upper *mapEntry) {
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
		e.value = e.value.Merge(value)
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
// each null it holds, because a sequence replaces what lies below it
// with every element.
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
// describes, and v then holds the result. A null in upper keeps v,
// unless v is a null too. Any other upper replaces v. Either value can be
// nil. A nil value stands for a layer that does not hold a value there.
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
func (v *Value) Merge(upper *Value) *Value {
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

		v.entries[i].merge(entry)
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
