package schema

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Static creates a [Resolver] that names s, a schema compiled already, for
// every document. It is the way into a [Registry] for a schema held at
// package scope, or one built from a Go type and wrapped with
// [FromJSONSchema]:
//
//	//go:embed config.schema.json
//	var schemaJSON []byte
//
//	var Config = schema.MustCompile(schemaJSON)
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.When(matcher.Content(kindPath, "Config"), schema.Static(Config)),
//	))
//
// The registry uses s as it is, so the [CompileOption] values from
// [WithCompileOptions] do not reach it. Schema bytes that are not compiled
// yet go in through [Embedded], which compiles them with those options.
//
// Panics if s is nil.
func Static(s *Schema) Resolver {
	if s == nil {
		panic("schema.Static: schema is nil")
	}

	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		return Ref{Schema: s}, nil
	})
}
