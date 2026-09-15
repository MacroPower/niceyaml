package registry

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// guarded applies a [schema.Resolver] only to documents a [matcher.Matcher]
// accepts.
type guarded struct {
	matcher  matcher.Matcher
	resolver schema.Resolver
}

// When creates a [schema.Resolver] that delegates to r for documents m
// accepts and reports [schema.ErrNoMatch] for the rest.
//
// This pairs a loader, which applies to every document, with a matcher that
// decides which documents it should apply to:
//
//	kindPath := paths.Root().Child("kind").Path()
//	reg.Register(registry.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    loader.Embedded("example.com/k8s/deployment.json", deploymentSchema),
//	))
//
// A resolver that decides and names the schema from the same parse, such as
// [Directive], implements [schema.Resolver] directly and needs no guard.
//
// Panics if m or r is nil.
func When(m matcher.Matcher, r schema.Resolver) schema.Resolver {
	if m == nil {
		panic("registry.When: matcher is nil")
	}

	if r == nil {
		panic("registry.When: resolver is nil")
	}

	return &guarded{matcher: m, resolver: r}
}

// Resolve implements [schema.Resolver].
func (g *guarded) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (schema.Ref, error) {
	if !g.matcher.Match(ctx, doc) {
		return schema.Ref{}, schema.ErrNoMatch
	}

	//nolint:wrapcheck // The guarded resolver's errors pass through unchanged.
	return g.resolver.Resolve(ctx, doc)
}
