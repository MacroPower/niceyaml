package schema

import (
	"context"
	"errors"

	"go.jacobcolvin.com/niceyaml"
)

// ErrNoMatch reports that a [Resolver] does not apply to a document. A
// registry moves on to its next resolver when Resolve returns an error
// wrapping ErrNoMatch, and reports it to the caller when no resolver
// applies.
var ErrNoMatch = errors.New("no matching schema")

// Ref names a schema by URL and loads its bytes on demand.
//
// A [Resolver] returns a Ref so a registry can check its cache by URL before
// any bytes move; Load runs only on a cache miss. The two fields therefore
// carry different obligations. URL must be cheap to produce and must
// identify the schema uniquely, since two Refs with the same URL are the
// same schema to every cache keyed on it. Load may be expensive, may fail,
// and must return the same bytes each time it is called for the same URL.
type Ref struct {
	// Load returns the schema bytes to compile. The registry calls it on a
	// cache miss and caches the compiled result, so a load that succeeds runs
	// once per URL.
	Load func(ctx context.Context) ([]byte, error)

	// URL identifies the schema and is the cache key. It is a name,
	// not necessarily a fetchable address. An embedded schema may use its
	// file name, but two packages that embed different schemas under the
	// same file name must choose distinct URLs, for example by prefixing
	// the package path. The registry rejects an empty URL.
	URL string
}

// Resolver finds the schema for a document.
//
// Resolve returns a [Ref] naming the schema for doc, or an error wrapping
// [ErrNoMatch] when the resolver does not apply to the document. A registry
// tries its resolvers in registration order and moves past each one that
// reports ErrNoMatch, so a resolver decides whether it applies and names the
// schema in the same call. Any other error stops the lookup.
//
// A resolver may inspect the document's content, file path, or tokens, or
// ignore the document and always name the same schema. The loaders in
// [go.jacobcolvin.com/niceyaml/schema/loader] are resolvers of the second
// kind, and [go.jacobcolvin.com/niceyaml/schema/registry.When] guards any
// resolver with a [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher].
//
// See [ResolverFunc], [go.jacobcolvin.com/niceyaml/schema/registry.Directive],
// and [go.jacobcolvin.com/niceyaml/schema/registry/schemastore.SchemaStore]
// for implementations.
type Resolver interface {
	Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (Ref, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
//
//	kindPath := paths.Root().Child("kind")
//	r := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.DocumentDecoder) (schema.Ref, error) {
//	    kind, err := doc.GetValue(kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    name := "schemas/" + strings.ToLower(kind) + ".json"
//
//	    return schema.Ref{
//	        URL:  name,
//	        Load: func(context.Context) ([]byte, error) { return schemaFS.ReadFile(name) },
//	    }, nil
//	})
type ResolverFunc func(ctx context.Context, doc *niceyaml.DocumentDecoder) (Ref, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (Ref, error) {
	return f(ctx, doc)
}
