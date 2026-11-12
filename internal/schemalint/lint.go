package schemalint

import (
	"encoding/json"
	"fmt"
	"reflect"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Finding is one problem that a lint found in a schema.
type Finding struct {
	// Message names the path of the property and the problem, as in
	// "$.seats: required, so its default 20 never applies".
	Message string
	// Details holds the text of each detail of the problem.
	Details []string
}

// RequiredDeprecated returns a [Finding] for each property that the
// document both requires and deprecates, as [Doc.Properties] reads them.
// A user cannot remove such a property, so the user cannot follow the
// warning about it.
func RequiredDeprecated(d *Doc) []Finding {
	var out []Finding

	for _, p := range d.Properties() {
		if p.Required && p.Deprecated {
			out = append(out, Finding{Message: fmt.Sprintf("%s: required and deprecated", p.Path)})
		}
	}

	return out
}

// UnusedDefaults returns a [Finding] for each property that the document
// requires, that a document replaces whole, and whose default is not zero.
// Every file must then write the property, so the default never applies.
// The walk reads the document beside t, the Go type that a decode reads
// with it. The defaults function returns the value that a decode of a type
// starts from, which is addressable.
//
// A document replaces every value whole, except a struct and a pointer to
// a struct, which a mapping merges into field by field. The walk follows
// properties, items, and additionalProperties through the chains of
// schemas, as [Doc.Marked] describes a chain. Under an item, a map value,
// or a nil pointer, it reads the defaults of the type. Under a pointer
// that a parent default sets, it reads the value that the parent set.
//
// When the default at a place differs from the one that the type that
// declares the field gives itself, a detail of the finding says so.
func UnusedDefaults(d *Doc, t reflect.Type, defaults func(t reflect.Type) reflect.Value) []Finding {
	w := &defaultsWalk{
		doc:      d,
		defaults: defaults,
		onPath:   map[defaultsKey]bool{},
	}

	w.walk(d.root, t, defaults(t), paths.Doc())

	return w.found
}

// defaultsWalk walks a schema beside the Go type that a decode reads with
// it, and the defaults that the decode starts from at each place.
type defaultsWalk struct {
	doc      *Doc
	defaults func(t reflect.Type) reflect.Value
	onPath   map[defaultsKey]bool
	found    []Finding
}

// defaultsKey names a schema that the walk reads as a type.
//
//nolint:unused // The fields tell the keys of a map apart.
type defaultsKey struct {
	schema *jsonschema.Schema
	typ    reflect.Type
}

// walk reads the value of type t at path, which starts from v, with the
// schema s. The walk follows properties, items, additionalProperties,
// and each schema in the chain of s. A pointer reads its target, which
// starts from the value that the pointer holds, or from the defaults of
// its type for a nil pointer.
func (w *defaultsWalk) walk(s *jsonschema.Schema, t reflect.Type, v reflect.Value, path paths.Path) {
	for t.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = w.defaults(t.Elem())
		} else {
			v = v.Elem()
		}

		t = t.Elem()
	}

	key := defaultsKey{schema: s, typ: t}
	if s == nil || w.onPath[key] {
		return
	}

	w.onPath[key] = true
	defer delete(w.onPath, key)

	group := w.doc.chain(s)

	switch {
	case t.Kind() == reflect.Struct && !unmarshaler.DecodesItself(t):
		w.fields(group, t, v, path)

	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		for _, g := range group {
			w.walk(g.Items, t.Elem(), w.defaults(t.Elem()), path.IndexAll())
		}

	case t.Kind() == reflect.Map:
		for _, g := range group {
			w.walk(g.AdditionalProperties, t.Elem(), w.defaults(t.Elem()), path.ChildAll())
		}
	}
}

// fields reads each field of v, a struct of type t that the decode reads
// field by field, with the properties of the schemas of group. It
// records each field that group requires, that the document replaces
// whole, and whose default is not zero.
func (w *defaultsWalk) fields(group []*jsonschema.Schema, t reflect.Type, v reflect.Value, path paths.Path) {
	required := map[string]bool{}
	props := map[string][]*jsonschema.Schema{}

	for _, g := range group {
		for _, name := range g.Required {
			required[name] = true
		}

		for name, sub := range g.Properties {
			props[name] = append(props[name], sub)
		}
	}

	for _, f := range fieldsOf(t, v) {
		at := path.Child(f.name)

		if required[f.name] && replacedWhole(f.value.Type()) && !f.value.IsZero() {
			w.found = append(w.found, w.defaultFinding(at, f))
		}

		for _, sub := range props[f.name] {
			w.walk(sub, f.value.Type(), f.value, at)
		}
	}
}

// defaultFinding returns the finding of the field f at path, which the
// schema requires although its default is not zero. When the default at
// path differs from the one that the type that declares f gives itself, a
// detail says so.
func (w *defaultsWalk) defaultFinding(path paths.Path, f structField) Finding {
	finding := Finding{
		Message: fmt.Sprintf("%s: required, so its default %s never applies", path, valueText(f.value)),
	}

	own := w.defaults(f.owner).FieldByIndex(f.index)
	if reflect.DeepEqual(own.Interface(), f.value.Interface()) {
		return finding
	}

	text := valueText(own)
	finding.Details = []string{
		fmt.Sprintf("%s defaults %s to %s on its own, so omitzero makes %s optional with %s at "+
			"its other places", f.owner.Name(), f.name, text, f.name, text),
	}

	return finding
}

// structField is a field that a decode of a struct reads, with its
// default at one place.
type structField struct {
	owner reflect.Type
	value reflect.Value
	name  string
	index []int
}

// fieldsOf returns each field that a decode of v, a struct of type t,
// reads by name, with the fields of each struct that t holds inline. It
// leaves out the fields that the decoder skips and those that it cannot
// set.
func fieldsOf(t reflect.Type, v reflect.Value) []structField {
	var out []structField

	for i := range t.NumField() {
		field := t.Field(i)

		name, inline, skip := yamlfield.Name(field)
		if skip || !v.Field(i).CanInterface() {
			continue
		}

		if !inline {
			out = append(out, structField{owner: t, value: v.Field(i), name: name, index: []int{i}})

			continue
		}

		inner := v.Field(i)
		if inner.Kind() == reflect.Pointer {
			if inner.IsNil() {
				continue
			}

			inner = inner.Elem()
		}

		if inner.Kind() == reflect.Struct && !unmarshaler.DecodesItself(inner.Type()) {
			out = append(out, fieldsOf(inner.Type(), inner)...)
		}
	}

	return out
}

// replacedWhole reports whether a document replaces a value of type t
// whole, rather than merging a mapping into it field by field, as it does
// a struct and a pointer to one.
func replacedWhole(t reflect.Type) bool {
	base := unmarshaler.Base(t)

	return base.Kind() != reflect.Struct || unmarshaler.DecodesItself(base)
}

// valueText returns v as the JSON text of a schema, such as 20 or "SEK".
func valueText(v reflect.Value) string {
	text, err := json.Marshal(v.Interface())
	if err != nil {
		return fmt.Sprint(v.Interface())
	}

	return string(text)
}
