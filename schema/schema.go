// Package schema provides utilities for working with JSON schemas.
package schema

import (
	"fmt"

	"github.com/invopop/jsonschema"
)

// GetProperty retrieves a property from a JSON schema by name.
func GetProperty(name string, js *jsonschema.Schema) (*jsonschema.Schema, error) {
	v, ok := js.Properties.Get(name)
	if !ok {
		return nil, fmt.Errorf("property %q not found", name)
	}

	return v, nil
}

// MustGetProperty is like [GetProperty] but panics on error.
func MustGetProperty(name string, js *jsonschema.Schema) *jsonschema.Schema {
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
