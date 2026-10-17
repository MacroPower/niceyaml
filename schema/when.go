package schema

import (
	"context"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// guarded applies a [Resolver] only to documents that a
// [matcher.Matcher] accepts.
type guarded struct {
	matcher  matcher.Matcher
	resolver Resolver
	// The error that Resolve returns for a document that the matcher
	// declines.
	decline error
}

// When creates a [Resolver] that delegates to r for documents that m
// accepts and reports [ErrNoMatch] for the rest. For a document that m
// cannot decide on, the Resolver returns the error from m, and that error
// ends the lookup of a registry.
//
// When pairs a [Ref], which applies to every document, with a matcher
// that decides which documents the Ref applies to:
//
//	kindPath := paths.Doc().Child("kind")
//	reg := schema.NewRegistry(schema.WithResolvers(schema.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    schema.Embedded(deploymentSchema),
//	)))
//
// A resolver that decides and names the schema from the same parse, such
// as [Directive], implements [Resolver] directly and does not need a
// guard.
//
// # Reasons
//
// A matcher that describes the documents that it accepts, through
// [matcher.Describer], gives that description as the reason of each
// decline. When every resolver declines a document, the error of the
// lookup then lists a line such as `applies only when $.kind is
// "Deployment"`. Each matcher of
// [go.jacobcolvin.com/niceyaml/schema/matcher] describes itself, except
// [matcher.Func], and [matcher.Described] gives any matcher a
// description. When reads the description of m as it builds the guard,
// not for each document. A matcher without a description declines with
// a bare [ErrNoMatch], and the error lists no line for it.
//
// A registry with many guards lists one line for each. A table of kinds
// reads better as one [ResolverFunc] that declines with the kind that it
// found, as [ErrNoMatch] describes.
//
// When panics if m or r is nil, including a nil pointer, a nil
// [matcher.Func], or a nil [ResolverFunc].
func When(m matcher.Matcher, r Resolver) Resolver {
	if nilness.IsNil(m) {
		panic("schema.When: matcher is nil")
	}

	if nilness.IsNil(r) {
		panic("schema.When: resolver is nil")
	}

	decline := ErrNoMatch

	if d, ok := m.(matcher.Describer); ok {
		if desc := d.Describe(); desc != "" {
			decline = fmt.Errorf("%w: applies only when %s", ErrNoMatch, desc)
		}
	}

	return &guarded{matcher: m, resolver: r, decline: decline}
}

// Resolve implements [Resolver].
func (g *guarded) Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error) {
	ok, err := g.matcher.Match(ctx, doc)
	if err != nil {
		//nolint:wrapcheck // The matcher's error passes through as the resolver's.
		return Ref{}, err
	}

	if !ok {
		return Ref{}, g.decline
	}

	//nolint:wrapcheck // The guarded resolver's errors pass through unchanged.
	return g.resolver.Resolve(ctx, doc)
}
