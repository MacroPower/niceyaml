package schema

import (
	"fmt"

	"github.com/invopop/jsonschema"
)

// JSON represents a JSON Schema object type.
// It is an alias for [github.com/invopop/jsonschema.Schema] to allow importing
// from this package instead of the underlying library.
//
// Types can implement schema extension by defining:
//
//	func (t MyType) JSONSchemaExtend(js *schema.JSON) {
//		f := schema.MustGetProperty("myField", js)
//		f.Description = "Custom description"
//		// ...
//	}
type JSON = jsonschema.Schema

// GetProperty retrieves a property from a JSON schema by name.
func GetProperty(name string, js *JSON) (*JSON, error) {
	v, ok := js.Properties.Get(name)
	if !ok {
		return nil, fmt.Errorf("property %q not found", name)
	}

	return v, nil
}

// MustGetProperty is like [GetProperty] but panics on error.
func MustGetProperty(name string, js *JSON) *JSON {
	v, err := GetProperty(name, js)
	if err != nil {
		panic(err)
	}

	return v
}

// PtrUint64 returns a pointer to the given uint64 value.
func PtrUint64(v uint64) *uint64 {
	return &v
}
