package loader

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Embedded creates a [Loader] that returns embedded schema bytes.
//
// Use this for schemas embedded in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	l := loader.Embedded("schema.json", schemaBytes)
func Embedded(schemaURL string, data []byte) Loader {
	return Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (Result, error) {
		return NewResult(schemaURL, data), nil
	})
}
