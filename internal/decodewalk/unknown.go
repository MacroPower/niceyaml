package decodewalk

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// UnknownField is an unknown field of a decode, as [UnknownFields] finds
// it.
type UnknownField struct {
	// Err is the rejection of the key of the field.
	Err *yaml.UnknownFieldError
	// Path is the `@` path of the key, from the value the walk starts at,
	// through the use that read it.
	Path paths.Path
}

// UnknownFields returns each unknown field of a decode of node into a
// value of type t, for a decoder that rejects unknown fields, in the order
// of the source. A key that several uses of an anchor read comes once for
// each use, with the path of that use, as [WalkUses] reaches it. The
// uses of one key come in the order the walk reaches them. The decoder
// stops at the first unknown field it finds. It does not find the fields
// of one mapping in a fixed order, so one decode names one field, and not
// always the same one. A decode that rejects a value does not name an
// unknown field.
//
// The search walks t as [WalkUses] does, once for each use of a value,
// and reads each mapping a struct decodes from. A key of such a mapping is
// a candidate when the struct does not have a field of that name. The
// search reads the field names as [yamlfield.Name] gives them, including
// the fields of inline structs.
//
// The candidates are the entries that the decoder reads. A later entry of
// a key replaces an earlier one, and a `<<` merge key brings in the entries
// of its sources where it stands. The search reads a source in a reference
// document through [Rules.Anchor], so an entry that such a source replaces
// drops out. A key that such a source brings in is not a candidate, and
// the walk does not read below its value, as [Walk] describes. When the
// search cannot read a source, a merge key after an entry might replace
// the entry. The key of that entry stays a candidate, because the struct
// does not have a field of that name whichever entry the decoder reads.
// The walk skips the value of that entry.
//
// The search leaves every other rule of the decode to the decoder, such
// as the prefixes of keys it allows. It also cannot see the types a
// function decodes. So the decoder confirms each candidate. The search
// decodes the one entry of the candidate into a new value of the struct
// type. It keeps the candidate only when that decode rejects its key as
// an unknown field.
//
// The search finds each struct that decodes itself with
// [unmarshaler.Options.ReportsOwnError]. The fields of such a struct do
// not need to mirror the document, and only its unmarshaler says whether
// the decoder checks them. The search therefore reads such a struct and
// the values below it as if the fields mirrored the document. It takes
// every key of the struct's own mapping as a candidate.
//
// The decode that confirms a candidate at or below such a struct runs the
// unmarshaler of the outermost such struct. It runs that unmarshaler on
// the entries and elements that lead from its mapping down to the
// candidate. So the unmarshaler decides, as it does in the decode itself.
// A decoder that rejects unknown fields panics on a struct that holds a
// map inline. Only the unmarshaler of the outermost such struct knows the
// type that reads a candidate. So when its decode panics so, the search
// keeps no candidate at or below that struct. When the decode of
// the one entry of a struct that does not decode itself panics so, a
// decode of that entry into a struct without fields decides.
//
// Before it reads the keys of the outermost such struct, the search
// decodes the whole struct once. When the decoder accepts it, the search
// does not read anything at or below the struct, because the decoder does
// not reject a key there. The search decodes the content of an anchor
// once for all the aliases that reach it, and it confirms a candidate
// once for all the uses that read it the same way. The unmarshaler of a
// struct without an unknown field thus runs once more rather than once
// more for each of its keys or aliases.
//
// An inline map takes every key that no other field reads. So a struct
// that holds a map inline, or holds an inline struct that holds one at
// any depth, does not have an unknown field. The search takes a key of
// the mapping of such a struct as a candidate only when the struct
// decodes itself, because its unmarshaler might not read the map. Such a
// struct still decides for its own fields.
//
// The search does not read below any other type that decodes itself,
// below a struct that decodes itself held inline, or below the members of
// an inline map.
func UnknownFields(ctx context.Context, r Rules, t reflect.Type, node ast.Node) []UnknownField {
	var fields []UnknownField

	held := map[fieldUse]bool{}
	verdicts := map[seenKey]bool{}
	confirmed := map[confirmKey]*yaml.UnknownFieldError{}

	w := newWalker(ctx, r, func(v *Value) Action {
		decodesItself := r.Unmarshalers.ReportsOwnError(v.Type)

		switch {
		case v.Kind == Key, v.Kind == Member && v.Struct != nil, v.Reads == Reference:
			return Skip
		case decodesItself && (v.Kind == Inline || v.Type.Kind() != reflect.Struct):
			return Skip
		case decodesItself && v.Reads == Fields && ownerOf(r, v.Parent) == nil && accepts(r, v, verdicts):
			return Skip
		default:
			return Descend
		}
	})

	w.onFields = func(v *Value, mapping *ast.MappingNode) {
		var names map[string]bool

		if !r.Unmarshalers.ReportsOwnError(v.Type) {
			// An inline map takes each key that no field reads.
			if inlinesMap(v.Type, map[reflect.Type]bool{}) {
				return
			}

			names = fieldNames(v.Type, map[reflect.Type]bool{})
		}

		for entry := range datapath.Members(r.Resolver, mapping, v.merges) {
			if ctx.Err() != nil {
				return
			}

			name, ok := r.Resolver.KeyName(entry.Key)
			if !ok || names[name] || w.entryOf(mapping, name, v.merges) != entry {
				continue
			}

			rejected := w.confirmOnce(v, entry, confirmed)
			if rejected == nil {
				continue
			}

			path := v.Path().Child(name).Key()

			use := fieldUse{offset: rejected.Token.Position.Offset, path: path.String()}
			if !held[use] {
				held[use] = true

				fields = append(fields, UnknownField{Err: rejected, Path: path})
			}
		}
	}

	w.uses = true
	w.root(t, node, astnode.Parent{})

	slices.SortStableFunc(fields, func(a, b UnknownField) int {
		return cmp.Compare(a.Err.Token.Position.Offset, b.Err.Token.Position.Offset)
	})

	return fields
}

// fieldUse names an unknown field that [UnknownFields] found, by the
// offset of its key and the path of the use that read it.
//
//nolint:unused // The fields tell the keys of the set apart.
type fieldUse struct {
	path   string
	offset int
}

// confirmOnce returns what [walker.confirm] returns for entry under v. The
// map done holds the answer for each decode that confirm has run, so the
// uses of an anchor that read entry the same way share one decode.
func (w *walker) confirmOnce(
	v *Value, entry *ast.MappingValueNode, done map[confirmKey]*yaml.UnknownFieldError,
) *yaml.UnknownFieldError {
	node, t, key, ok := w.confirmation(v, entry)
	if !ok {
		return nil
	}

	if rejected, held := done[key]; held {
		return rejected
	}

	rejected := w.confirm(node, t, entry)
	done[key] = rejected

	return rejected
}

// confirmation returns the node and the type that [walker.confirm] decodes
// to confirm entry, an entry of the mapping v reads, as an unknown field
// of the struct v. The node is a mapping of entry alone, and the type is
// the type of v. Under a struct that decodes itself, the node holds the
// outermost such struct, with only the entries and elements that lead down
// to entry, and the type is the type of that struct. The unmarshaler then
// reads entry where the document holds it. The key names the nodes the
// decode reads and the type, so two uses that read entry the same way
// share a key. It
// reports false when the decode does not hold an entry in the place of
// entry or of one that leads to it.
func (w *walker) confirmation(v *Value, entry *ast.MappingValueNode) (ast.Node, reflect.Type, confirmKey, bool) {
	view := w.view(entry)
	if view == nil || view.Key == nil {
		return nil, nil, confirmKey{}, false
	}

	var node ast.Node = ast.Mapping(view.Start, false, view)

	var key strings.Builder

	fmt.Fprintf(&key, "%p", entry)

	owner := ownerOf(w.rules, v)

	t := v.Type

	if owner != nil {
		for at := v; at != owner; at = at.Parent {
			switch at.Kind {
			case Element:
				seq := ast.Sequence(at.From.Sequence.Start, false)
				seq.Values = append(seq.Values, node)
				node = seq

				fmt.Fprintf(&key, " %p[%d]", at.From.Sequence, at.From.Index)

			case Field, Member:
				outer := w.view(at.Entry)
				if outer == nil {
					return nil, nil, confirmKey{}, false
				}

				node = ast.Mapping(outer.Start, false, ast.MappingValue(outer.Start, outer.Key, node))

				fmt.Fprintf(&key, " %p", at.Entry)

			case Root, Inline, Key:
				// The value reads the node of the one that holds it.
			}
		}

		t = owner.Type
	}

	return node, t, confirmKey{chain: key.String(), typ: t}, true
}

// confirmKey names a decode that [walker.confirm] runs, by the nodes it
// reads and the type it decodes into.
//
//nolint:unused // The fields tell the keys of the set apart.
type confirmKey struct {
	typ   reflect.Type
	chain string
}

// confirm returns the rejection of the key of entry as an unknown field,
// when the decode of node into a new value of type t rejects it so, or nil
// for any other outcome. [walker.confirmation] builds node and t.
//
// A decoder that rejects unknown fields panics on a struct that holds a
// map inline, as [Rules] describes for PanicsOnInlineMap. When t decodes
// itself, the panic leaves the key without a verdict, so confirm returns
// nil. Otherwise node holds entry alone, and the struct of type t has no
// field for its key. The search does not take a key of a struct with an
// inline map as a candidate, so the panic comes from another inline field
// that the check of the decoder reads as a struct, such as a map that
// reads an anchor. A decode of node into a struct without fields then
// decides. It rejects the key as the decoder does, except a key under a
// prefix that the decoder allows.
func (w *walker) confirm(node ast.Node, t reflect.Type, entry *ast.MappingValueNode) *yaml.UnknownFieldError {
	view := w.view(entry)

	err := w.rules.Decode(node, reflect.New(t).Interface())
	if w.rules.PanicsOnInlineMap != nil && w.rules.PanicsOnInlineMap(err) {
		if w.rules.Unmarshalers.ReportsOwnError(t) {
			return nil
		}

		err = w.rules.Decode(node, &struct{}{})
	}

	rejected, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || rejected.Token == nil || rejected.Token != view.Key.GetToken() || rejected.Token.Position == nil {
		return nil
	}

	return rejected
}

// ownerOf returns the outermost struct that decodes itself among v and
// the values that hold v, or nil when there is none. Such a struct reads
// its mapping through its unmarshaler, as [UnknownFields] describes.
func ownerOf(r Rules, v *Value) *Value {
	var owner *Value

	for at := v; at != nil; at = at.Parent {
		if at.Kind != Inline && at.Reads == Fields && r.Unmarshalers.ReportsOwnError(at.Type) {
			owner = at
		}
	}

	return owner
}

// accepts reports whether the decoder accepts the node of v, a struct
// that decodes itself, as a new value of the type of v. The verdicts map
// holds the answer for each content node and type that the search has
// decoded, so the aliases of one anchor share one decode.
func accepts(r Rules, v *Value, verdicts map[seenKey]bool) bool {
	key := seenKey{typ: v.Type, node: datapath.Deref(r.Resolver, v.Node)}

	ok, held := verdicts[key]
	if held {
		return ok
	}

	view := r.View(v.Node)
	ok = !astnode.IsNil(view) && r.Decode(view, reflect.New(v.Type).Interface()) == nil
	verdicts[key] = ok

	return ok
}

// fieldNames returns the names of the keys that t, a struct type, has a
// field for, as [yamlfield.Name] names them, at any depth of inline
// structs. The seen set holds the inline structs the walk is inside of,
// so the walk stops at a struct that holds itself inline. An inline field
// that [yamlfield.ReadsAnchor] reports does not add a name.
func fieldNames(t reflect.Type, seen map[reflect.Type]bool) map[string]bool {
	names := yamlfield.OwnNames(t)

	seen[t] = true

	for field := range t.Fields() {
		_, inline, skip := yamlfield.Name(field)
		if skip || !inline || yamlfield.ReadsAnchor(field) {
			continue
		}

		inner := unmarshaler.Base(field.Type)
		if inner.Kind() != reflect.Struct || seen[inner] {
			continue
		}

		for name := range fieldNames(inner, seen) {
			names[name] = true
		}
	}

	delete(seen, t)

	return names
}

// inlinesMap reports whether t, a struct type, holds a map inline, as
// [yamlfield.InlinesMap] reports, or holds a struct inline that does, at
// any depth. The seen set holds the inline structs the walk is inside of,
// so the walk stops at a struct that holds itself inline.
func inlinesMap(t reflect.Type, seen map[reflect.Type]bool) bool {
	if yamlfield.InlinesMap(t) {
		return true
	}

	seen[t] = true
	defer delete(seen, t)

	for field := range t.Fields() {
		_, inline, skip := yamlfield.Name(field)
		if skip || !inline || yamlfield.ReadsAnchor(field) {
			continue
		}

		inner := unmarshaler.Base(field.Type)
		if inner.Kind() == reflect.Struct && !seen[inner] && inlinesMap(inner, seen) {
			return true
		}
	}

	return false
}
