package schema

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrNoMatch reports that a [Resolver] does not apply to a document. A
// registry moves on to its next resolver when Resolve returns an error
// wrapping ErrNoMatch, and reports it to the caller when no resolver
// applies.
var ErrNoMatch = errors.New("no matching schema")

// Ref is the schema a [Resolver] names for a document: a [*Schema]
// compiled already, from [Compiled]; a key and a function that loads the
// bytes to compile, from [Loadable], which [Embedded] and [File] build; or
// an HTTP URL the registry fetches, from [URL]. [FileOrURL] builds one of
// the last two.
//
// A Ref is itself a [Resolver] that names its schema for every document,
// so one goes into [WithResolvers] or [When] as it is, and a resolver that
// picks a schema from the document returns one:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
//	    kind, err := doc.Get[string](ctx, kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    return schema.File("schemas/" + kind + ".json")
//	})
//
// The registry uses a compiled schema as it is. For any other Ref it
// checks its cache by [Ref.Key] before any bytes move, and loads the
// schema only on a cache miss, so a load that succeeds runs once per key
// however many documents name it. The registry fetches a URL with the
// client [WithHTTPClient] gave it, so one client serves every URL its
// resolvers name.
//
// The zero Ref names no schema. Return it beside an error, as a resolver
// does with [ErrNoMatch].
type Ref struct {
	schema *Schema
	load   func(ctx context.Context) ([]byte, error)
	key    string
	// The key is an HTTP URL the registry fetches with its client.
	url bool
}

// Compiled creates a new [Ref] that carries s, a schema compiled already,
// such as one built at package scope with [MustCompile] or from a Go type
// and wrapped with [FromJSONSchema]. The registry uses s as it is, so the
// [CompileOption] values from [WithCompileOptions] do not reach it.
//
// Panics if s is nil.
func Compiled(s *Schema) Ref {
	if s == nil {
		panic("schema.Compiled: schema is nil")
	}

	return Ref{schema: s}
}

// Loadable creates a new [Ref] that names a schema by key and loads its
// bytes with load on demand.
//
// The key must identify the schema uniquely, since two Refs with the same
// key are one schema to every registry that caches on it. It is a name,
// not necessarily a fetchable address: [URL] uses the URL, [File] the
// file URL of the absolute path, and [Embedded] a digest of the bytes.
// The load may be expensive and may fail, and must return the same bytes
// each time it runs for one key.
//
// Panics if key is empty or load is nil.
func Loadable(key string, load func(ctx context.Context) ([]byte, error)) Ref {
	if key == "" {
		panic("schema.Loadable: key is empty")
	}

	if load == nil {
		panic("schema.Loadable: load is nil")
	}

	return Ref{key: key, load: load}
}

// Schema returns the compiled schema of a [Ref] from [Compiled], or nil
// for any other Ref.
func (r Ref) Schema() *Schema {
	return r.schema
}

// Key returns the key of a [Ref] from [Loadable] or [URL], or "" for any
// other Ref.
func (r Ref) Key() string {
	return r.key
}

// Load returns the schema bytes of a [Ref] from [Loadable], or fetches
// those of a Ref from [URL] with [http.DefaultClient]. A [Registry] loads
// the same bytes itself and fetches a URL with the client [WithHTTPClient]
// gave it, so Load is for a caller that loads schemas without one. A Ref
// from [Compiled], or the zero Ref, carries no loader, and Load then
// returns an error wrapping [ErrLoad].
func (r Ref) Load(ctx context.Context) ([]byte, error) {
	return r.fetch(ctx, http.DefaultClient)
}

// fetch is [Ref.Load] with the client that fetches a Ref from [URL].
func (r Ref) fetch(ctx context.Context, client *http.Client) ([]byte, error) {
	switch {
	case r.url:
		//nolint:wrapcheck // The fetch error names the URL already.
		return httpfetch.Get(ctx, client, r.key)

	case r.load != nil:
		return r.load(ctx)

	default:
		return nil, fmt.Errorf("%w: ref carries no loader", ErrLoad)
	}
}

// Resolve implements [Resolver]. It names the Ref's schema for every
// document and never reports [ErrNoMatch].
func (r Ref) Resolve(_ context.Context, _ *niceyaml.Document) (Ref, error) {
	return r, nil
}

// Resolver finds the schema for a document.
//
// Resolve returns a [Ref] naming the schema for doc, from [Compiled] or
// [Loadable], or an error wrapping [ErrNoMatch] when the resolver does not
// apply to the document. A registry
// tries its resolvers in the order given and moves past each one that
// reports ErrNoMatch, so a resolver decides whether it applies and names the
// schema in the same call. Any other error stops the lookup.
//
// A resolver may inspect the document's content, file path, or tokens, or
// ignore the document and always name the same schema. The document is
// never nil, so a resolver reads it without checking. A [Ref] and a
// [*Schema] are resolvers of the second kind, and [When] guards any
// resolver with a [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher].
//
// See [ResolverFunc], [Ref], [Schema], [Directive], and
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] for
// implementations.
type Resolver interface {
	Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
//
//	kindPath := paths.Root().Child("kind")
//	r := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
//	    kind, err := doc.Get[string](ctx, kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    return schema.File("schemas/" + strings.ToLower(kind) + ".json")
//	})
type ResolverFunc func(ctx context.Context, doc *niceyaml.Document) (Ref, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error) {
	return f(ctx, doc)
}
