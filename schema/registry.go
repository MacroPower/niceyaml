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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.jacobcolvin.com/x/jsonschema"
	"golang.org/x/sync/singleflight"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/capture"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

var (
	// ErrResolve indicates a resolver applied to the document but could not
	// name its schema, either by returning an error of its own or the zero
	// Ref with no error. A lookup whose context ends before a resolver names
	// a schema reports it too, and so does [Registry.Schema] for the zero
	// Ref. A [matcher.Content] or [matcher.Text] guard that refuses a
	// document for its aliases returns [ErrExcessiveAliasing], and the
	// document is at fault for that one, so [niceyaml.IsInvalid] reports
	// the ErrResolve around it.
	ErrResolve = errors.New("resolve schema")

	// ErrLoad indicates the registry could not load the schema.
	ErrLoad = errors.New("load schema")

	// ErrScopedDocument indicates a caller passed a [*niceyaml.Node] from
	// [niceyaml.Node.At] to [Registry.Lookup] or [Registry.Validate],
	// which pick a schema for a whole document. Validate the document
	// once at its root, then decode its nodes without the registry.
	ErrScopedDocument = errors.New("registry needs a whole document")

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
// Lookup tries the resolvers [WithResolvers] gave it in order, and the
// first [Resolver] that does not report [ErrNoMatch] wins. The registry
// caches the schemas it compiles by [Ref.Key], and a Ref from [FileFS]
// by its file system too. It consults that cache
// before loading, so once a schema compiles, the registry serves it to
// every later document that names it without loading it again. A failed
// load or compile stays out of the cache, so the next document that names
// the Key loads it again. The cache never evicts, so the registry keeps every
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
//	kindPath := paths.Doc().Child("kind")
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
	group   singleflight.Group // one load and compile in flight per cacheKey
	cache   map[string]*Schema // compiled schemas by cacheKey
	refDocs map[string]refDoc  // the documents a $ref names, by URI without fragment
	fsIDs   map[any]int        // the number of each file system a Ref names its file in
	fsKept  []fs.FS            // each file system in fsIDs, which keeps a map known by address alive
	registryConfig
	mu sync.RWMutex // guards cache, refDocs, fsIDs, and fsKept
}

// RegistryOption configures [Registry] creation.
//
// Available options:
//   - [WithResolvers]
//   - [WithCompileOptions]
//   - [WithRequireSchema]
//   - [WithHTTPClient]
//   - [WithFSAt]
type RegistryOption func(*registryConfig)

// registryConfig holds the settings a [RegistryOption] configures. An
// option takes it in place of the [Registry] that embeds it, so only
// [NewRegistry] can apply one.
type registryConfig struct {
	client      *http.Client // fetches the schemas URL refs name
	fsys        fs.FS        // reads the schemas File refs name under WithFSAt; nil reads the disk
	fsAt        *fsDir       // the directory on disk the root of fsys stands for; nil when it stands for none
	resolvers   []Resolver
	compileOpts []CompileOption
	// Makes Validate report ErrNoMatch when no resolver applies.
	requireSchema bool
}

// WithFSAt is a [RegistryOption] that confines the schema files the
// registry reads from disk to dir, a directory there, and reads them
// through fsys. It serves every [Ref] that names a file on disk: one from
// [File], and one [Directive], [RefBeside], or [FileOrURL] builds for a
// document on disk. A Ref from [FileFS], and one built for a document in
// a file system, reads from its own file system and never from disk, so
// the option has no part in it. The root of fsys stands for dir, as it
// does in the file system
// [os.DirFS] returns for dir and in the one of an [os.Root] opened on
// dir. The registry resolves each path as it does without a file system,
// so a relative path resolves against the working directory at the time
// [File] runs. It then reads the path relative to dir from fsys. A path
// outside dir names no file and fails to load with [fs.ErrInvalid].
//
// The option pairs with [niceyaml.NewSourceFromFile], whose documents
// carry paths on disk. A program that validates documents from another
// trust domain passes the file system of an [os.Root] and keeps the Root
// open while the registry is in use:
//
//	root, err := os.OpenRoot(tenantDir)
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	reg := schema.NewRegistry(
//	    schema.WithFSAt(tenantDir, root.FS()),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
//	source, err := niceyaml.NewSourceFromFile(filepath.Join(tenantDir, "app.yaml"))
//
// A directive in that document that names ./schema.json resolves to
// schema.json in tenantDir, and one that names ../other/schema.json
// fails to load. The registry checks only the path, and [os.DirFS]
// follows symbolic links, so a link inside dir still reads a file
// anywhere on disk. The file system [os.Root.FS] returns refuses a link
// that leads out of the tree.
//
// A relative dir resolves against the working directory when
// [NewRegistry] runs, so WithFSAt(".", fsys) confines the registry to
// that directory. In a process without a working directory, a relative
// dir names no directory, and every file fails to load.
//
// The registry compares each path with dir as written.
// When the path is not under dir that way, it resolves the symbolic
// links in the directories of both and compares them again. A program
// that entered its working directory through a link therefore still
// reads the files under a dir it names by its physical path.
//
// [Ref.Key], the cache, and the URL a $ref resolves against stay as they
// are without a file system, and an error names the path on disk.
//
// A registry that must read nothing from disk takes a file system that
// holds no file, since every path on disk then names none:
//
//	schema.WithFSAt(".", fstest.MapFS{})
//
// A nil fsys leaves the registry as it is. Given more than once, the
// last option wins. The registry reads only a regular file of at most
// 10 MB, as [File] describes.
//
// Panics if dir is empty.
func WithFSAt(dir string, fsys fs.FS) RegistryOption {
	if dir == "" {
		panic("schema.WithFSAt: dir is empty")
	}

	return func(c *registryConfig) {
		if fsys != nil {
			c.fsys, c.fsAt = fsys, newFSDir(dir)
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
	return func(c *registryConfig) {
		if client != nil {
			c.client = client
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
// Panics if any resolver is nil, including a nil pointer or a nil
// [ResolverFunc].
func WithResolvers(res ...Resolver) RegistryOption {
	// The check covers the copy, which holds the resolvers the registry
	// tries.
	res = slices.Clone(res)

	for _, resolver := range res {
		if nilness.IsNil(resolver) {
			panic("schema.WithResolvers: resolver is nil")
		}
	}

	return func(c *registryConfig) {
		c.resolvers = append(c.resolvers, res...)
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
	return func(c *registryConfig) {
		c.requireSchema = require
	}
}

// WithCompileOptions is a [RegistryOption] that sets the [CompileOption]
// values the registry compiles every schema with, as [Compile] takes them.
// They apply when the registry compiles a schema, which happens once per
// cache key, as [Registry.Schema] describes, so an option such as a format
// validator takes effect for every document validated against that
// schema. A [*Schema] compiled elsewhere goes into the registry as it is,
// with the options of its own compile:
//
//	reg := schema.NewRegistry(schema.WithCompileOptions(
//	    schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
//	))
//
// A [jsonschema.WithRefResolver] among these options replaces the
// registry's own resolution of the $refs in a whole schema from [File] or
// [URL]. A relative $ref there then resolves only if that resolver serves
// it. A [Ref] that names a subschema by a fragment, such as
// "https://example.com/defs.json#/$defs/a", always resolves its $refs
// through the registry, because only the registry holds the document the
// fragment points into. A resolver among these options does not apply to
// it.
//
// The option keeps its own copy of opts, so writing to the caller's slice
// afterwards changes nothing, even for a registry built later. Given more
// than once, each call appends after the options of the one before it.
func WithCompileOptions(opts ...CompileOption) RegistryOption {
	opts = slices.Clone(opts)

	return func(c *registryConfig) {
		c.compileOpts = append(c.compileOpts, opts...)
	}
}

// NewRegistry creates a new [*Registry].
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		cache:         make(map[string]*Schema),
		refDocs:       make(map[string]refDoc),
		fsIDs:         make(map[any]int),
		client:        defaultHTTPClient,
		requireSchema: true,
	}
	for _, opt := range opts {
		opt(&r.registryConfig)
	}

	return r
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if no resolver applies to the document, with the
// reason each resolver gave as a detail, from [niceyaml.WithDetails]. The
// lookup failed once, so the message of the error is one line.
// [errors.Is] finds a reason such as [ErrNoDirective], and a rendering of
// the error, such as [niceyaml.FormatError] or
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], lists the
// reasons below the message:
//
//	app.yaml: no matching schema
//	├── no schema directive
//	└── no catalog entry matches
//
// In a file that holds several documents, the message names the document
// the lookup ran for, as "app.yaml: document 3: no matching schema".
//
// Returns [ErrResolve] if a resolver applied but could not name the schema, and
// [ErrLoad] or [ErrCompile] if loading or compiling the schema fails. When
// ctx ends before a resolver names a schema, or before Lookup finds that no
// resolver applies, Lookup returns [ErrResolve] wrapping the context's
// error, even when the resolvers ignore their context. When ctx ends after
// that, before the named schema finishes loading, Lookup returns [ErrLoad]
// wrapping the context's error without waiting for the load. Either way [errors.Is]
// finds the context's error, such as [context.Canceled] or
// [context.DeadlineExceeded].
//
// An empty document, such as one that holds only comments, is the null
// document, and a resolver sees it as it sees any other. A document that
// did not parse has no content for a resolver to read, so Lookup returns
// the syntax error [niceyaml.Node.Err] returns and asks no resolver.
//
// The resolvers pick a schema for a whole document, from its file path,
// its preamble, or its content, so n must be the root [niceyaml.Node] of
// a document. A Node from [niceyaml.Node.At] fails with
// [ErrScopedDocument]. The schema Lookup would return is the document's,
// and a caller who applied it to the node would check the node against
// the wrong schema. Validate one node against a schema of its own with a
// [Schema].
//
// Every error comes back bound to the root of the document through
// [niceyaml.Node.Bind], so its message names the file the document
// came from. An error of the lookup is about the schema rather than a
// value of the document, so it carries no position, even when n is a
// Node from [niceyaml.Node.At].
//
// For most use cases, prefer [Registry.Validate] which combines lookup
// and validation. Use Lookup when you need the validator for custom
// processing.
func (r *Registry) Lookup(ctx context.Context, n *niceyaml.Node) (*Schema, error) {
	err := n.Err()
	if err != nil {
		//nolint:wrapcheck // The source bound the syntax error already.
		return nil, err
	}

	v, _, err := r.lookup(ctx, n)
	if err != nil {
		// The root of the document gives the error no location, where a
		// scoped Node would point it at its value.
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return nil, n.Document().Bind(err)
	}

	return v, nil
}

// lookup is [Registry.Lookup] before binding the error to the document.
// It reports true when every resolver declined the document. The error
// alone cannot tell, since a load error may wrap [ErrNoMatch] too.
func (r *Registry) lookup(ctx context.Context, doc *niceyaml.Node) (*Schema, bool, error) {
	if !doc.Path().IsRoot() {
		return nil, false, fmt.Errorf("%w: node is scoped to %s", ErrScopedDocument, doc.Path())
	}

	// A resolver that ignores its context, as a Ref does, would name a
	// schema for a canceled lookup, and a cached schema needs no context
	// to load. Check the context before the first resolver and after each
	// one, before the lookup uses what the resolver returned. A lookup
	// whose context ends before a resolver names a schema then reports the
	// context's error whether the resolver named a schema, declined, or
	// failed.
	if ctx.Err() != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrResolve, ctx.Err())
	}

	var reasons []error

	for _, res := range r.resolvers {
		ref, err := res.Resolve(ctx, doc)

		ctxErr := ctx.Err()
		if ctxErr != nil {
			// A resolver that honors its context may fail with the
			// context's error and detail of its own, and the lookup keeps
			// that error. The context's error replaces anything else,
			// even a decline that wraps it, since a canceled lookup never
			// matches ErrNoMatch.
			if err == nil || errors.Is(err, ErrNoMatch) || !errors.Is(err, ctxErr) {
				err = ctxErr
			}

			return nil, false, fmt.Errorf("%w: %w", ErrResolve, err)
		}

		if err != nil && !errors.Is(err, ErrNoMatch) {
			return nil, false, fmt.Errorf("%w: %w", ErrResolve, err)
		}

		if err != nil {
			reasons = append(reasons, err)

			continue
		}

		v, err := r.Schema(ctx, ref)

		return v, false, err
	}

	return nil, true, noMatch(reasons)
}

// noMatch returns the error a lookup reports when every resolver declined:
// [ErrNoMatch] inside a [*niceyaml.Error] from [niceyaml.Invalid], with
// the reason of each resolver that said more than ErrNoMatch as a detail
// from [niceyaml.WithDetails], in lookup order. The document names no
// schema the registry knows, so [niceyaml.IsInvalid] reports the error.
// The lookup failed once, so the error is one problem, and its message is
// one line. [errors.Is] finds a reason such as [ErrNoDirective], and
// [niceyaml.FormatError] lists the reasons below the message.
func noMatch(reasons []error) error {
	var details []error

	for _, reason := range reasons {
		if reason.Error() != ErrNoMatch.Error() {
			details = append(details, reasonError{err: reason})
		}
	}

	return niceyaml.Invalid(ErrNoMatch, niceyaml.WithDetails(details...))
}

// reasonError is the reason one resolver declined a document, a detail of
// the [ErrNoMatch] the lookup reports. A resolver wraps ErrNoMatch,
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
//	items, err := doc.Nodes(paths.Doc().Child("items").IndexAll())
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
// [niceyaml.IsInvalid] reports that error, since the document names no
// schema the registry knows.
// A document a resolver applies to never passes that way, even when the
// load error of its schema wraps ErrNoMatch. Callers of a registry that
// requires a schema can check for the error to allow unmatched documents
// at one call site. Such a load error matches both [ErrLoad] and
// ErrNoMatch, so the check tests for ErrLoad first:
//
//	err := doc.Validate(ctx, reg)
//	if err != nil && (errors.Is(err, ErrLoad) || !errors.Is(err, ErrNoMatch)) {
//	    return err
//	}
//
// An empty document validates as any other, so the one a trailing "---"
// leaves at the end of a file fails with ErrNoMatch when no resolver
// applies to it. [niceyaml.Source.ValidateDocuments] passes over that
// document in a file that holds a document with content.
// [niceyaml.SkipEmpty] wraps the registry to pass an empty document
// wherever the registry runs, such as on a file that is empty as a whole:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// Returns validation errors if the document doesn't conform to the schema.
// Returns resolution, loading, or compilation errors if schema preparation
// fails. When ctx ends before the lookup finishes, Validate returns the
// error [Registry.Lookup] returns for it, even with [WithRequireSchema] set
// false. A document that did not parse returns the syntax error
// [niceyaml.Node.Err] returns, even with [WithRequireSchema] set false, so
// a loop that validates every document of a file reports each syntax
// error beside the violations of the documents that parsed.
func (r *Registry) Validate(ctx context.Context, n *niceyaml.Node) error {
	err := n.Err()
	if err != nil {
		//nolint:wrapcheck // The source bound the syntax error already.
		return err
	}

	v, unmatched, err := r.lookup(ctx, n)
	if err != nil {
		if !r.requireSchema && unmatched {
			return nil
		}

		// The root of the document gives the error no location, as it
		// does in [Registry.Lookup].
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return n.Document().Bind(err)
	}

	//nolint:wrapcheck // Validation errors pass through unchanged.
	return n.Validate(ctx, v)
}

// Schema returns the compiled schema ref names. For a Ref from
// [Schema.Ref], that is the schema the Ref carries, as it is. For any
// other Ref, Schema compiles the bytes [Registry.Load] loads for it on
// the first request for its cache key, with the options
// [WithCompileOptions] gave the registry, and serves the result from the
// cache after that. The cache key is the [Ref.Key], and for a Ref from
// [FileFS] the file system beside it, so one path in two file systems is
// two schemas. [Registry.Lookup]
// takes the schema it validates with from here, so a caller that holds a
// Ref of its own, such as one that checks a Go value with
// [Schema.ValidateValue], shares the same load and compile:
//
//	s, err := reg.Schema(ctx, schema.URL(schemaURL))
//	if err != nil {
//		return err
//	}
//
//	return s.ValidateValue(ctx, value)
//
// The value came from no document, and the text of the error
// ValidateValue returns names the path of each violation, as in
// "$.port: 0 is less than 1", so the caller returns it as it is. A
// caller that places the errors in a document binds the same result
// there, as ValidateValue describes.
//
// The zero Ref names no schema, so it is [ErrResolve]. A load that fails
// is [ErrLoad], and a compile that fails is [ErrCompile]. Schema caches
// neither, so the next request for the cache key loads again. When ctx
// ends before the schema loads, Schema returns [ErrLoad] wrapping the
// context's error without waiting for the load to finish.
//
// Concurrent requests for one cache key share a single load and compile,
// and each caller waits for it only until its own context ends. The shared
// load runs under the context of the caller that started it and reports
// whether that context ended before the load failed. In that case a
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
	if ref.url && !httpfetch.IsHTTPURL(ref.key) {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), errNotHTTPURL)
	}

	// A Ref from File that names no file in this registry has no cache
	// key, and the check runs before the cache. A path File could not make
	// absolute shares its Key with the same path under the root, so it
	// would otherwise return the schema of that other file.
	key, err := r.cacheKey(ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), err)
	}

	if v, ok := r.cached(key); ok {
		return v, nil
	}

	for {
		ch := r.group.DoChan(key, func() (any, error) {
			s, err := r.compileRecovering(ctx, ref, key)

			// Report whether this caller's context ended before the load
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
				return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), ctx.Err())
			}

		case res = <-ch:
		}

		// The load hands back a panic or a call to runtime.Goexit as an
		// error, and each caller that shared it raises it again here.
		capture.Reraise(res.Err)

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

			return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), ctx.Err())
		}

		//nolint:wrapcheck // compile already wraps its errors with the sentinel and Key.
		return nil, res.Err
	}
}

// Load returns the bytes of the schema ref names. For a Ref from [File],
// Load reads the file from disk, or through the file system [WithFSAt]
// gave the registry. For a Ref from [FileFS], it reads the file from the
// file system of the Ref. For a Ref from [URL], it fetches the URL
// with the client [WithHTTPClient] gave the registry. For a Ref from
// [Loadable], it returns the bytes the Ref's load returns. Load reads the
// bytes on every call and caches nothing. [Registry.Schema] loads the
// same bytes once and compiles them, so Load is for a caller that wants
// the bytes themselves, such as one that prints a schema. For a file or
// URL whose fragment selects a subschema, Load returns the bytes of the
// whole document. An error wraps [ErrLoad].
//
// The zero Ref names no bytes, and a Ref from [Schema.Ref] carries a
// compiled schema rather than bytes, which [Ref.Schema] returns, so Load
// returns an error for either.
func (r *Registry) Load(ctx context.Context, ref Ref) ([]byte, error) {
	data, err := r.load(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), err)
	}

	return data, nil
}

// load is [Registry.Load] before wrapping the error with the sentinel and
// the key.
func (r *Registry) load(ctx context.Context, ref Ref) ([]byte, error) {
	switch {
	case ref.url:
		if !httpfetch.IsHTTPURL(ref.key) {
			return nil, errNotHTTPURL
		}

		//nolint:wrapcheck // The fetch error names the URL already.
		return httpfetch.Get(ctx, r.client, ref.key)

	case ref.file != "":
		path, err := filePath(ref)
		if err != nil {
			return nil, err
		}

		return r.readFile(ref.fsys, path)

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

// refDoc is a document a $ref names, as the registry keeps it.
type refDoc struct {
	// The error the search for fileURL returned.
	fileURLErr error
	// The document, parsed. Every schema that references the document
	// shares it, so nothing may change it. The compiler copies each
	// document it resolves and never changes the one it receives.
	schema *jsonschema.Schema
	// The first file URL an HTTP or HTTPS document names, as
	// [localFileURL] finds it. It and fileURLErr stay empty for a
	// document from any other scheme.
	fileURL string
}

// refDocument returns the document a $ref names by uri, a URI without a
// fragment, which the registry keeps under that URI. The first call whose
// load succeeds and whose bytes parse keeps the document in the registry,
// and every later call for uri returns the kept document instead of
// calling load. A load that fails and bytes that do not parse stay out of
// the registry, so the next call loads again. When two calls load one
// document at once, both return the document the first of them kept.
func (r *Registry) refDocument(key, uri string, load func() ([]byte, error)) (refDoc, error) {
	if doc, ok := r.keptRefDoc(key); ok {
		return doc, nil
	}

	data, err := load()
	if err != nil {
		return refDoc{}, err
	}

	s, err := jsonschema.ParseSchema(data)
	if err != nil {
		return refDoc{}, fmt.Errorf("parse %s: %w", httpfetch.Redacted(uri), err)
	}

	doc := refDoc{schema: s}
	if httpfetch.IsHTTPURL(uri) {
		doc.fileURL, doc.fileURLErr = localFileURL(data)
	}

	return r.keepRefDoc(key, doc), nil
}

// keptRefDoc returns the document the registry keeps under key, if any.
func (r *Registry) keptRefDoc(key string) (refDoc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	doc, ok := r.refDocs[key]

	return doc, ok
}

// keepRefDoc keeps doc under key and returns it, unless the registry keeps
// a document under key already, in which case it returns that document.
// So every schema sees one document per key.
func (r *Registry) keepRefDoc(key string, doc refDoc) refDoc {
	r.mu.Lock()
	defer r.mu.Unlock()

	if kept, ok := r.refDocs[key]; ok {
		return kept
	}

	r.refDocs[key] = doc

	return doc
}

// refOptions returns the options the registry compiles the schema ref
// names with: the options [WithCompileOptions] gave it, and options
// that let a $ref in a schema from [File] or [URL] name a document beside
// it. Such a schema takes base as the base URI of its references. That
// is the URL the registry caches a file under, or the key of a URL
// without any userinfo. The registry reads each document a reference
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
// means ref names a whole document.
//
// For a whole document, the options from [WithCompileOptions] come after
// the registry's own, so they win. For a fragment, they come first, and
// the registry's resolver wins, because only it can serve doc.
func (r *Registry) refOptions(ref Ref, base string, user keyUserinfo, doc *jsonschema.Schema) []CompileOption {
	if !ref.url && ref.file == "" {
		return r.compileOpts
	}

	allowFile := !ref.url

	// The cache key of ref held this text already, so the file system has
	// one. A file URL names a file in the file system of ref, and the
	// registry keeps the document apart from one of that URL in another.
	inFS, _ := r.fsKey(ref.fsys) //nolint:errcheck // The cache key of ref reported it.

	resolver := jsonschema.RefResolverFunc(func(ctx context.Context, uri string) (*jsonschema.Schema, error) {
		if i := strings.IndexByte(uri, '#'); i >= 0 {
			uri = uri[:i]
		}

		// The scheme decides what a schema may reach before the registry
		// looks for a kept document, so a schema from URL never receives a
		// local file that a schema from File loaded. The registry keeps the
		// document under key, which is uri for a URL and names the file
		// system beside it for a file.
		var (
			load func() ([]byte, error)
			key  string
		)

		switch {
		case httpfetch.IsHTTPURL(uri):
			uri = user.apply(uri)
			key = uri
			load = func() ([]byte, error) {
				//nolint:wrapcheck // The fetch error names the URL already.
				return httpfetch.Get(ctx, r.client, uri)
			}

		case allowFile && isFileURL(uri):
			path, ok := fileURLPath(uri)
			if !ok {
				return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
			}

			key = inFS + uri
			load = func() ([]byte, error) {
				return r.readFile(ref.fsys, path)
			}

		default:
			return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
		}

		doc, err := r.refDocument(key, uri, load)
		if err != nil {
			return nil, err
		}

		if allowFile {
			if doc.fileURLErr != nil {
				return nil, fmt.Errorf("parse %s: %w", httpfetch.Redacted(uri), doc.fileURLErr)
			}

			if doc.fileURL != "" {
				return nil, fmt.Errorf("%s: remote schema names local file %q", httpfetch.Redacted(uri), doc.fileURL)
			}
		}

		return doc.schema, nil
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

	// Only the registry holds the document the fragment names, so its
	// resolver comes last there, where a resolver of the caller cannot
	// replace it.
	if doc != nil {
		opts = append(opts, r.compileOpts...)

		return append(opts, WithJSONSchemaOptions(refOpts...))
	}

	opts = append(opts, WithJSONSchemaOptions(refOpts...))

	return append(opts, r.compileOpts...)
}

// cacheKey returns the key the registry caches the schema ref names
// under. That is [Ref.Key], except for a [Ref] that names a file, which
// the registry caches by the URL [fileBase] returns for it. For a file on
// disk, that URL is the Key. For a file in a file system, the text
// [Registry.fsKey] returns for the file system stands in front, so one
// path in two file systems is two entries. A Ref that names no file the
// registry can read has no cache key, and cacheKey returns the error its
// read reports.
func (r *Registry) cacheKey(ref Ref) (string, error) {
	if ref.file == "" {
		return ref.Key(), nil
	}

	base, err := fileBase(ref)
	if err != nil {
		return "", err
	}

	inFS, err := r.fsKey(ref.fsys)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ref.file, err)
	}

	return inFS + base, nil
}

// fileBase returns the URL the registry knows the file of ref by, a
// [Ref] from [File] or [FileFS], with the fragment of its key. A $ref in
// the schema resolves against it.
func fileBase(ref Ref) (string, error) {
	path, err := filePath(ref)
	if err != nil {
		return "", err
	}

	return fileURL(path) + fileFragment(ref), nil
}

// fsKey returns the text that tells fsys apart from every other file
// system in the keys of the registry, or "" for a nil fsys, which stands
// for the disk. The text opens with a NUL, which no URL holds.
//
// The registry knows a file system by its value when Go can compare the
// value, as it can an [embed.FS], a pointer, and the file system
// [os.DirFS] returns. It knows a map, such as an [fstest.MapFS], by the
// map itself. A value of any other type has no identity the registry can
// read, so fsKey returns [fs.ErrInvalid] for it.
func (r *Registry) fsKey(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", nil
	}

	var id any

	switch v := reflect.ValueOf(fsys); {
	case v.Comparable():
		id = fsys
	case v.Kind() == reflect.Map:
		id = mapIdentity(v.Pointer())
	default:
		return "", fmt.Errorf(
			"%w: a file system of type %T has no identity to cache a schema by: pass a pointer to it",
			fs.ErrInvalid, fsys,
		)
	}

	n, ok := r.keptFSNumber(id)
	if !ok {
		n = r.keepFSNumber(id, fsys)
	}

	return "\x00fs" + strconv.Itoa(n) + "\x00", nil
}

// keptFSNumber returns the number the registry keeps for the file system
// id names, if any. Every lookup of a schema in a file system asks for
// it, so it takes the read lock alone.
func (r *Registry) keptFSNumber(id any) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n, ok := r.fsIDs[id]

	return n, ok
}

// keepFSNumber gives fsys, the file system id names, the next number and
// returns it, unless the registry keeps a number for id already, in
// which case it returns that number. The registry holds fsys from then
// on. An id that is a [mapIdentity] holds nothing alive, and a later map
// at the address of a collected one would take its number.
func (r *Registry) keepFSNumber(id any, fsys fs.FS) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.fsIDs[id]
	if !ok {
		n = len(r.fsIDs) + 1
		r.fsIDs[id] = n
		r.fsKept = append(r.fsKept, fsys)
	}

	return n
}

// mapIdentity is the identity of a file system that is a map, which
// [Registry.fsKey] knows by the address of the map.
type mapIdentity uintptr

// fileFragment returns the fragment of the key of ref, a [Ref] from
// [File], behind its '#', or "" when the key has none. [fileURL] escapes
// a '#' in a file name, so the first one in the key opens the fragment
// that [FileOrURL] or [Directive] put there.
func fileFragment(ref Ref) string {
	i := strings.IndexByte(ref.key, '#')
	if i < 0 {
		return ""
	}

	return ref.key[i:]
}

// name returns the name of ref for a message. That is the key, as
// [Ref.name] returns it, except for a [Ref] that names its file in a
// file system. The registry names such a Ref by the path as given, with
// the fragment of its key, since that is the path the file system reads.
func (r *Registry) name(ref Ref) string {
	if ref.file == "" || ref.fsys == nil {
		return ref.name()
	}

	return ref.file + fileFragment(ref)
}

// cached returns the schema cached under key, if any.
func (r *Registry) cached(key string) (*Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.cache[key]

	return v, ok
}

// A flight carries the result of a shared load to every caller that
// joined it. It holds the schema the load compiled and whether the
// context of the caller that started the load ended before the load
// failed.
type flight struct {
	schema       *Schema
	starterEnded bool
}

// compileRecovering runs compile and turns a panic in the load or the
// compiler into a [*capture.PanicError] and a call to [runtime.Goexit]
// into [capture.ErrGoexit]. A panic on the singleflight group's goroutine
// would end the process, and DoChan never answers a flight whose function
// calls [runtime.Goexit], so compileRecovering keeps both off that
// goroutine.
func (r *Registry) compileRecovering(ctx context.Context, ref Ref, cacheKey string) (*Schema, error) {
	var s *Schema

	err := capture.Run(func() error {
		var err error

		s, err = r.compile(ctx, ref, cacheKey)

		return err
	})

	//nolint:wrapcheck // Reraise matches only the error Run returned, and compile wraps its own.
	return s, err
}

// compile loads and compiles the schema ref names, caches it under
// cacheKey, its [Registry.cacheKey], and returns it. When an earlier
// flight cached a schema under that key, compile returns that schema. The
// group runs one compile per key at a time and each compile checks the
// cache first, so every caller sees one schema per key.
func (r *Registry) compile(ctx context.Context, ref Ref, cacheKey string) (*Schema, error) {
	if v, ok := r.cached(cacheKey); ok {
		return v, nil
	}

	data, err := r.Load(ctx, ref)
	if err != nil {
		return nil, err
	}

	// The compiler resolves the references of the schema against base.
	// For a file, that is the URL of its path, on disk or in the file
	// system of the Ref. For a URL, it is the key without its userinfo,
	// and the resolver adds the userinfo back to each fetch.
	base, user := ref.Key(), keyUserinfo{}

	switch {
	case ref.url:
		base, user = splitUserinfo(base)
	case ref.file != "":
		base, err = fileBase(ref)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrLoad, r.name(ref), err)
		}
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
			return nil, fmt.Errorf("%w: %q: %w", ErrCompile, r.name(ref), err)
		}

		wrapper := map[string]string{"$ref": base}
		if doc.Schema != "" {
			wrapper["$schema"] = doc.Schema
		}

		data, err = json.Marshal(wrapper)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrCompile, r.name(ref), err)
		}
	}

	compiled, err := compileJSON(ctx, data, r.refOptions(ref, base, user, doc))
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrCompile, r.name(ref), err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache[cacheKey] = compiled

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
