package niceyaml

import (
	"reflect"

	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

// FieldKey returns the key that a decode reads field from. A check that
// knows a failing value by its Go field thus names the key that an `@`
// path must hold.
//
// The key comes from the yaml tag, or from the json tag when field lacks
// a yaml tag. It is the lowercased field name when that tag does not name
// the field. The second result reports an inline field, whose own fields
// sit beside its siblings, so a path leaves out the key of that field.
// The third result is false for a field that the decode skips. The decode
// skips an unexported field that is not embedded and a field whose tag is
// "-".
//
// A field tagged `yaml:",inline,alias"` reads the anchor that a `<<` key
// of the mapping names, not the keys of the mapping. A path below such a
// field thus does not name a key of its parent.
//
// FieldKey names one field. A check that builds a longer path follows
// pointers, elements, and inline fields itself.
func FieldKey(field reflect.StructField) (string, bool, bool) {
	key, inline, skip := yamlfield.Name(field)

	return key, inline, !skip
}
