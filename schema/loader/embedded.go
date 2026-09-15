package loader

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Embedded creates a [schema.Resolver] that serves schema bytes already in
// memory, such as a schema embedded in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	r := loader.Embedded("example.com/config/schema.json", schemaBytes)
//
// The schemaURL is the registry's cache key and need not be fetchable, but
// it must be unique among the schemas one registry sees. A bare file name
// collides when two packages each embed their own "schema.json", so prefix
// it with something package-specific, such as the module path.
func Embedded(schemaURL string, data []byte) schema.Resolver {
	return schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
		return schema.Ref{
			URL: schemaURL,
			Load: func(_ context.Context) ([]byte, error) {
				return data, nil
			},
		}, nil
	})
}
