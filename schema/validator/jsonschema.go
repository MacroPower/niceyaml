package validator

import (
	"encoding/json"
	"fmt"

	"go.jacobcolvin.com/x/jsonschema"
)

// defaultCompiler compiles schemas with [jsonschema.Compile] to implement
// [SchemaCompiler]. The compiled [*jsonschema.Validator] satisfies [Schema]
// directly.
type defaultCompiler struct {
	doc any
}

// newDefaultCompiler creates a new default [SchemaCompiler].
func newDefaultCompiler() *defaultCompiler {
	return &defaultCompiler{}
}

// AddResource implements [SchemaCompiler].
func (d *defaultCompiler) AddResource(_ string, doc any) error {
	d.doc = doc

	return nil
}

// Compile implements [SchemaCompiler].
func (d *defaultCompiler) Compile(_ string) (Schema, error) {
	raw, err := json.Marshal(d.doc)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	var s jsonschema.Schema

	err = json.Unmarshal(raw, &s)
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}

	v, err := jsonschema.Compile(&s)
	if err != nil {
		//nolint:wrapcheck // Transparent wrapper; errors handled by caller.
		return nil, err
	}

	return v, nil
}
