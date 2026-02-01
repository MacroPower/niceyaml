package loader

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Validator creates a [Loader] that returns a pre-compiled
// [niceyaml.SchemaValidator].
//
// This bypasses schema loading and compilation, returning the validator
// directly. Useful for sharing pre-compiled validators across registrations.
//
//	v := validator.MustNew("schema.json", schemaBytes)
//	l := loader.Validator("schema.json", v)
func Validator(schemaURL string, v niceyaml.SchemaValidator) Loader {
	return Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (Result, error) {
		return NewResultWithValidator(schemaURL, v), nil
	})
}
