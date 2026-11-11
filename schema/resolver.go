package schema

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"weak"

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
// together, so one path in two file systems is two schemas. It caches a
// Ref from [Loadable] or [Embedded] apart from the Refs that name a file
// or a URL, as Loadable describes. The registry compiles every schema
// with the options that [WithCompileOptions] gave it.
//
// [Registry.Load] reads a file from the file system that its Ref names,
// or from disk. On disk, the [Access] of the registry decides which
// files it reads. Load fetches a URL with the client that
// [WithHTTPClient] gave the registry, or with the loader of [WithLoader]
// whose prefix covers the URL. A Ref that carries a compiled
// schema does not have anything to load, and the registry validates with
// the schema as it is.
//
// A Ref from [RefBeside] or [Directive] names what the author of a
// document picked. A registry loads such a Ref only when [WithAccess]
// gave it an Access, as [ErrAccessNotSet] describes. A registry does
// not need the option for the other Refs, which the program names. An
// Access that the registry holds applies to those Refs too.
//
// [Ref.WithFragment] names a subschema of the file or URL that a Ref
// names. [Ref.NamedAt] records where a document names the Ref, so a
// lookup of that document locates the error of the schema there.
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
	// Where a document names the Ref, as NamedAt sets it, or nil. A
	// lookup of that document locates the load or compile error of the
	// schema there.
	at  *site
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
	// Whether a document named the Ref, as RefBeside marks it. A registry
	// loads such a Ref only when WithAccess gave it an access.
	named bool
}

// Loadable creates a new [Ref] that names a schema by key and loads its
// bytes with load on demand.
//
// The key must be unique among the Refs from Loadable. Two such Refs
// with one key are one schema to every registry that caches them. The
// key is a name, not necessarily an address that a registry can fetch,
// so the $id of the schema is a fitting key. A Ref from [Embedded] is a
// Ref from Loadable whose key is "embedded:" and a digest of its bytes.
// The load might be expensive or fail, but it must return the same bytes
// each time it runs for one key.
//
// A registry keeps a Ref from Loadable apart from each Ref that names a
// location, such as one from [URL], [File], or [FileFS]. A key that
// spells a URL or a file URL is therefore not the schema of that URL or
// file. A request for the Ref from Loadable gets the schema that load
// returns, whatever a document named before it. A request for the URL or
// the file gets the bytes at that location. The registry fetches the
// URL, or reads a file on disk, only when its [Access] allows the
// location.
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
//
// Key is the only method that returns the password in the userinfo of a
// URL. The Ref itself formats and logs as [Ref.Name], which replaces the
// password.
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

// String returns [Ref.Name], so a verb such as %v or %s prints a Ref
// without the password of a URL. The %#v verb prints the fields of the
// Ref, and the key among them keeps the password.
func (r Ref) String() string {
	return r.Name()
}

// LogValue implements [slog.LogValuer]. It gives a [log/slog] handler
// the [Ref.Name] as a string, without the password of a URL.
func (r Ref) LogValue() slog.Value {
	return slog.StringValue(r.Name())
}

// Schema returns the compiled schema of a [Ref] from [Schema.Ref], or nil
// for a Ref that names bytes to load. [Registry.Schema] returns it as it
// is for such a Ref. A caller that loads bytes without a registry checks
// it before [Registry.Load], as the registry does.
func (r Ref) Schema() *Schema {
	return r.schema
}

// WithFragment returns the [Ref] of the subschema that fragment selects
// in the document that r names. The fragment is the text after the '#'
// of a URL, such as the JSON pointer "/definitions/Foo" or an anchor
// name. It selects the subschema that a $ref with the same fragment
// selects. WithFragment replaces any fragment that r already carries,
// and an empty fragment names the whole document. The Ref keeps the rest
// of r, so a Ref from [RefBeside] still counts as one that a document
// names.
//
// The registry reads the fragment as a URL writes it. A token of a JSON
// pointer writes '~' as ~0 and '/' as ~1, and the URL then escapes '%'
// and the other bytes that it reserves. A program that builds the
// fragment from a name escapes the name first. Unescaped, a name that
// holds '/' selects a nested subschema, and a name that holds '%' might
// not select any.
//
// A Ref from [File], [FileFS], [URL], [FileOrURL], or [RefBeside] names
// a file or a URL, so it takes a fragment. A schema bundle in an
// [embed.FS] thus serves each of its definitions:
//
//	escape := strings.NewReplacer("~", "~0", "/", "~1")
//	token := url.PathEscape(escape.Replace(kind))
//
//	schema.FileFS(bundle, "schemas/defs.json").WithFragment("/definitions/" + token)
//
// The registry caches each fragment apart, as [URL] describes, and loads
// the document once for each fragment that names it.
//
// WithFragment panics for a Ref that does not name a file or a URL, such
// as the zero Ref or one from [Loadable], [Embedded], or [Schema.Ref].
// The registry compiles the bytes of such a Ref whole.
func (r Ref) WithFragment(fragment string) Ref {
	if !r.url && r.file == "" {
		panic("schema.Ref.WithFragment: the Ref does not name a file or a URL")
	}

	base, _, _ := strings.Cut(r.key, "#")
	if fragment != "" {
		base += "#" + fragment
	}

	r.key = base

	return r
}

// NamedAt returns r with the place in the document of doc where that
// document names the schema. The options locate the place, as they do
// for [niceyaml.Locate]. A lookup of that document locates there the
// error of a schema that does not load or compile, as [Registry.Lookup]
// describes. [Directive] marks each Ref at the comment that holds the
// directive.
//
// A resolver that reads the reference from a field marks the Ref at the
// field, and locates the error of [RefBeside] there too:
//
//	at := niceyaml.AtPath(schemaPath)
//
//	ref, err := schema.RefBeside(doc, s)
//	if err != nil {
//	    return schema.Ref{}, niceyaml.Locate(err, at)
//	}
//
//	return ref.NamedAt(doc, at), nil
//
// The place belongs to the document of doc alone. A program might keep
// the Ref and check another document with it. A lookup of that document
// reports the error of the schema without a position. The Ref holds the
// document through a weak pointer, so a Ref that a program or a cache
// keeps does not keep the document alive.
//
// NamedAt replaces the place that r already carries. NamedAt panics if
// doc is nil.
func (r Ref) NamedAt(doc *niceyaml.Node, at ...niceyaml.ErrorOption) Ref {
	if doc == nil {
		panic("schema.Ref.NamedAt: doc is nil")
	}

	r.at = &site{doc: weak.Make(doc.Document()), at: slices.Clone(at)}

	return r
}

// A site is where a document names a [Ref]. It holds the root of the
// document and the options that locate the place there.
type site struct {
	doc weak.Pointer[niceyaml.Node]
	at  []niceyaml.ErrorOption
}

// locate returns err located at the site when the site stands in doc.
// For a site in another document, and for a nil site, locate returns err
// as it is. The lookup that calls locate puts the result inside
// [niceyaml.Unchecked].
func (s *site) locate(doc *niceyaml.Node, err error) error {
	if s == nil || s.doc != weak.Make(doc.Document()) {
		return err
	}

	return niceyaml.Locate(err, s.at...)
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
// [go.jacobcolvin.com/niceyaml/matcher.Matcher].
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
