package decodewalk

import (
	"cmp"
	"context"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

// UnknownFields returns the rejection of each unknown field of a decode
// of node into a value of type t, for a decoder that rejects unknown
// fields, in the order of the source. The decoder stops at the first
// unknown field it finds, and it finds the fields of one mapping in no
// fixed order, so one decode names one field, and not always the same
// one. A decode that rejects a value names none.
//
// The search walks t as [Walk] does and reads each mapping a struct
// decodes from. A key of such a mapping is a candidate when no field of
// the struct has its name, as [yamlfield.Name] names the fields, the
// fields of inline structs among them. The search leaves every other
// rule of the decode to the decoder, such as the prefixes of keys it
// allows, and it cannot see the types a function decodes. So the decoder
// confirms each candidate. The search decodes the one entry of the
// candidate into a new value of the struct type, and keeps the candidate
// only when that decode rejects its key as an unknown field.
//
// The fields of a struct that decodes itself, as
// [unmarshaler.Options.ReportsOwnError] lists those types, need not
// mirror the document, and only its unmarshaler says whether the decoder
// checks them. The search therefore reads such a struct, and the values
// below it, as if the fields mirrored the document, and takes every key
// of its own mapping for a candidate. The decode that confirms a
// candidate at or below such a struct runs the unmarshaler of the
// outermost one, on the entries and elements that lead from its mapping
// down to the candidate. So the unmarshaler decides, as it does in the
// decode itself.
//
// The search reads nothing below any other type that decodes itself,
// below a struct that decodes itself held inline, or below the members
// of an inline map.
func UnknownFields(ctx context.Context, r Rules, t reflect.Type, node ast.Node) []*yaml.UnknownFieldError {
	found := map[int]*yaml.UnknownFieldError{}

	w := newWalker(ctx, r, func(v *Value) Action {
		decodesItself := r.Unmarshalers.ReportsOwnError(v.Type)

		switch {
		case v.Kind == Key, v.Kind == Member && v.Struct != nil:
			return Skip
		case decodesItself && (v.Kind == Inline || v.Type.Kind() != reflect.Struct):
			return Skip
		default:
			return Descend
		}
	})

	w.onFields = func(v *Value, mapping *ast.MappingNode) {
		var names map[string]bool

		if !r.Unmarshalers.ReportsOwnError(v.Type) {
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

			rejected := w.confirm(v, entry)
			if rejected != nil {
				offset := rejected.Token.Position.Offset
				if _, held := found[offset]; !held {
					found[offset] = rejected
				}
			}
		}
	}

	w.root(t, node, astnode.Parent{})

	fields := make([]*yaml.UnknownFieldError, 0, len(found))
	for _, field := range found {
		fields = append(fields, field)
	}

	slices.SortFunc(fields, func(a, b *yaml.UnknownFieldError) int {
		return cmp.Compare(a.Token.Position.Offset, b.Token.Position.Offset)
	})

	return fields
}

// confirm asks the decoder whether the key of entry, an entry of the
// mapping v reads, is an unknown field of the struct v, and returns the
// rejection when it is. The decode reads a mapping that holds entry alone
// into a new value of the type of v. The decoder reads no value under a
// key it rejects, so the value of the entry does not decide the result.
//
// Under a struct that decodes itself, the decode reads the outermost such
// struct that holds v, or v itself. The mapping of the entry then sits
// inside one mapping or sequence for each value on the way from that
// struct down to v. Each of those holds the one entry or element the way
// goes through. The unmarshaler of the struct thus reads the entry where
// the document holds it.
//
// Any other outcome returns nil. That is a decode that passes, and one
// that fails another way.
func (w *walker) confirm(v *Value, entry *ast.MappingValueNode) *yaml.UnknownFieldError {
	view := w.view(entry)
	if view == nil || view.Key == nil {
		return nil
	}

	var (
		node  ast.Node = ast.Mapping(view.Start, false, view)
		owner *Value
	)

	for at := v; at != nil; at = at.Parent {
		if at.Kind != Inline && at.Reads == Fields && w.rules.Unmarshalers.ReportsOwnError(at.Type) {
			owner = at
		}
	}

	t := v.Type

	if owner != nil {
		for at := v; at != owner; at = at.Parent {
			switch at.Kind {
			case Element:
				seq := ast.Sequence(at.From.Sequence.Start, false)
				seq.Values = append(seq.Values, node)
				node = seq

			case Field, Member:
				outer := w.view(at.Entry)
				if outer == nil {
					return nil
				}

				node = ast.Mapping(outer.Start, false, ast.MappingValue(outer.Start, outer.Key, node))

			case Root, Inline, Key:
				// The value reads the node of the one that holds it.
			}
		}

		t = owner.Type
	}

	err := w.rules.Decode(node, reflect.New(t).Interface())

	rejected, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || rejected.Token == nil || rejected.Token != view.Key.GetToken() || rejected.Token.Position == nil {
		return nil
	}

	return rejected
}

// fieldNames returns the names of the keys that t, a struct type, has a
// field for, as [yamlfield.Name] names them. The fields of an inline
// struct count as fields of t, however deep the inline structs nest. The
// seen set holds the inline structs the walk is inside of, so a struct
// that holds itself inline ends the walk. A field that
// [yamlfield.ReadsAnchor] reports reads no key, so its struct adds no
// name.
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
