package registry

import (
	"context"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/schema/loader"
	"jacobcolvin.com/niceyaml/schema/matcher"
)

// MatchLoader combines matching and loading into a single type.
//
// [Registry] calls Match first, then Load if matched. Implementations may
// cache intermediate results between these calls to avoid redundant work.
//
// Contract: [Registry] guarantees it passes the same [*niceyaml.DocumentDecoder]
// pointer to both Match and Load for a given document. Implementations may
// rely on this guarantee to cache expensive parse results (like [Directive]
// does for token parsing).
//
// Register with [Registry.Register]. For separate [matcher.Matcher] and
// [loader.Loader] implementations that do not share state, use
// [Registry.RegisterFunc].
//
// See [Directive] and [schemastore.SchemaStore] for implementations.
type MatchLoader interface {
	matcher.Matcher
	loader.Loader
}

// matchLoaderWrapper wraps separate Matcher and Loader into a MatchLoader.
type matchLoaderWrapper struct {
	matcher matcher.Matcher
	loader  loader.Loader
}

// Match delegates to the wrapped matcher.
func (w *matchLoaderWrapper) Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
	return w.matcher.Match(ctx, doc)
}

// Load delegates to the wrapped loader.
func (w *matchLoaderWrapper) Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	//nolint:wrapcheck // Errors wrapped by Registry.
	return w.loader.Load(ctx, doc)
}
