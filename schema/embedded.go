package schema

import (
	"bytes"
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
// same bytes name one schema to the registry, which compiles it once with
// the options [WithCompileOptions] gave it. Embedded copies data, so a
// caller that writes to its slice afterward cannot make the key name
// different bytes. A schema compiled already, such as one held at
// package scope through [MustCompile], goes into a registry as it is
// rather than as its bytes, since a [*Schema] is a [Resolver].
func Embedded(data []byte) Ref {
	sum := sha256.Sum256(data)
	schemaData := bytes.Clone(data)

	return Loadable("embedded:"+hex.EncodeToString(sum[:]), func(_ context.Context) ([]byte, error) {
		return schemaData, nil
	})
}
