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

// Ref is the schema a [Resolver] names for a document: a [*Validator]
// compiled already, or a key and a function that loads the bytes to
// compile.
//
// A Ref with a Validator is complete. The registry uses the validator as it
// is, and Key and Load play no part, so a schema compiled at package scope
// with [MustCompile], or built from a Go type and wrapped with
// [NewValidator], goes into a registry without a round trip through bytes.
// [Static] returns such a Ref for every document.
//
// A Ref without a Validator carries a Key and a Load. The registry checks
// its cache by Key before any bytes move, and Load runs only on a cache
// miss, so the two fields carry different obligations. Key must be cheap to
// produce and must identify the schema uniquely, since two Refs with the
// same Key are the same schema to every cache keyed on it. Load may be
// expensive, may fail, and must return the same bytes each time it is
// called for the same Key.
type Ref struct {
	// Validator is the schema, compiled. When set, the registry uses it as
	// it is and never calls Load.
	Validator *Validator

	// Load returns the schema bytes to compile. The registry calls it on a
	// cache miss and caches the compiled result, so a load that succeeds
	// runs once per Key.
	Load func(ctx context.Context) ([]byte, error)

	// Key identifies the schema and is the cache key. It is a name, not
	// necessarily a fetchable address: [URL] uses the URL, [File] the file
	// URL of the absolute path, and [Embedded] a digest of the bytes. The
	// registry rejects an empty Key on a Ref without a Validator.
	Key string
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
// ignore the document and always name the same schema. The document is
// never nil, so a resolver reads it without checking. [Static] and the
// loaders [Embedded], [File], [URL], and [FileOrURL] are resolvers of the
// second kind, and [When] guards any resolver with a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher].
//
// See [ResolverFunc], [Directive], and
// [go.jacobcolvin.com/niceyaml/schema/schemastore.SchemaStore] for
// implementations.
type Resolver interface {
	Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
//
//	kindPath := paths.Root().Child("kind")
//	r := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Document) (schema.Ref, error) {
//	    kind, err := doc.GetValue(kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    name := "schemas/" + strings.ToLower(kind) + ".json"
//
//	    return schema.Ref{
//	        Key:  name,
//	        Load: func(context.Context) ([]byte, error) { return schemaFS.ReadFile(name) },
//	    }, nil
//	})
type ResolverFunc func(ctx context.Context, doc *niceyaml.Document) (Ref, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error) {
	return f(ctx, doc)
}
