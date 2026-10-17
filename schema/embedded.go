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
// same bytes name one schema to the registry. The registry compiles that
// schema once with the options that [WithCompileOptions] gave it.
// Embedded copies data, so a caller that writes to its slice afterward
// cannot make the key name different bytes. A [*Schema] is a [Resolver].
// A compiled schema, such as one held at package scope through
// [MustCompile], therefore goes into a registry as it is, not as its
// bytes.
func Embedded(data []byte) Ref {
	sum := sha256.Sum256(data)
	schemaData := bytes.Clone(data)

	// Each load hands out its own copy, so a caller that writes to the
	// bytes one load returned cannot change what a later load returns.
	return Loadable("embedded:"+hex.EncodeToString(sum[:]), func(_ context.Context) ([]byte, error) {
		return bytes.Clone(schemaData), nil
	})
}
