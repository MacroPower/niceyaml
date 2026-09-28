package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"go.jacobcolvin.com/x/jsonschema"
	"golang.org/x/sync/singleflight"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

var (
	// ErrResolve indicates a resolver applied to the document but could not
	// name its schema, either by returning an error of its own or the zero
	// Ref with no error. A lookup whose context ends before a resolver names
	// a schema reports it too, and so does [Registry.Schema] for the zero
	// Ref.
	ErrResolve = errors.New("resolve schema")

	// ErrLoad indicates the registry could not load the schema.
	ErrLoad = errors.New("load schema")

	// ErrScopedDocument indicates a caller passed a [*niceyaml.Node] from
	// [niceyaml.Node.At] to [Registry.Lookup] or [Registry.Validate],
	// which pick a schema for a whole document. Validate the document
	// once at its root, then decode its nodes without the registry.
	ErrScopedDocument = errors.New("registry needs a whole document")

	// The error that carries a call to [runtime.Goexit] out of a shared
	// load, so every caller that joined the load can end its own goroutine
	// the same way.
	errGoexit = errors.New("load called runtime.Goexit")

	// The reason a [URL] Ref loads nothing when its scheme is neither http
	// nor https.
	errNotHTTPURL = errors.New("not an HTTP or HTTPS URL")

	// The client every registry that [WithHTTPClient] gave no client
	// fetches schemas with. An [http.Client] is safe for concurrent use,
	// so one serves them all.
	defaultHTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
)

// defaultHTTPTimeout bounds each schema fetch of a registry that
// [WithHTTPClient] gave no client.
const defaultHTTPTimeout = 30 * time.Second

// Registry maps YAML documents to schemas using pluggable resolvers.
//
// Lookup tries the resolvers [WithResolvers] gave it in order; the first
// [Resolver] that does not report [ErrNoMatch] wins. The registry caches
// the schemas it compiles by [Ref.Key] and consults that cache before
// loading, so it loads and compiles each schema once however many
// documents name it. The cache never evicts, so the registry keeps every
// schema it compiles for its whole lifetime, and each distinct Key adds
// an entry, a URL that differs from another only in its query string or
// fragment included. The registry compiles every schema with the options
// [WithCompileOptions] gave it. [Registry.Schema] hands out the compiled
// schema a [Ref] names through that cache, for a caller that holds a Ref
// of its own. A [*Schema] compiled elsewhere is a resolver too, and the
// registry validates with it as it is.
//
// Example:
//
//	kindPath := paths.Root().Child("kind")
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    // Directive matching first (i.e. explicit user intent).
//	    schema.Directive(),
//	    // Content-based matching.
//	    schema.When(
//	        matcher.Content(kindPath, "Deployment"),
//	        schema.Embedded(deploymentSchema),
//	    ),
//	))
//
// A Registry never changes after construction except for its cache, so it
// is safe for concurrent use. Create instances with [NewRegistry].
type Registry struct {
	group       singleflight.Group // one load and compile in flight per Key
	cache       map[string]*Schema // compiled schemas by Ref.Key
	refDocs     map[string][]byte  // bytes of the documents a $ref names, by URI without fragment
	client      *http.Client       // fetches the schemas URL refs name
	fsys        fs.FS              // reads the schemas File refs name; nil reads the working directory
	resolvers   []Resolver
	compileOpts []CompileOption
	mu          sync.RWMutex // guards cache and refDocs
	// Makes Validate report ErrNoMatch when no resolver applies.
	requireSchema bool
}

// RegistryOption configures [Registry] creation.
//
// Available options:
//   - [WithResolvers]
//   - [WithCompileOptions]
//   - [WithRequireSchema]
//   - [WithHTTPClient]
//   - [WithFS]
type RegistryOption func(*Registry)

// WithFS is a [RegistryOption] that sets the file system the registry
// reads schema files from: every [Ref] from [File], whether a resolver
// holds it or [Directive] and [FileOrURL] build it from a reference in
// the input. The root of fsys stands for the working directory. A
// relative path names a file relative to that root, in slash form, so
// schemas shipped in an [embed.FS] beside the documents that name them
// resolve without touching the disk:
//
//	source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
//
//	reg := schema.NewRegistry(
//	    schema.WithFS(bundle),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// A directive in that document that names ./schema.json resolves to
// configs/schema.json in bundle. An absolute path reads relative to the
// working directory at the time [File] or [FileOrURL] built the Ref. That
// covers the path a directive resolves to in a document opened by its
// absolute path and the path a $ref resolves to. A path outside that
// directory names no file. The registry checks only the path, and
// [os.DirFS] follows symbolic links, so a link inside the directory still
// reads a file anywhere on disk. The file system [os.Root.FS] returns
// refuses a link that leads out of the tree, so a program that validates
// documents from another trust domain passes that file system and keeps
// the Root open while the registry is in use:
//
//	root, err := os.OpenRoot(".")
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	reg := schema.NewRegistry(
//	    schema.WithFS(root.FS()),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// Without the option, the registry reads the working directory, with
// each path made absolute against it, and a nil fsys keeps that.
// Either way, the registry reads only a regular file of at most 10 MB,
// the limit it sets on a response from a [URL].
func WithFS(fsys fs.FS) RegistryOption {
	return func(r *Registry) {
		if fsys != nil {
			r.fsys = fsys
		}
	}
}

// WithHTTPClient is a [RegistryOption] that sets the client the registry
// fetches schemas with: every [Ref] from [URL], whether a resolver holds
// it or [Directive] and [FileOrURL] build it from a reference in the
// input. The client's Timeout bounds each fetch, beside the context of the
// lookup, so a schema host that accepts a connection and never answers
// cannot hang a caller whose context has no deadline:
//
//	reg := schema.NewRegistry(
//	    schema.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	)
//
// The default client bounds each fetch at 30 seconds and is otherwise
// [http.DefaultClient], and a nil client keeps it. A
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] fetches
// its catalog with a client of its own, since the catalog is not a schema
// the registry loads.
func WithHTTPClient(client *http.Client) RegistryOption {
	return func(r *Registry) {
		if client != nil {
			r.client = client
		}
	}
}

// WithResolvers is a [RegistryOption] that appends resolvers to the end of
// the lookup order. Lookup tries them in the order given, and the first
// that does not report [ErrNoMatch] wins, so explicit user intent goes
// before content matching and content matching before file path
// conventions:
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.Directive(),
//	    schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded(deploymentSchema)),
//	    schemastore.New(),
//	))
//
// Given more than once, each call appends after the resolvers of the one
// before it.
//
// The option keeps its own copy of res, so writing to the caller's slice
// afterwards changes nothing.
//
// Panics if any resolver is nil, including a nil [ResolverFunc].
func WithResolvers(res ...Resolver) RegistryOption {
	// The check covers the copy, which holds the resolvers the registry
	// tries.
	res = slices.Clone(res)

	for _, resolver := range res {
		// A nil function type is a non-nil interface value that panics
		// when called, so it counts as nil too.
		if f, ok := resolver.(ResolverFunc); resolver == nil || (ok && f == nil) {
			panic("schema.WithResolvers: resolver is nil")
		}
	}

	return func(r *Registry) {
		r.resolvers = append(r.resolvers, res...)
	}
}

// WithRequireSchema is a [RegistryOption] that sets whether
// [Registry.Validate] reports a document no resolver applies to. The
// default is true, and such a document then fails with [ErrNoMatch]. With
// false, Validate accepts it, so a registry that validates what it
// recognizes and passes the rest runs inside a decode through
// [niceyaml.WithValidator]:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	    schema.WithRequireSchema(false),
//	)
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// [Registry.Lookup] reports [ErrNoMatch] either way, since a caller that
// asks for the validator needs to know there is none.
func WithRequireSchema(require bool) RegistryOption {
	return func(r *Registry) {
		r.requireSchema = require
	}
}

// WithCompileOptions is a [RegistryOption] that sets the [CompileOption]
// values the registry compiles every schema with, as [Compile] takes them.
// They apply when the registry compiles a schema, which happens once per
// [Ref.Key], so an option such as a format validator takes effect for
// every document validated against that schema. A [*Schema] compiled
// elsewhere goes into the registry as it is, with the options it was
// compiled with:
//
//	reg := schema.NewRegistry(schema.WithCompileOptions(
//	    schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
//	))
//
// The option keeps its own copy of opts, so writing to the caller's slice
// afterwards changes nothing, even for a registry built later. Given more
// than once, each call appends after the options of the one before it.
func WithCompileOptions(opts ...CompileOption) RegistryOption {
	opts = slices.Clone(opts)

	return func(r *Registry) {
		r.compileOpts = append(r.compileOpts, opts...)
	}
}

// NewRegistry creates a new [*Registry].
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		cache:         make(map[string]*Schema),
		refDocs:       make(map[string][]byte),
		client:        defaultHTTPClient,
		requireSchema: true,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if no resolver applies to the document, with the
// reason each resolver gave nested in it, so [errors.Is] finds a reason
// such as [ErrNoDirective] and a rendering of the error, such as
// [niceyaml.FormatError] or
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], lists the
// reasons below the message:
//
//	app.yaml: no matching schema
//	├── no schema directive
//	└── no catalog entry matches
//
// Returns [ErrResolve] if a resolver applied but could not name the schema, and
// [ErrLoad] or [ErrCompile] if loading or compiling the schema fails. When
// ctx has ended before a resolver runs, or before Lookup finds that no
// resolver applies, Lookup returns [ErrResolve] wrapping the context's
// error, even when the resolvers ignore their context. When ctx ends before
// the named schema finishes loading, Lookup returns [ErrLoad] wrapping the
// context's error without waiting for the load. Either way [errors.Is]
// finds the context's error, such as [context.Canceled] or
// [context.DeadlineExceeded].
//
// An empty document, such as one that holds only comments, is the null
// document, and a resolver sees it as it sees any other.
//
// The resolvers pick a schema for a whole document, from its file path,
// its preamble, or its content, so n must be the root [niceyaml.Node] of
// a document, and a Node from [niceyaml.Node.At] fails with
// [ErrScopedDocument], since the schema Lookup would return is the
// document's and a caller who applied it to the node would check the
// node against the wrong schema. Validate one node against a schema of
// its own with a [Schema].
//
// Every error comes back bound to the document through
// [niceyaml.Node.Bind], so its message names the file the document
// came from.
//
// For most use cases, prefer [Registry.Validate] which combines lookup
// and validation. Use Lookup when you need the validator for custom
// processing.
func (r *Registry) Lookup(ctx context.Context, n *niceyaml.Node) (*Schema, error) {
	v, err := r.lookup(ctx, n)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return nil, n.Bind(err)
	}

	return v, nil
}

// lookup is [Registry.Lookup] before binding the error to the document.
func (r *Registry) lookup(ctx context.Context, doc *niceyaml.Node) (*Schema, error) {
	if !doc.Path().IsRoot() {
		return nil, fmt.Errorf("%w: node is scoped to %s", ErrScopedDocument, doc.Path())
	}

	var reasons []error

	for _, res := range r.resolvers {
		// A resolver that ignores its context, as a Ref does, would name
		// a schema for a canceled lookup. Check the context here, so a
		// canceled lookup reports that whatever the resolver does.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrResolve, ctx.Err())
		}

		ref, err := res.Resolve(ctx, doc)
		if errors.Is(err, ErrNoMatch) {
			reasons = append(reasons, err)

			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrResolve, err)
		}

		return r.Schema(ctx, ref)
	}

	// The loop-top check does not see a context the last resolver ended, so
	// a resolver that cancels and then declines would report no match.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrResolve, ctx.Err())
	}

	return nil, noMatch(reasons)
}

// noMatch returns the error a lookup reports when every resolver declined:
// [ErrNoMatch] alone when no resolver said more than that, and otherwise
// ErrNoMatch with the reason of each resolver that did nested in it, in
// lookup order, so [errors.Is] finds a reason such as [ErrNoDirective]
// and a rendering of the error lists the reasons below the message.
func noMatch(reasons []error) error {
	var nested []error

	for _, reason := range reasons {
		if reason.Error() != ErrNoMatch.Error() {
			nested = append(nested, reasonError{err: reason})
		}
	}

	if len(nested) == 0 {
		return ErrNoMatch
	}

	return niceyaml.WrapError(ErrNoMatch, niceyaml.WithErrors(nested...))
}

// reasonError is the reason one resolver declined a document, nested
// under the [ErrNoMatch] the lookup reports. A resolver wraps ErrNoMatch,
// so its message repeats the sentinel the lookup's message states
// already, and the reason reads without it.
type reasonError struct {
	err error
}

func (e reasonError) Error() string {
	return strings.TrimPrefix(e.err.Error(), ErrNoMatch.Error()+": ")
}

func (e reasonError) Unwrap() error {
	return e.err
}

// Validate validates a document using the first matching schema.
//
// Validate combines schema lookup and validation into a single call. Use
// [Registry.Lookup] when you need the validator for custom processing.
// Validate implements [niceyaml.Validator], so [niceyaml.WithValidator]
// runs it before a decode and [niceyaml.Node.Validate] runs it on its
// own:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// The resolvers pick a schema for a whole document, so n must be the
// root [niceyaml.Node] of one, and a Node from [niceyaml.Node.At] fails
// with [ErrScopedDocument] as it does in [Registry.Lookup]. A registry
// therefore validates a document once, at its root. A loop that decodes
// several nodes of that document runs without the registry, or with a
// [Schema] for the node:
//
//	if err := doc.Validate(ctx, reg); err != nil {
//		return err
//	}
//
//	items, err := doc.Nodes(paths.Root().Child("items").IndexAll())
//	if err != nil {
//		return err
//	}
//
//	for _, item := range items {
//		it, err := item.Decode[Item](ctx)
//		...
//	}
//
// Returns [ErrNoMatch] if no resolver applies to the document, unless
// [WithRequireSchema] set false, in which case such a document passes.
// Callers of a registry that requires a schema can check for the error to
// allow unmatched documents at one call site:
//
//	err := doc.Validate(ctx, reg)
//	if err != nil && !errors.Is(err, ErrNoMatch) {
//	    return err
//	}
//
// Returns validation errors if the document doesn't conform to the schema.
// Returns resolution, loading, or compilation errors if schema preparation
// fails. When ctx ends before the lookup finishes, Validate returns the
// error [Registry.Lookup] returns for it, even with [WithRequireSchema] set
// false.
func (r *Registry) Validate(ctx context.Context, n *niceyaml.Node) error {
	v, err := r.Lookup(ctx, n)
	if err != nil {
		if !r.requireSchema && errors.Is(err, ErrNoMatch) {
			return nil
		}

		return err
	}

	//nolint:wrapcheck // Validation errors should be returned directly.
	return n.Validate(ctx, v)
}

// Schema returns the compiled schema ref names: the one a Ref from
// [Schema.Ref] carries, as it is, or the bytes [Registry.Load] loads for
// it, compiled with the options [WithCompileOptions] gave the registry on
// the first request for its [Ref.Key] and served from the cache after
// that. [Registry.Lookup] takes the schema it validates with from here,
// so a caller that holds a Ref of its own, such as one that checks a Go
// value with [Schema.ValidateValue], shares the same load and compile:
//
//	s, err := reg.Schema(ctx, schema.URL(schemaURL))
//	if err != nil {
//		return err
//	}
//
//	return s.ValidateValue(ctx, value)
//
// The zero Ref names no schema, so it is [ErrResolve]. A load that fails
// is [ErrLoad], and a compile that fails is [ErrCompile]. When ctx ends
// before the schema loads, Schema returns [ErrLoad] wrapping the context's
// error without waiting for the load to finish.
//
// Concurrent requests for one Key share a single load and compile, and
// each caller waits for it only while its own context is live. The shared
// load runs under the context of the caller that started it and reports
// whether that context had ended when the load failed. In that case a
// caller with a live context loads again, and a caller whose context has
// ended returns [ErrLoad] wrapping its own context's error. Any other
// failure reaches every caller that shared the load, including a timeout
// inside the load whose error wraps a context error. A panic or a call to
// [runtime.Goexit] in the load happens again in every caller that shared
// it.
func (r *Registry) Schema(ctx context.Context, ref Ref) (*Schema, error) {
	if ref.Schema() != nil {
		return ref.Schema(), nil
	}

	if ref.Key() == "" {
		return nil, fmt.Errorf("%w: ref names no schema", ErrResolve)
	}

	// A URL Ref shares its key space with a File Ref, whose key is a
	// file:// URL, so the check runs before the cache. Otherwise such a
	// URL Ref would return what a File Ref cached and fail on a registry
	// that holds nothing for its key.
	if ref.url && !isHTTPURL(ref.key) {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.name(), errNotHTTPURL)
	}

	if v, ok := r.cached(ref.Key()); ok {
		return v, nil
	}

	for {
		ch := r.group.DoChan(ref.Key(), func() (any, error) {
			s, err := r.compileRecovering(ctx, ref)

			// Report whether this caller's context had ended when the load
			// failed, so a joiner can tell that cancellation apart from a
			// failure of the load itself.
			return flight{schema: s, starterEnded: err != nil && ctx.Err() != nil}, err
		})

		var res singleflight.Result

		select {
		case <-ctx.Done():
			// The load this caller waits on may have finished in the same
			// instant the context ended. Its result counts when it is ready,
			// so the outcome does not depend on which case the select picks.
			// A schema that a later load cached does not count, because this
			// caller's context ended before that load began.
			select {
			case res = <-ch:
			default:
				return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.name(), ctx.Err())
			}

		case res = <-ch:
		}

		// The singleflight group raises a panic from the load on a
		// goroutine of its own, where no caller can recover it, so the load
		// hands the panic back as an error and each caller that shared it
		// raises it here. The load hands back a call to runtime.Goexit the
		// same way, and each caller calls runtime.Goexit in turn.
		if pe, ok := errors.AsType[*panicError](res.Err); ok {
			panic(pe.value)
		}

		if errors.Is(res.Err, errGoexit) {
			runtime.Goexit()
		}

		f, _ := res.Val.(flight) //nolint:errcheck // The DoChan function always returns a flight.
		if res.Err == nil {
			return f.schema, nil
		}

		// A load that failed because its starter's context ended says
		// nothing about the schema, so a caller with a live context loads
		// again, and a caller whose context has ended reports its own
		// context's error rather than the starter's.
		if f.starterEnded {
			if ctx.Err() == nil {
				continue
			}

			return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.name(), ctx.Err())
		}

		//nolint:wrapcheck // compile already wraps its errors with the sentinel and Key.
		return nil, res.Err
	}
}

// Load returns the bytes of the schema ref names: the file a Ref from
// [File] names, read from the file system [WithFS] gave the registry or
// from the working directory; the URL a Ref from [URL] names, fetched
// with the client [WithHTTPClient] gave it; or the bytes the load of a Ref
// from [Loadable] returns. Load reads the bytes on every call and caches
// nothing. [Registry.Schema] loads the same bytes once and compiles them,
// so Load is for a caller that wants the bytes themselves, such as one
// that prints a schema. For a file or URL whose fragment selects a
// subschema, Load returns the bytes of the whole document. An error wraps
// [ErrLoad].
//
// The zero Ref names no bytes, and a Ref from [Schema.Ref] carries a
// compiled schema rather than bytes, which [Ref.Schema] returns, so Load
// returns an error for either.
func (r *Registry) Load(ctx context.Context, ref Ref) ([]byte, error) {
	data, err := r.load(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.name(), err)
	}

	return data, nil
}

// load is [Registry.Load] before wrapping the error with the sentinel and
// the key.
func (r *Registry) load(ctx context.Context, ref Ref) ([]byte, error) {
	switch {
	case ref.url:
		if !isHTTPURL(ref.key) {
			return nil, errNotHTTPURL
		}

		//nolint:wrapcheck // The fetch error names the URL already.
		return httpfetch.Get(ctx, r.client, ref.key)

	case ref.file != "":
		return readFile(r.fsys, ref.file, ref.abs, ref.wd)

	case ref.load != nil:
		return ref.load(ctx)

	case ref.schema != nil:
		return nil, errors.New("ref carries a compiled schema rather than bytes")

	default:
		return nil, errors.New("ref carries no loader")
	}
}

// localFileURL returns the first file URL that the schema document data
// names in a $id, $ref, or $dynamicRef member, or "" when it names none.
// A member name matches in any case, because the schema decoder reads
// "$REF" as $ref. The search covers every object in the document, not
// only its subschemas, because a JSON pointer $ref can reach an object
// under an unknown keyword and resolve the references there.
func localFileURL(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var doc any

	err := dec.Decode(&doc)
	if err != nil {
		return "", fmt.Errorf("decode schema: %w", err)
	}

	return findFileURL(doc), nil
}

// findFileURL is [localFileURL] for a decoded JSON value. It visits object
// members in sorted key order, so the result is the same on every call.
func findFileURL(v any) string {
	switch v := v.(type) {
	case map[string]any:
		keys := slices.Sorted(maps.Keys(v))

		for _, key := range keys {
			if s, ok := v[key].(string); ok && isRefKeyword(key) && hasPrefixFold(s, "file:") {
				return s
			}
		}

		for _, key := range keys {
			if target := findFileURL(v[key]); target != "" {
				return target
			}
		}

	case []any:
		for _, elem := range v {
			if target := findFileURL(elem); target != "" {
				return target
			}
		}
	}

	return ""
}

// isRefKeyword reports whether key names $id, $ref, or $dynamicRef under
// Unicode case folding, the match encoding/json uses for struct fields.
func isRefKeyword(key string) bool {
	return slices.ContainsFunc([]string{"$id", "$ref", "$dynamicRef"}, func(kw string) bool {
		return strings.EqualFold(key, kw)
	})
}

// refDocument returns the document a $ref names by uri, a URI without a
// fragment, as its bytes and as a schema parsed for this caller alone.
// The first call whose load succeeds and whose bytes parse keeps the
// bytes in the registry, and every later call parses the kept bytes
// instead of calling load. A load that fails and bytes that do not parse
// stay out of the registry, so the next call loads again. When two calls
// load one document at once, both return the bytes the first of them
// kept.
func (r *Registry) refDocument(uri string, load func() ([]byte, error)) ([]byte, *jsonschema.Schema, error) {
	data, kept := r.keptRefDoc(uri)
	if !kept {
		var err error

		data, err = load()
		if err != nil {
			return nil, nil, err
		}
	}

	s, err := jsonschema.ParseSchema(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", httpfetch.Redacted(uri), err)
	}

	// Another call kept the document while this one loaded it. Return the
	// bytes that call kept, so every schema sees one document per URI.
	if !kept && !r.keepRefDoc(uri, data) {
		return r.refDocument(uri, load)
	}

	return data, s, nil
}

// keptRefDoc returns the bytes the registry keeps under uri, if any.
func (r *Registry) keptRefDoc(uri string) ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data, ok := r.refDocs[uri]

	return data, ok
}

// keepRefDoc keeps data under uri and reports true, unless the registry
// keeps bytes under uri already, in which case it reports false and keeps
// those.
func (r *Registry) keepRefDoc(uri string, data []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.refDocs[uri]; ok {
		return false
	}

	r.refDocs[uri] = data

	return true
}

// refOptions returns the options the registry compiles the schema ref
// names with: the options [WithCompileOptions] gave it, behind options
// that let a $ref in a schema from [File] or [URL] name a document beside
// it. Such a schema takes base, its key without any userinfo, as the base
// URI of its references. The registry reads each document a reference
// names as it reads the schema, so a relative $ref resolves against the
// file or URL that holds it. The registry loads each such document once
// and keeps it for every schema that references it, at compile time and
// during validation. A document that fails to load stays out of the
// registry, so a later validation that reaches the reference loads it
// again. A schema from [File] reaches local files and URLs, and one from
// [URL] reaches only URLs, so a remote schema cannot read the local disk,
// even when a schema from [File] loaded the file first. A remote document
// that a schema from [File] reaches cannot read the local disk either,
// because the resolver refuses a document fetched over HTTP or HTTPS that
// names a file URL. The refusal holds whatever order the compiler reaches
// documents in, and it covers a document the registry kept for a schema
// from [URL].
//
// User holds the userinfo of a key from [URL], such as a password. It
// stays out of every URI the compiler and the validator resolve, so no
// error that quotes such a URI holds the password. The registry adds user
// to each fetch of a URL that names the scheme and host of the key and
// carries no userinfo of its own. It keeps the document under that URL
// with user in it, so a document fetched with the password stays apart
// from one fetched without it.
//
// When ref names a subschema by a fragment, doc is the document the
// registry loaded for ref, parsed, and its URL is base without the
// fragment. A reference to that URL resolves to doc without a second
// load, and a relative $ref in doc resolves against it. The compiler
// hands a resolver each URI in its RFC 3986 normal form, so the registry
// compares the two in that form. A key that spells the URL another way,
// such as with an upper-case host, then still resolves to doc. A nil doc
// means ref names a whole document. An option from [WithCompileOptions]
// comes later and wins.
func (r *Registry) refOptions(ref Ref, base string, user keyUserinfo, doc *jsonschema.Schema) []CompileOption {
	if !ref.url && ref.file == "" {
		return r.compileOpts
	}

	allowFile := !ref.url

	resolver := jsonschema.RefResolverFunc(func(ctx context.Context, uri string) (*jsonschema.Schema, error) {
		if i := strings.IndexByte(uri, '#'); i >= 0 {
			uri = uri[:i]
		}

		// The scheme decides what a schema may reach before the registry
		// looks for a kept document, so a schema from URL never receives a
		// local file that a schema from File loaded.
		var load func() ([]byte, error)

		switch {
		case isHTTPURL(uri):
			uri = user.apply(uri)
			load = func() ([]byte, error) {
				//nolint:wrapcheck // The fetch error names the URL already.
				return httpfetch.Get(ctx, r.client, uri)
			}

		case allowFile && isFileURL(uri):
			path, ok := fileURLPath(uri)
			if !ok {
				return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
			}

			load = func() ([]byte, error) {
				return readFile(r.fsys, path, path, ref.wd)
			}

		default:
			return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
		}

		data, s, err := r.refDocument(uri, load)
		if err != nil {
			return nil, err
		}

		if allowFile && isHTTPURL(uri) {
			target, err := localFileURL(data)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", httpfetch.Redacted(uri), err)
			}

			if target != "" {
				return nil, fmt.Errorf("%s: remote schema names local file %q", httpfetch.Redacted(uri), target)
			}
		}

		return s, nil
	})

	refOpts := []jsonschema.ValidateOption{
		jsonschema.WithBaseURI(base),
		jsonschema.WithRefResolver(resolver),
	}

	// The schema that names the fragment takes no base URI, because a base
	// equal to the document's URL would point its $ref at itself. The
	// document takes its URL as its base, as a whole document does.
	if doc != nil {
		docURL := canonicalURL(base)

		preload := jsonschema.RefResolverFunc(func(_ context.Context, uri string) (*jsonschema.Schema, error) {
			if canonicalURL(uri) == docURL {
				return doc, nil
			}

			return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
		})

		refOpts = []jsonschema.ValidateOption{
			jsonschema.WithRefResolver(jsonschema.ChainResolvers(preload, resolver)),
		}
	}

	opts := make([]CompileOption, 0, len(r.compileOpts)+1)
	opts = append(opts, WithJSONSchemaOptions(refOpts...))

	return append(opts, r.compileOpts...)
}

// cached returns the schema cached under key, if any.
func (r *Registry) cached(key string) (*Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.cache[key]

	return v, ok
}

// A panicError carries a panic out of a shared load as an error, so it
// can cross the singleflight group and be raised again by every caller
// that joined the load.
type panicError struct {
	value any
}

// Error implements error.
func (p *panicError) Error() string {
	return fmt.Sprintf("load panicked: %v", p.value)
}

// A flight carries the result of a shared load to every caller that
// joined it. It holds the schema the load compiled and whether the
// context of the caller that started the load had ended when the load
// failed.
type flight struct {
	schema       *Schema
	starterEnded bool
}

// compileRecovering runs compile and turns a panic in the load or the
// compiler into a [*panicError] and a call to [runtime.Goexit] into
// errGoexit. The singleflight group's DoChan never answers a flight whose
// function ends its goroutine that way, so compileRecovering runs compile
// on a goroutine of its own and waits for it.
func (r *Registry) compileRecovering(ctx context.Context, ref Ref) (*Schema, error) {
	var (
		s        *Schema
		err      error
		returned bool
	)

	done := make(chan struct{})

	go func() {
		defer close(done)

		defer func() {
			if p := recover(); p != nil {
				err = &panicError{value: p}
				returned = true
			}
		}()

		s, err = r.compile(ctx, ref)
		returned = true
	}()

	<-done

	// The deferred recover returns nil while runtime.Goexit unwinds the
	// goroutine, so returned stays false only when compile called
	// runtime.Goexit.
	if !returned {
		return nil, errGoexit
	}

	return s, err
}

// compile loads and compiles the schema ref names, caches it under its
// Key, and returns it. When an earlier flight cached a schema under that
// Key, compile returns that schema. The group runs one compile per Key at
// a time and each compile checks the cache first, so every caller sees
// one schema per Key.
func (r *Registry) compile(ctx context.Context, ref Ref) (*Schema, error) {
	key := ref.Key()

	if v, ok := r.cached(key); ok {
		return v, nil
	}

	data, err := r.Load(ctx, ref)
	if err != nil {
		return nil, err
	}

	// The compiler sees the key of a URL without its userinfo, and the
	// resolver adds the userinfo back to each fetch.
	base, user := key, keyUserinfo{}
	if ref.url {
		base, user = splitUserinfo(key)
	}

	// A fragment on the key of a file or a URL names a subschema of the
	// document. The registry compiles a schema whose $ref names that
	// subschema and serves the loaded document to the reference. The
	// compiler reads the draft from the $schema of the root it compiles,
	// so that schema declares the $schema of the document, and the
	// subschema follows the draft it does when the document compiles
	// whole. A key from Loadable is a name, where a '#' may mean anything,
	// so it compiles whole.
	var doc *jsonschema.Schema

	_, fragment, _ := strings.Cut(base, "#")
	if (ref.url || ref.file != "") && fragment != "" {
		doc, err = jsonschema.ParseSchema(data)
		if err != nil {
			return nil, fmt.Errorf("%q: %w: %w", ref.name(), ErrCompile, err)
		}

		wrapper := map[string]string{"$ref": base}
		if doc.Schema != "" {
			wrapper["$schema"] = doc.Schema
		}

		data, err = json.Marshal(wrapper)
		if err != nil {
			return nil, fmt.Errorf("%q: %w: %w", ref.name(), ErrCompile, err)
		}
	}

	compiled, err := Compile(ctx, data, r.refOptions(ref, base, user, doc)...)
	if err != nil {
		return nil, fmt.Errorf("%q: %w", ref.name(), err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache[key] = compiled

	return compiled, nil
}

// A keyUserinfo is the userinfo of a URL key, with the scheme and host
// it belongs to. The zero keyUserinfo holds none.
type keyUserinfo struct {
	user   *url.Userinfo
	scheme string
	host   string
}

// splitUserinfo returns rawURL without its userinfo, and that userinfo.
// A URL that carries none, or does not parse, comes back unchanged with
// the zero keyUserinfo.
func splitUserinfo(rawURL string) (string, keyUserinfo) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL, keyUserinfo{}
	}

	info := keyUserinfo{user: u.User, scheme: u.Scheme, host: u.Host}
	u.User = nil

	return u.String(), info
}

// apply returns uri with the userinfo of k when uri names the scheme and
// host of k and carries no userinfo of its own, and uri unchanged
// otherwise.
func (k keyUserinfo) apply(uri string) string {
	if k.user == nil {
		return uri
	}

	u, err := url.Parse(uri)
	if err != nil || u.User != nil || !strings.EqualFold(u.Scheme, k.scheme) || !strings.EqualFold(u.Host, k.host) {
		return uri
	}

	u.User = k.user

	return u.String()
}

// canonicalURL returns rawURL without its fragment in the normal form of
// RFC 3986 section 6.2.2, which the compiler gives each URI it hands a
// resolver. That form spells the scheme and host in lower case and each
// percent-encoded octet decoded where it encodes an unreserved character
// and in upper-case hex otherwise, and its path holds no dot segments. A
// URL that does not parse comes back without its fragment and otherwise
// as written.
func canonicalURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		base, _, _ := strings.Cut(rawURL, "#")

		return base
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.RawQuery = canonicalEscapes(u.RawQuery)
	u.Fragment, u.RawFragment = "", ""

	if u.Opaque != "" {
		u.Opaque = canonicalEscapes(u.Opaque)

		return u.String()
	}

	escaped := canonicalEscapes(u.EscapedPath())

	path, err := url.PathUnescape(escaped)
	if err != nil {
		return u.String()
	}

	u.Path, u.RawPath = path, escaped

	// RFC 3986 removes the dot segments from the path of an absolute
	// reference as it resolves one, and an absolute reference ignores its
	// base, so resolving u against itself removes them.
	if u.IsAbs() {
		u = u.ResolveReference(u)
	}

	return u.String()
}

// canonicalEscapes returns s with each percent-encoded octet decoded where
// it encodes an RFC 3986 unreserved character and spelled in upper-case
// hex otherwise. A malformed escape stays as it is.
func canonicalEscapes(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}

	var b strings.Builder

	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
			b.WriteByte(s[i])

			continue
		}

		octet := hexValue(s[i+1])<<4 | hexValue(s[i+2])
		if isUnreserved(octet) {
			b.WriteByte(octet)
		} else {
			b.WriteString(strings.ToUpper(s[i : i+3]))
		}

		i += 2
	}

	return b.String()
}

// isUnreserved reports whether c is an RFC 3986 unreserved character.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '.' || c == '_' || c == '~'
	}
}

// isHexDigit reports whether c is a hexadecimal digit in either case.
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// hexValue returns the value of the hexadecimal digit c.
func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
