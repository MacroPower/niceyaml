package schema

import (
	"context"
	"errors"
	"io/fs"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrNoMatch reports that a [Resolver] does not apply to a document. A
// registry tries its next resolver when Resolve returns an error wrapping
// ErrNoMatch, and reports the error to the caller when no resolver
// applies. The document then does not name a schema that the registry
// knows, so [niceyaml.IsInvalid] reports true for the error that the
// registry returns.
//
// A resolver says why it declines by putting ErrNoMatch first and its
// reason after it:
//
//	return schema.Ref{}, fmt.Errorf("%w: no schema for kind %q", schema.ErrNoMatch, kind)
//
// When every resolver declines, the error of the registry lists the text
// after "no matching schema: " as the reason of each resolver. [When]
// declines with the description of its matcher. A bare ErrNoMatch gives
// no reason, and the error lists no line for it. A reason that comes
// before the sentinel keeps the sentinel, so the format "no kind: %w"
// gives the line "no kind: no matching schema".
//
// The error of the registry matches each error that a decline wraps. A
// sentinel that a resolver declines with, such as [ErrNoDirective], can
// wrap ErrNoMatch, or sit beside it:
//
//	return schema.Ref{}, fmt.Errorf("%w: %w", schema.ErrNoMatch, errNoKind)
//
// Either way [errors.Is] finds the sentinel in the error of the
// registry. The error of a context is the exception. A resolver whose
// own deadline passed can decline with that error, but the error of the
// registry matches neither [context.Canceled] nor
// [context.DeadlineExceeded]. The lookup went on past that resolver, so
// the document stays at fault.
var ErrNoMatch = errors.New("no matching schema")

// Ref is the schema that a [Resolver] names for a document. A Ref from
// [Loadable] or [Embedded] holds a key and a function that loads the
// bytes to compile. A Ref from [File] names a file that the registry
// reads from disk. One from [FileFS] names a file in a file system, and
// one from [URL] names an HTTP URL that the registry fetches. A Ref from
// [Schema.Ref] carries a [*Schema] compiled already. [FileOrURL] builds a
// file or a URL Ref from a reference as written, and [RefBeside] builds
// one for a reference that belongs to a document.
//
// A Ref is itself a [Resolver] that names its schema for every document,
// so a Ref goes into [WithResolvers] or [When] as it is. A resolver that
// picks a schema from the document returns a Ref:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    var kind string
//
//	    found, err := doc.DecodeIfPresent(ctx, kindPath, &kind)
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    if !found {
//	        return schema.Ref{}, fmt.Errorf("%w: document has no kind", schema.ErrNoMatch)
//	    }
//
//	    // The document picks kind, so keep it to a file name in schemas/.
//	    if strings.ContainsAny(kind, `/\`) {
//	        return schema.Ref{}, fmt.Errorf("kind %q: not a schema name", kind)
//	    }
//
//	    return schema.File("schemas/" + kind + ".json"), nil
//	})
//
// A Ref is data, and the registry loads the bytes that the Ref names.
// The registry checks its cache by [Ref.Key] first, and loads and compiles
// the schema only on a cache miss. After a schema compiles, the registry
// serves it to every later document that names it without loading it
// again. The registry remembers a failed load or compile for the interval
// that [WithRetryAfter] sets. The first document that names the Key after
// the interval loads it again.
//
// The registry caches a Ref from [FileFS] by its file system and its path
// together, so one path in two file systems is two schemas. The registry
// compiles every schema with the options that [WithCompileOptions] gave
// it. [Registry.Load] reads a file from the file system that its Ref
// names, or from disk. On disk, the [Access] of the registry decides
// which files it reads. Load fetches a URL with the client that
// [WithHTTPClient] gave the registry. A Ref that carries a compiled
// schema does not have anything to load, and the registry validates with
// the schema as it is.
//
// Create instances with [Loadable], [Embedded], [File], [FileFS], [URL],
// [FileOrURL], [RefBeside], or [Schema.Ref]. The zero Ref does not name a
// schema. Return it beside an error, as a resolver does with
// [ErrNoMatch].
type Ref struct {
	load func(ctx context.Context) ([]byte, error)
	// The compiled schema that the Ref carries, from Schema.Ref. The
	// registry validates with it as it is.
	schema *Schema
	// Where the directive that named the Ref stands, or nil for a Ref
	// that no directive named. A lookup of the document that holds the
	// directive locates the load or compile error of the schema there.
	at  *directiveSite
	key string
	// The path of the file, as given to File or FileFS. For a Ref that
	// names the file in a file system, the registry reads this path from
	// that file system and names the Ref by it in messages.
	file string
	// The file system that the file lives in, or nil for a file on disk.
	fsys fs.FS
	// The reason that File could not make the path absolute, or nil. When
	// it is set, abs is empty, and a read of the Ref from disk reports it.
	absErr error
	// The path of the file that File made absolute against the working
	// directory to build the key, or empty for a Ref with a file system.
	// Every read from disk uses it, through a root of AllowRoot or not.
	// The bytes under the key thus stay the same whatever the working
	// directory is at the time of the read.
	abs string
	// Whether the key is an HTTP URL that the registry fetches with its
	// client.
	url bool
}

// Loadable creates a new [Ref] that names a schema by key and loads its
// bytes with load on demand.
//
// The key must identify the schema uniquely, because two Refs with the
// same key are one schema to every registry that caches on it. The key is
// a name, not necessarily a fetchable address. [URL] uses the URL, [File]
// the file URL of the absolute path, and [Embedded] a digest of the bytes.
// [FileFS] uses the file URL of the path in its file system, and the
// registry tells two file systems apart itself. The load might be
// expensive or fail, but it must return the same bytes each time it runs
// for one key.
//
// Loadable panics if key is empty or load is nil.
func Loadable(key string, load func(ctx context.Context) ([]byte, error)) Ref {
	if key == "" {
		panic("schema.Loadable: key is empty")
	}

	if load == nil {
		panic("schema.Loadable: load is nil")
	}

	return Ref{key: key, load: load}
}

// Key returns the key of a [Ref] from [Loadable], [File], or [URL]. It
// returns "" for the zero Ref and for a Ref from [Schema.Ref], which does
// not name bytes to cache.
func (r Ref) Key() string {
	return r.key
}

// Name returns the name that the errors of a [Registry] give r. The name
// is the [Ref.Key] with any password in the userinfo of a URL replaced by
// "xxxxx", so the errors do not print the password. A Ref that names its
// file in a file system, such as one from [FileFS], goes by the path as
// given, with the fragment of its key. The file system reads that path.
func (r Ref) Name() string {
	if r.file != "" && r.fsys != nil {
		return r.file + fileFragment(r)
	}

	return httpfetch.Redacted(r.key)
}

// Schema returns the compiled schema of a [Ref] from [Schema.Ref], or nil
// for a Ref that names bytes to load. [Registry.Schema] returns it as it
// is for such a Ref. A caller that loads bytes without a registry checks
// it before [Registry.Load], as the registry does.
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
// Resolve returns a [Ref] that names the schema for doc, such as one from
// [Loadable]. When the resolver does not apply to the document, Resolve
// returns an error wrapping [ErrNoMatch], with its reason after the
// sentinel as ErrNoMatch describes.
//
// A registry tries its resolvers in the order given and moves past each
// one that reports ErrNoMatch. A resolver thus decides whether it applies
// and names the schema in the same call. Any other error stops the
// lookup. An error that wraps ErrNoMatch but also matches [ErrResolve],
// [ErrLoad], or [ErrCompile] stops the lookup too, and the registry
// returns it without a second ErrResolve.
//
// A resolver can read the document's content, file path, or tokens, or
// it can ignore the document and always name the same schema. The
// document is never nil, so a resolver reads it without checking. A [Ref]
// and a [*Schema] are resolvers that ignore the document, and each always
// names its own schema. [When] guards any resolver with a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher].
//
// See [ResolverFunc], [Ref], [Schema], [Directive], [*Registry], and
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] for
// implementations.
type Resolver interface {
	Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error)
}

// ResolverFunc adapts a function to the [Resolver] interface.
//
//	kindPath := paths.Doc().Child("kind")
//	r := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    var kind string
//
//	    found, err := doc.DecodeIfPresent(ctx, kindPath, &kind)
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    if !found {
//	        return schema.Ref{}, fmt.Errorf("%w: document has no kind", schema.ErrNoMatch)
//	    }
//
//	    // The document picks kind, so keep it to a file name in schemas/.
//	    if strings.ContainsAny(kind, `/\`) {
//	        return schema.Ref{}, fmt.Errorf("kind %q: not a schema name", kind)
//	    }
//
//	    return schema.File("schemas/" + strings.ToLower(kind) + ".json"), nil
//	})
type ResolverFunc func(ctx context.Context, doc *niceyaml.Node) (Ref, error)

// Resolve implements [Resolver].
func (f ResolverFunc) Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error) {
	return f(ctx, doc)
}
