package schema

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Static creates a [Resolver] that names v, a schema compiled already, for
// every document. It is the way into a [Registry] for a validator held at
// package scope, or one built from a Go type and wrapped with
// [NewValidator]:
//
//	//go:embed config.schema.json
//	var schemaJSON []byte
//
//	var Schema = schema.MustCompile(schemaJSON)
//
//	reg.Register(schema.When(matcher.Content(kindPath, "Config"), schema.Static(Schema)))
//
// The registry uses v as it is, so the [CompileOption] values from
// [WithCompileOptions] do not reach it. Schema bytes that are not compiled
// yet go in through [Embedded], which compiles them with those options.
//
// Panics if v is nil.
func Static(v *Validator) Resolver {
	if v == nil {
		panic("schema.Static: validator is nil")
	}

	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		return Ref{Validator: v}, nil
	})
}
