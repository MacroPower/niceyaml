package schema

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// guarded applies a [Resolver] only to documents a [matcher.Matcher]
// accepts.
type guarded struct {
	matcher  matcher.Matcher
	resolver Resolver
}

// When creates a [Resolver] that delegates to r for documents m
// accepts, reports [ErrNoMatch] for the rest, and returns the error of a
// matcher that cannot decide, which ends a registry's lookup.
//
// This pairs a [Ref], which applies to every document, with a matcher that
// decides which documents it should apply to:
//
//	kindPath := paths.Root().Child("kind")
//	reg := schema.NewRegistry(schema.WithResolvers(schema.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    schema.Embedded(deploymentSchema),
//	)))
//
// A resolver that decides and names the schema from the same parse, such as
// [Directive], implements [Resolver] directly and needs no guard.
//
// Panics if m or r is nil, including a nil pointer, a nil [matcher.Func],
// or a nil [ResolverFunc].
func When(m matcher.Matcher, r Resolver) Resolver {
	if nilness.IsNil(m) {
		panic("schema.When: matcher is nil")
	}

	if nilness.IsNil(r) {
		panic("schema.When: resolver is nil")
	}

	return &guarded{matcher: m, resolver: r}
}

// Resolve implements [Resolver].
func (g *guarded) Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error) {
	ok, err := g.matcher.Match(ctx, doc)
	if err != nil {
		//nolint:wrapcheck // The matcher's error passes through as the resolver's.
		return Ref{}, err
	}

	if !ok {
		return Ref{}, ErrNoMatch
	}

	//nolint:wrapcheck // The guarded resolver's errors pass through unchanged.
	return g.resolver.Resolve(ctx, doc)
}
