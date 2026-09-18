package schema

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// guarded applies a [Resolver] only to documents a [matcher.Matcher]
// accepts.
type guarded struct {
	matcher  matcher.Matcher
	resolver Resolver
}

// When creates a [Resolver] that delegates to r for documents m
// accepts and reports [ErrNoMatch] for the rest.
//
// This pairs a loader, which applies to every document, with a matcher that
// decides which documents it should apply to:
//
//	kindPath := paths.Root().Child("kind")
//	reg.Register(schema.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    schema.Embedded(deploymentSchema),
//	))
//
// A resolver that decides and names the schema from the same parse, such as
// [Directive], implements [Resolver] directly and needs no guard.
//
// Panics if m or r is nil.
func When(m matcher.Matcher, r Resolver) Resolver {
	if m == nil {
		panic("schema.When: matcher is nil")
	}

	if r == nil {
		panic("schema.When: resolver is nil")
	}

	return &guarded{matcher: m, resolver: r}
}

// Resolve implements [Resolver].
func (g *guarded) Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error) {
	if !g.matcher.Match(ctx, doc) {
		return Ref{}, ErrNoMatch
	}

	//nolint:wrapcheck // The guarded resolver's errors pass through unchanged.
	return g.resolver.Resolve(ctx, doc)
}
