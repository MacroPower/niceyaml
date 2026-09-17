package schema

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Embedded creates a [Resolver] that serves schema bytes already in
// memory, such as a schema embedded in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	r := schema.Embedded("example.com/config/schema.json", schemaBytes)
//
// The schemaURL is the registry's cache key and need not be fetchable, but
// it must be unique among the schemas one registry sees. A bare file name
// collides when two packages each embed their own "schema.json", so prefix
// it with something package-specific, such as the module path.
func Embedded(schemaURL string, data []byte) Resolver {
	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		return Ref{
			URL: schemaURL,
			Load: func(_ context.Context) ([]byte, error) {
				return data, nil
			},
		}, nil
	})
}
