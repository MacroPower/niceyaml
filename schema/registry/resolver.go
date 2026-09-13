package registry

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Resolver finds the schema for a document in one step.
//
// Resolve returns the schema data for doc, or an error wrapping [ErrNoMatch]
// when the resolver does not apply to the document. [Registry] tries its
// resolvers in registration order and moves past each one that reports
// ErrNoMatch, so a resolver decides whether it matches and loads the schema
// in the same call and never needs to carry state between calls. Any other
// error stops the lookup.
//
// Register with [Registry.Register]. For a separate [matcher.Matcher] and
// [loader.Loader], use [Registry.RegisterFunc], which pairs them into a
// Resolver.
//
// See [Directive] and [schemastore.SchemaStore] for implementations.
type Resolver interface {
	Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
type ResolverFunc func(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	return f(ctx, doc)
}

// pair adapts a [matcher.Matcher] and a [loader.Loader] into a [Resolver].
type pair struct {
	matcher matcher.Matcher
	loader  loader.Loader
}

// Resolve loads through the loader when the matcher accepts doc and reports
// [ErrNoMatch] otherwise.
func (p *pair) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	if !p.matcher.Match(ctx, doc) {
		return loader.Result{}, ErrNoMatch
	}

	//nolint:wrapcheck // Errors wrapped by Registry.
	return p.loader.Load(ctx, doc)
}
