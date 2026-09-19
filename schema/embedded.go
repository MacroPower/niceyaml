package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Embedded creates a [Ref] that serves schema bytes already in memory,
// such as a schema embedded in the binary with go:embed. The Ref is a
// [Resolver] that names the bytes for every document:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	r := schema.Embedded(schemaBytes)
//
// The [Ref.Key] is a digest of the bytes, so two Embedded Refs over the
// same bytes name one schema to the registry, which compiles it once. A
// schema compiled already, with [MustCompile] or [FromJSONSchema], is a
// [Resolver] itself and goes in as it is.
func Embedded(data []byte) Ref {
	sum := sha256.Sum256(data)

	return Loadable("embedded:"+hex.EncodeToString(sum[:]), func(_ context.Context) ([]byte, error) {
		return data, nil
	})
}
