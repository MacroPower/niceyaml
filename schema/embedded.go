package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"go.jacobcolvin.com/niceyaml"
)

// Embedded creates a [Resolver] that serves schema bytes already in
// memory, such as a schema embedded in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	r := schema.Embedded(schemaBytes)
//
// The [Ref.Key] is a digest of the bytes, so two Embedded resolvers over
// the same bytes name one schema to the registry, which compiles it once.
// A schema compiled already, with [MustCompile] or [FromJSONSchema], goes in
// through [Static] instead.
func Embedded(data []byte) Resolver {
	sum := sha256.Sum256(data)
	key := "embedded:" + hex.EncodeToString(sum[:])

	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		return Ref{
			Key: key,
			Load: func(_ context.Context) ([]byte, error) {
				return data, nil
			},
		}, nil
	})
}
