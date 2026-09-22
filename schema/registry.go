package schema

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

var (
	// ErrResolve indicates a resolver applied to the document but could not
	// name its schema, either by returning an error of its own or the zero
	// Ref with no error. A lookup whose context ends before a resolver names
	// a schema reports it too.
	ErrResolve = errors.New("resolve schema")

	// ErrLoad indicates the registry could not load the schema.
	ErrLoad = errors.New("load schema")

	// ErrScopedDocument indicates a caller passed a [*niceyaml.Node] from
	// [niceyaml.Node.At] to [Registry.Validate], which picks a schema for
	// a whole document.
	ErrScopedDocument = errors.New("registry needs a whole document")
)

// Registry maps YAML documents to schemas using pluggable resolvers.
//
// Lookup tries the resolvers [WithResolvers] gave it in order; the first
// [Resolver] that does not report [ErrNoMatch] wins. The registry caches
// the schemas it compiles by [Ref.Key] and consults that cache before
// loading, so it loads and compiles each schema once however many
// documents name it, and it compiles every schema with the options
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
	client      *http.Client       // fetches the schemas URL refs name
	fsys        fs.FS              // reads the schemas File refs name; nil reads the working directory
	resolvers   []Resolver
	compileOpts []CompileOption
	mu          sync.RWMutex // guards cache
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
// the input. A path then names a file relative to the root of fsys, in
// slash form, so schemas shipped in an [embed.FS] beside the documents
// that name them resolve without touching the disk:
//
//	source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
//
//	reg := schema.NewRegistry(
//	    schema.WithFS(bundle),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// A directive in that document that names ./schema.json resolves to
// configs/schema.json in bundle. Without the option, the registry reads
// the working directory, with each path made absolute against it, and a
// nil fsys keeps that.
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
// The default is [http.DefaultClient], and a nil client keeps it. A
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
func WithResolvers(res ...Resolver) RegistryOption {
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
// The registry keeps its own copy of opts, so writing to the caller's slice
// afterwards changes nothing. Given more than once, each call appends after
// the options of the one before it.
func WithCompileOptions(opts ...CompileOption) RegistryOption {
	return func(r *Registry) {
		r.compileOpts = append(r.compileOpts, opts...)
	}
}

// NewRegistry creates a new [*Registry].
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		cache:         make(map[string]*Schema),
		client:        http.DefaultClient,
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
// such as [ErrNoDirective] and a rendering of the error, such as the %+v
// verb or [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError],
// lists the reasons below the message:
//
//	app.yaml: no matching schema
//	├── no schema directive
//	└── no catalog entry matches
//
// Returns [ErrResolve] if a resolver applied but could not name the schema, and
// [ErrLoad] or [ErrCompile] if loading or compiling the schema fails. When
// ctx ends before the schema loads, Lookup returns [ErrLoad] wrapping the
// context's error without waiting for the load to finish.
//
// An empty document, such as one that holds only comments, is the null
// document, and a resolver sees it as it sees any other.
//
// The resolvers pick a schema for a whole document, from its file path,
// its preamble, or its content, so n must be the root [niceyaml.Node] of
// a document, and a Node from [niceyaml.Node.At] fails with
// [ErrScopedDocument]. Validate one node against a schema of its own
// with a [Schema].
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
// The resolvers pick a schema for a whole document, so n must be the root
// [niceyaml.Node] of a document, the one [niceyaml.Source.Documents]
// returns or [niceyaml.Node.Document] reaches, and a Node from
// [niceyaml.Node.At] fails with [ErrScopedDocument] rather than a schema
// for the file applied to the node. Validate one node against a schema of
// its own with a [Schema].
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
// fails.
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
// whether that context had ended when the load failed. A caller that
// joined with a live context loads again only in that case. Any other
// failure reaches every caller that shared the load, including a timeout
// inside the load whose error wraps a context error.
func (r *Registry) Schema(ctx context.Context, ref Ref) (*Schema, error) {
	if ref.Schema() != nil {
		return ref.Schema(), nil
	}

	if ref.Key() == "" {
		return nil, fmt.Errorf("%w: resolver returned an empty ref", ErrResolve)
	}

	if v, ok := r.cached(ref.Key()); ok {
		return v, nil
	}

	for {
		ch := r.group.DoChan(ref.Key(), func() (any, error) {
			err := r.compile(ctx, ref)

			// Report whether this caller's context had ended when the load
			// failed, so a joiner can tell that cancellation apart from a
			// failure of the load itself.
			return err != nil && ctx.Err() != nil, err
		})

		var res singleflight.Result

		select {
		case <-ctx.Done():
			// The load may have finished in the same instant the context
			// ended, so hand back the cached schema when there is one.
			if v, ok := r.cached(ref.Key()); ok {
				return v, nil
			}

			return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.Key(), ctx.Err())

		case res = <-ch:
		}

		if res.Err == nil {
			break
		}

		if starterEnded, ok := res.Val.(bool); ok && starterEnded && ctx.Err() == nil {
			continue
		}

		//nolint:wrapcheck // compile already wraps its errors with the sentinel and Key.
		return nil, res.Err
	}

	v, ok := r.cached(ref.Key())
	if !ok {
		return nil, fmt.Errorf("%w: %q: validator missing after compile", ErrCompile, ref.Key())
	}

	return v, nil
}

// Load returns the bytes of the schema ref names: the file a Ref from
// [File] names, read from the file system [WithFS] gave the registry or
// from the working directory; the URL a Ref from [URL] names, fetched
// with the client [WithHTTPClient] gave it; or the bytes the load of a Ref
// from [Loadable] returns. Load reads the bytes on every call and caches
// nothing. [Registry.Schema] loads the same bytes once and compiles them,
// so Load is for a caller that wants the bytes themselves, such as one
// that prints a schema. An error wraps [ErrLoad].
//
// The zero Ref names no bytes, and a Ref from [Schema.Ref] carries a
// compiled schema rather than bytes, which [Ref.Schema] returns, so Load
// returns an error for either.
func (r *Registry) Load(ctx context.Context, ref Ref) ([]byte, error) {
	data, err := r.load(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.Key(), err)
	}

	return data, nil
}

// load is [Registry.Load] before wrapping the error with the sentinel and
// the key.
func (r *Registry) load(ctx context.Context, ref Ref) ([]byte, error) {
	switch {
	case ref.url:
		//nolint:wrapcheck // The fetch error names the URL already.
		return httpfetch.Get(ctx, r.client, ref.key)

	case ref.file != "":
		return readFile(r.fsys, ref.file, ref.abs)

	case ref.load != nil:
		return ref.load(ctx)

	case ref.schema != nil:
		return nil, errors.New("ref carries a compiled schema rather than bytes")

	default:
		return nil, errors.New("ref carries no loader")
	}
}

// cached returns the schema cached under key, if any.
func (r *Registry) cached(key string) (*Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.cache[key]

	return v, ok
}

// compile loads and compiles the schema ref names and caches the result
// under its Key. When an earlier call cached a schema under that Key,
// compile keeps it, so every caller sees one schema per Key.
func (r *Registry) compile(ctx context.Context, ref Ref) error {
	key := ref.Key()

	if _, ok := r.cached(key); ok {
		return nil
	}

	data, err := r.Load(ctx, ref)
	if err != nil {
		return err
	}

	compiled, err := Compile(ctx, data, r.compileOpts...)
	if err != nil {
		return fmt.Errorf("%q: %w", key, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.cache[key]; !ok {
		r.cache[key] = compiled
	}

	return nil
}
