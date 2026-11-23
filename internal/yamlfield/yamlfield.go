// Package yamlfield holds the rules go-yaml follows when it decodes a
// mapping into a struct: the key each field reads, the fields it skips,
// and the fields it decodes inline.
//
// Code that pairs the fields of a decoded struct with the entries of the
// mapping they came from calls [Name] for each field. So it pairs them as
// the decoder does. The fields of an inline struct sit beside the
// fields of its parent. Go-yaml zeroes each such field whose name a field
// of the parent that is not inline also uses. [OwnNames] returns the
// names of the parent that zero such a field.
//
// Code that searches for a type the decoder can reach from another, such
// as a type with a method that a decode calls, uses [Reaches]. [Reaches]
// follows the fields that the decoder reads.
package yamlfield

import (
	"reflect"
	"slices"
	"strings"
)

// Name returns the name go-yaml decodes field under, whether the field
// is inline so its own fields sit beside its siblings, and whether
// go-yaml skips the field. It skips an unexported field that is not
// embedded, and one whose tag is "-". The name comes from the yaml tag,
// or the json tag when the field lacks a yaml tag. It is the lowercased
// field name when the tag that go-yaml reads does not name the field, as
// go-yaml spells it.
func Name(field reflect.StructField) (string, bool, bool) {
	tag, skip := fieldTag(field)
	if skip {
		return "", false, true
	}

	name := strings.ToLower(field.Name)

	options := strings.Split(tag, ",")
	if options[0] != "" {
		name = options[0]
	}

	return name, slices.Contains(options[1:], "inline"), false
}

// fieldTag returns the tag go-yaml reads for field, which is the yaml
// tag, or the json tag when the field lacks a yaml tag. The second result
// reports whether go-yaml skips the field.
func fieldTag(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" && !field.Anonymous {
		return "", true
	}

	tag := field.Tag.Get("yaml")
	if tag == "" {
		tag = field.Tag.Get("json")
	}

	return tag, tag == "-"
}

// OwnNames returns the names that go-yaml decodes the fields of t, a
// struct type, under. It leaves out the fields that go-yaml skips and the
// fields that are inline.
func OwnNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range t.Fields() {
		if name, inline, skip := Name(field); !skip && !inline {
			names[name] = true
		}
	}

	return names
}

// Reaches reports whether at finds a type that a decode into t reaches.
// Those types are t itself and each type that the decoder reaches from t
// through a pointer, an array or slice element, a map key or element, or
// a struct field. The search follows only the struct fields that go-yaml
// does not skip, as [Name] reports. For each type, at returns whether it
// found the type, and whether the search stops there without looking
// below it. The search reads each type once, so it ends for a type that
// holds itself.
func Reaches(t reflect.Type, at func(reflect.Type) (bool, bool)) bool {
	return reaches(t, at, map[reflect.Type]bool{})
}

// reaches is [Reaches] for a search that has read the types in seen.
func reaches(t reflect.Type, at func(reflect.Type) (bool, bool), seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}

	seen[t] = true

	if found, stop := at(t); found || stop {
		return found
	}

	switch t.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return reaches(t.Elem(), at, seen)

	case reflect.Map:
		return reaches(t.Key(), at, seen) || reaches(t.Elem(), at, seen)

	case reflect.Struct:
		for field := range t.Fields() {
			if _, _, skip := Name(field); !skip && reaches(field.Type, at, seen) {
				return true
			}
		}

		return false

	default:
		return false
	}
}
