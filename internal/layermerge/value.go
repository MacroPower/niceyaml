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
// of the layer resolved, and [Value.Merge] puts the value of a higher
// layer over it.
type Value struct {
	names   map[string]int
	tag     string
	text    string
	items   []*Value
	entries []*mapEntry
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
	// Reports an entry that a layer removed with a written null. The
	// merged document leaves the entry out, and value is the null that
	// removed it, so an error about the key binds at that null. For an
	// entry that a merged layer removed, value is a null of that layer,
	// which stands for the null in a layer of its own.
	removed bool
}

// merge puts upper over e, where upper is the entry a higher layer holds
// under the name of e. A null in upper that removes, as
// [Value.removes] reports one, removes e when e holds a value, and
// stays a null over a null. Any other null in upper keeps e, unless e is
// such a null too, which upper replaces. Any other value of upper merges
// over the value of e, as [Value.Merge] describes.
//
// A removed e stays removed under a null of either kind, and holds the
// null of the highest layer whose null removes. A removed upper, which
// the reader of a merged layer carries, removes e whatever e holds. Any
// other value of upper takes the place of a removed e, with none of the
// value the layers below held.
func (e *mapEntry) merge(upper *mapEntry) {
	value := upper.value

	switch {
	case upper.removed, value.removes() && (e.removed || e.value.kind != kindNull):
		e.value, e.removed = value, true

	case value.kind == kindNull && !value.removes() && (e.removed || e.value.removes()):
		// The null keeps the removal, or the null below that removes.

	case e.removed:
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
// below hold under its key: a written null with no tag. A tag stays on a
// null where it converts the null, as "!!str" does, and such a null
// keeps the value below it, written or not. The merged text spells both
// as a written null, since go-yaml parses no entry behind a tag with no
// value, so the two must merge alike.
func (v *Value) removes() bool {
	return v.kind == kindNull && v.written && v.tag == ""
}

// Merge returns upper merged over v, where upper comes from a higher
// layer. Two mappings merge entry by entry, as [mapEntry.merge]
// describes, and v then holds the result. A null in upper keeps v,
// unless v is a null too. Any other upper replaces v. Either value may
// be nil, which stands for a layer that holds no value there.
//
// No null removes a value here. [mapEntry.merge] decides that for the
// value under a key, and the root of a layer has no key.
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
		return upper
	}

	for _, entry := range upper.entries {
		i, ok := v.names[entry.name]
		if !ok {
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

// block reports whether v takes lines of its own in the merged document:
// a sequence that is not empty, or a mapping with an entry that no layer
// removed.
func (v *Value) block() bool {
	return len(v.items) > 0 || slices.ContainsFunc(v.entries, func(e *mapEntry) bool { return !e.removed })
}

// inline returns the text of a value that is no block, with its tag in
// front. With keyed set, the text goes behind the ":" of a key, where an
// unwritten null with no tag has no text, as its layer spells it. A
// written null there would remove a value once the merged document is a
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
// to v, and true. It follows rel for as long as the values hold it, and
// returns the last value it reaches and false when one does not. A nil v
// holds no value.
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

// child returns the value sel selects in v, or nil when v holds none. The
// `~` selector of a key selects v itself, since the key of an entry
// belongs to the layer of its value. The name of an entry that a layer
// removed selects the null that removed it.
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

// Carry adds each entry that held marks as removed to v, a value that a
// [Reader] read at the index layer. It serves a layer that is a node of a
// merged document, where held is the value the layers of that document
// hold at the node. The reader read v from the merged text, which leaves
// out each key a layer removed. Each such entry goes in where held has
// it, as an entry the layer removes, so the layer removes what its own
// layers do. A nil held holds no entry.
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
