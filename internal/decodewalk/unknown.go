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
// unknown field it finds. It does not find the fields of one mapping in a
// fixed order, so one decode names one field, and not always the same
// one. A decode that rejects a value does not name an unknown field.
//
// The search walks t as [Walk] does and reads each mapping a struct
// decodes from. A key of such a mapping is a candidate when the struct
// does not have a field of that name. The search reads the field names as
// [yamlfield.Name] gives them, including the fields of inline structs.
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
//
// The search does not read below any other type that decodes itself,
// below a struct that decodes itself held inline, or below the members of
// an inline map.
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

// confirm returns the rejection of the key of entry, an entry of the
// mapping v reads, as an unknown field of the struct v, or nil for any
// other outcome. It decodes a mapping of entry alone into a new value of
// the type of v. Under a struct that decodes itself, it decodes the
// outermost such struct from nodes that hold only the entries and
// elements that lead down to entry. The unmarshaler then reads entry
// where the document holds it.
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
