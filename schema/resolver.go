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

// Ref is the schema a [Resolver] names for a document: a key and a
// function that loads the bytes to compile, from [Loadable], which
// [Embedded] builds, a file the registry reads, from [File], an HTTP URL
// the registry fetches, from [URL], or a [*Schema] compiled already, from
// [Schema.Ref]. [FileOrURL] builds a file or a URL Ref from a reference
// as written.
//
// A Ref is itself a [Resolver] that names its schema for every document,
// so one goes into [WithResolvers] or [When] as it is, and a resolver that
// picks a schema from the document returns one:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    node, err := doc.At(kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    kind, err := node.Decode[string](ctx)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    return schema.File("schemas/" + kind + ".json"), nil
//	})
//
// A Ref is data, and the registry is where the bytes move. It checks its
// cache by [Ref.Key] first, and loads and compiles the schema only on a
// cache miss, so a load that succeeds runs once per key however many
// documents name it. The registry compiles every schema with the options
// [WithCompileOptions] gave it. [Registry.Load] reads a file from the file
// system [WithFS] gave the registry and fetches a URL with the client
// [WithHTTPClient] gave it, so one file system and one client serve every
// Ref its resolvers name. A Ref that carries a compiled schema has nothing
// to load, and the registry validates with the schema as it is.
//
// The zero Ref names no schema. Return it beside an error, as a resolver
// does with [ErrNoMatch].
type Ref struct {
	load func(ctx context.Context) ([]byte, error)
	// The compiled schema the Ref carries, from Schema.Ref, which the
	// registry validates with as it is.
	schema *Schema
	key    string
	// The file the registry reads, as given to File, which is relative to
	// the working directory or to the file system of the registry.
	file string
	// The file made absolute against the working directory as File made
	// it to build the key, which a read from the working directory uses,
	// so the bytes under the key stay the same wherever the read happens.
	abs string
	// The key is an HTTP URL the registry fetches with its client.
	url bool
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

// Key returns the key of a [Ref] from [Loadable], [File], or [URL], or
// "" for the zero Ref and for a Ref from [Schema.Ref], which names no
// bytes to cache.
func (r Ref) Key() string {
	return r.key
}

// Schema returns the compiled schema of a [Ref] from [Schema.Ref], or nil
// for a Ref that names bytes to load. [Registry.Schema] returns it as it
// is for such a Ref, and a caller that loads bytes without a registry
// checks it before [Registry.Load], as the registry does.
func (r Ref) Schema() *Schema {
	return r.schema
}

// Resolve implements [Resolver]. It names the Ref's schema for every
// document and never reports [ErrNoMatch].
func (r Ref) Resolve(_ context.Context, _ *niceyaml.Node) (Ref, error) {
	return r, nil
}

// Resolver finds the schema for a document.
//
// Resolve returns a [Ref] naming the schema for doc, such as one from
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
	Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
//
//	kindPath := paths.Root().Child("kind")
//	r := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    node, err := doc.At(kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    kind, err := node.Decode[string](ctx)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    return schema.File("schemas/" + strings.ToLower(kind) + ".json"), nil
//	})
type ResolverFunc func(ctx context.Context, doc *niceyaml.Node) (Ref, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error) {
	return f(ctx, doc)
}
