package manifest

import (
	"reflect"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

// checkInline reports each field of a struct that the decode of the kind
// of entry reads field by field, where the decode and the schema generator
// of go.jacobcolvin.com/x/jsonschema disagree on whether the field is
// inline. The generator follows encoding/json and reads an embedded struct
// without a JSON name inline, and the decode does so only for a field with
// an inline option.
func (b *registryBuilder) checkInline(entry *kindEntry) {
	k := entry.kind

	yamlfield.Reaches(k.typ, func(t reflect.Type) (bool, bool) {
		if unmarshaler.DecodesItself(t) {
			return false, true
		}

		if t.Kind() != reflect.Struct {
			return false, false
		}

		for f := range t.Fields() {
			_, inline, skip := yamlfield.Name(f)
			if skip {
				continue
			}

			promoted := f.Anonymous && jsonName(f) == "" && isStruct(f.Type)

			switch {
			case promoted && !inline:
				b.failf("kind %s: %s.%s: embedded struct needs yaml:\",inline\", because gen reads it inline "+
					"and the decode nests it", k.name, typeName(t), f.Name)

			case inline && !f.Anonymous:
				b.failf("kind %s: %s.%s: named field has an inline option, which the decode reads inline "+
					"and gen nests", k.name, typeName(t), f.Name)

			case inline && !promoted:
				b.failf("kind %s: %s.%s: embedded field has a JSON name and an inline option, which the decode "+
					"reads inline and gen nests", k.name, typeName(t), f.Name)
			}
		}

		return false, false
	})
}

// jsonName returns the name that the json tag of f gives it, or "" when
// the tag names none.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")

	return name
}

// isStruct reports whether t is a struct or a pointer to one, the types
// whose fields encoding/json promotes from an embedded field.
func isStruct(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t.Kind() == reflect.Struct
}

// typeName returns the name of t as a message of [NewRegistry] shows it.
func typeName(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}

	return t.String()
}
