package schema

import (
	"errors"
	"fmt"

	"github.com/invopop/jsonschema"
)

// ErrPropertyNotFound indicates the requested property does not exist in the
// schema.
var ErrPropertyNotFound = errors.New("property not found")

// JSON represents a JSON Schema object.
//
// It is an alias for [jsonschema.Schema] to allow importing from this package
// instead of the underlying library.
//
// Types can implement schema extension by defining:
//
//	func (t MyType) JSONSchemaExtend(js *schema.JSON) {
//		f := schema.MustGetProperty("myField", js)
//		f.Description = "Custom description"
//		// ...
//	}
type JSON = jsonschema.Schema

// GetProperty retrieves a property from a [*JSON] schema by name.
// Returns [ErrPropertyNotFound] if the property does not exist.
func GetProperty(name string, js *JSON) (*JSON, error) {
	v, ok := js.Properties.Get(name)
	if !ok {
		return nil, fmt.Errorf("property %q: %w", name, ErrPropertyNotFound)
	}

	return v, nil
}

// MustGetProperty retrieves a property from a [*JSON] schema by name.
// It is like [GetProperty] but panics if the property does not exist.
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
