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
	"strings"
	"time"
	"unsafe"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/capture"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

var (
	// ErrResolve indicates that a resolver applied to the document but
	// could not name its schema. The resolver either returned an error of
	// its own or returned the zero Ref and a nil error. A lookup whose
	// context ends before a resolver names a schema reports it too, and so
	// does [Registry.Schema] for the zero Ref. A [matcher.Content] or
	// [matcher.Text] guard that refuses a document for its aliases returns
	// [ErrExcessiveAliasing]. The document is at fault for that error, so
	// [niceyaml.IsInvalid] reports the ErrResolve around it.
	//
	// When a resolver error already matches ErrResolve, [ErrLoad], or
	// [ErrCompile], the lookup returns it unchanged and does not wrap it in
	// a second ErrResolve. Every error that a [Registry] returns as a
	// resolver of another registry, other than a decline, is such an error.
	ErrResolve = errors.New("resolve schema")

	// ErrLoad indicates that the registry could not load the schema, or a
	// document that a $ref of the schema names, as [Registry.Schema]
	// describes. The registry returns it as the Phase of a [*LoadError].
	ErrLoad = errors.New("load schema")

	// ErrScopedDocument indicates that a caller passed a [*niceyaml.Node]
	// from [niceyaml.Node.At] to [Registry.Lookup] or [Registry.Check],
	// which pick a schema for a whole document. Validate the document once
	// at its root, then decode its nodes without the registry.
	ErrScopedDocument = errors.New("registry needs a whole document")

	// The reason that a [URL] Ref fails to load when its scheme is neither
	// http nor https.
	errNotHTTPURL = errors.New("not an HTTP or HTTPS URL")

	// The client that fetches schemas for every registry that
	// [WithHTTPClient] did not give a client. An [http.Client] is safe for
	// concurrent use, so one serves them all.
	defaultHTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
)

// defaultHTTPTimeout bounds each schema fetch of a registry that
// [WithHTTPClient] did not give a client.
const defaultHTTPTimeout = 30 * time.Second

// defaultRetryAfter is how long a registry remembers a failure when
// [WithRetryAfter] did not give it an interval.
const defaultRetryAfter = 1 * time.Minute

// Registry maps YAML documents to schemas using pluggable resolvers.
//
// Lookup tries the resolvers that [WithResolvers] gave the registry, in
// order. The first [Resolver] that does not decline wins, as Resolver
// describes.
//
// The registry caches the schemas that it compiles by [Ref.Key], and a
// Ref from [FileFS] by its file system too. It consults that cache before
// loading. So after a schema compiles, the registry serves it to every
// later document that names it without loading it again. The registry
// remembers a failed load or compile for the interval that
// [WithRetryAfter] sets, one minute by default. Every document that names
// the Key inside the interval gets the error of that attempt. The first
// document after the interval loads the schema again.
//
// The registry drops a failure after its interval has passed. It keeps
// every schema that it compiles until [WithCacheLimit] drops the schema
// to make room or the program drops it. [Registry.Invalidate] drops the
// schemas of the documents that a program names, [Registry.Forget] the
// schemas of a file system, and [Registry.Purge] every schema. The
// registry holds each file system for as long as it keeps an entry from
// that file system, as [FileFS] describes. Each distinct Key adds an
// entry, whether its schema compiled or failed. A URL that differs from
// another only in its query string or fragment is a Key of its own.
//
// A schema stays in the cache after the file or URL that it came from
// changes, and so does each document that its $refs name. A program that
// watches its schema files, such as an editor, passes each file that
// changes to Invalidate. The next document that names a schema that
// Invalidate dropped loads it again.
//
// The registry compiles every schema with the options that
// [WithCompileOptions] gave it. [Registry.Schema] returns the compiled
// schema that a [Ref] names, through that cache, to a caller that holds
// a Ref of its own. A [*Schema] compiled elsewhere is a resolver too,
// and the registry validates with it unchanged.
//
// Example:
//
//	kindPath := paths.Doc().Child("kind")
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    // Directive matching first, for explicit user intent.
//	    schema.Directive(),
//	    // Content-based matching.
//	    schema.When(
//	        matcher.Content(kindPath, "Deployment"),
//	        schema.Embedded(deploymentSchema),
//	    ),
//	))
//
// A Registry is a [Resolver] too. So a registry that an [Access]
// confines can load the schemas that documents pick and serve them to a
// registry that holds the program's own schemas, as [Registry.Resolve]
// describes.
//
// Only the cache of a Registry changes after construction, so a Registry
// is safe for concurrent use. Create instances with [NewRegistry].
type Registry struct {
	group flightGroup[flightKey, flight] // one load and compile in flight per key and generation
	cache *cache                         // compiled schemas, the documents that a $ref names, and remembered failures
	registryConfig
}

// RegistryOption configures [Registry] creation.
//
// Available options:
//   - [WithResolvers]
//   - [WithCompileOptions]
//   - [WithRequireSchema]
//   - [WithHTTPClient]
//   - [WithRetryAfter]
//   - [WithAccess]
//   - [WithCacheLimit]
//   - [WithLoadLimit]
type RegistryOption func(*registryConfig)

// registryConfig holds the settings that a [RegistryOption] configures.
// An option takes it in place of the [Registry] that embeds it, so only
// [NewRegistry] can apply one.
type registryConfig struct {
	client      *http.Client // fetches the schemas that URL refs name
	resolvers   []Resolver
	compileOpts []CompileOption
	access      accessConfig  // the files on disk that the registry reads and the URLs that it fetches
	retryAfter  time.Duration // how long the registry remembers a failure
	cacheLimit  int           // the most entries that each cache keeps, or 0 for unlimited
	loadLimit   int           // the most bytes of each schema document that the registry reads
	// Makes Check report ErrNoMatch when every resolver declines.
	requireSchema bool
}

// WithHTTPClient is a [RegistryOption] that sets the client that the
// registry fetches schemas with. The registry fetches every [Ref] from
// [URL] with it, whether a resolver holds the Ref or [Directive] and
// [FileOrURL] build it from a reference in the input. The Timeout of the
// client bounds each fetch, beside the context of the lookup. So a
// schema host that accepts a connection and never answers cannot hang a
// caller whose context does not have a deadline:
//
//	reg := schema.NewRegistry(
//	    schema.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	)
//
// The default client bounds each fetch at 30 seconds and otherwise
// matches [http.DefaultClient]. A nil client keeps the default. A
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] fetches its
// catalog with a client of its own, because the catalog is not a schema
// that the registry loads.
//
// An [Access] from [WithAccess] can limit the URLs that the registry
// fetches with the client. The registry then fetches with a copy of the
// client and leaves the client itself unchanged.
func WithHTTPClient(client *http.Client) RegistryOption {
	return func(c *registryConfig) {
		if client != nil {
			c.client = client
		}
	}
}

// WithRetryAfter is a [RegistryOption] that sets how long the registry
// remembers a schema that failed to load or compile before it tries the
// schema again.
//
// Until the interval passes, every request for the schema returns the
// error of that attempt and does not load anything. The documents that
// name a broken schema, or a host that never answers, then cost one
// attempt per interval between them. The first request after the
// interval loads the schema again. For the same interval, the registry
// remembers each document that a $ref names and that failed to load, as
// [Registry.Schema] describes.
//
// After the interval has passed, the registry drops the failure from
// memory. Some documents name an unbounded number of distinct missing
// schemas, such as one file with a new fragment each time. Those
// documents then hold memory for one interval of failures at most.
//
// The interval covers only failures. The registry keeps a schema that
// compiled until [WithCacheLimit] drops it to make room, or until
// [Registry.Invalidate], [Registry.Forget], or [Registry.Purge] drops
// it. Each of those methods also drops the failures that it matches
// before their interval has passed. The registry does not remember the
// failure of a request whose context ended.
//
// Defaults to 1 minute. An interval of zero or less keeps the default,
// because an interval that short loads a broken schema again for every
// document.
func WithRetryAfter(interval time.Duration) RegistryOption {
	return func(c *registryConfig) {
		if interval > 0 {
			c.retryAfter = interval
		}
	}
}

// WithResolvers is a [RegistryOption] that appends resolvers to the end
// of the lookup order. Lookup tries them in the order given, and the
// first that does not report [ErrNoMatch] wins. So a program should list
// explicit user intent before content matching, and content matching
// before file path conventions:
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
// The option keeps its own copy of res, so a later write to the caller's
// slice does not change the option.
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
// [Registry.Check] reports a document that every resolver declines. The
// default is true, and such a document then fails with [ErrNoMatch].
// With false, Check accepts the document. So a registry that validates
// what it recognizes and passes the rest runs inside a decode through
// [niceyaml.WithValidator]:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	    schema.WithRequireSchema(false),
//	)
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// [Directive] lets each document pick its own schema. A program that
// decodes documents from another trust domain should confine the
// registry first, as the Untrusted Documents section of the package
// documentation describes.
//
// [Registry.Lookup] reports [ErrNoMatch] either way, because a caller
// that asks for the validator must learn when the lookup does not find
// one.
func WithRequireSchema(require bool) RegistryOption {
	return func(c *registryConfig) {
		c.requireSchema = require
	}
}

// WithCompileOptions is a [RegistryOption] that sets the [CompileOption]
// values that the registry compiles every schema with, as [Compile]
// takes them:
//
//	reg := schema.NewRegistry(schema.WithCompileOptions(
//	    schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
//	))
//
// The options apply when the registry compiles a schema, once per cache
// key, as [Registry.Schema] describes. So an option such as a format
// validator takes effect for every document that the registry validates
// against that schema. The registry uses a [*Schema] compiled elsewhere
// unchanged, with the options of its own compile.
//
// When these options set [WithRequireRefs] to false, the registry
// compiles a schema whose $ref names a document that does not load, as
// [Registry.Schema] describes.
//
// A [jsonschema.WithRefResolver] among these options replaces the
// registry's own resolution of the $refs in a whole schema from [File]
// or [URL]. A relative $ref there then resolves only when that resolver
// serves it. The registry cannot tell whether it does, so such a schema
// compiles whatever [WithRequireRefs] sets. A [Ref] that names a
// subschema by a fragment, such as
// "https://example.com/defs.json#/$defs/a", always resolves its $refs
// through the registry, because only the registry holds the document
// that the fragment points into. A resolver among these options does not
// apply to it.
//
// The option keeps its own copy of opts, so a later write to the
// caller's slice does not change the option, even for a registry built
// later. Given more than once, each call appends after the options of
// the one before it.
func WithCompileOptions(opts ...CompileOption) RegistryOption {
	opts = slices.Clone(opts)

	return func(c *registryConfig) {
		c.compileOpts = append(c.compileOpts, opts...)
	}
}

// NewRegistry creates a new [*Registry].
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		client:        defaultHTTPClient,
		access:        accessConfig{anyFile: true, anyURL: true},
		retryAfter:    defaultRetryAfter,
		loadLimit:     DefaultLoadLimit,
		requireSchema: true,
	}
	for _, opt := range opts {
		opt(&r.registryConfig)
	}

	// The access checks each hop of a redirect, so a schema host cannot
	// send the fetch elsewhere.
	r.client = r.access.guard(r.client)

	r.cache = newCache(r.cacheLimit, r.retryAfter)

	return r
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if every resolver declines the document, with a
// detail from [niceyaml.WithDetails] for each resolver that gave a
// reason, as ErrNoMatch describes. The lookup failed once, so the
// message of the error is one line. The error matches each reason as
// well as ErrNoMatch, so [errors.Is] finds a reason such as
// [ErrNoDirective], except the error of a context. A rendering of the
// error lists the reasons below the message.
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draws:
//
//	app.yaml: no matching schema
//	├── no schema directive
//	└── no catalog entry matches: "/srv/app/app.yaml"
//
// [niceyaml.FormatError] draws the same tree with ASCII lines.
//
// In a file that holds several documents, the message names the document
// that the lookup ran for, as "app.yaml: document 3: no matching
// schema". A registry among the resolvers gives the reason of each of
// its own resolvers, as [Registry.Resolve] describes.
//
// Returns [ErrResolve] if a resolver applied but could not name the
// schema. Returns an error that holds a [*LoadError] matching [ErrLoad]
// or [ErrCompile] if loading or compiling the schema fails.
//
// When ctx ends before a resolver names a schema, or before Lookup finds
// that every resolver declines, Lookup returns [ErrResolve] wrapping the
// context's error, even when the resolvers ignore their context. When
// ctx ends after that, before the named schema finishes loading, Lookup
// returns [ErrLoad] wrapping the context's error without waiting for the
// load. Either way [errors.Is] finds the context's error, such as
// [context.Canceled] or [context.DeadlineExceeded]. A resolver that is a
// [Registry] returns the errors of its own lookup, and Lookup returns
// them unchanged, as [Registry.Resolve] describes. So when ctx ends
// during the load of that registry, Lookup returns the ErrLoad of that
// registry.
//
// An empty document, such as one that holds only comments, is the null
// document, and a resolver sees it as it sees any other. A document that
// did not parse does not have content for a resolver to read. So Lookup
// returns the syntax error that [niceyaml.Node.Err] returns and does not
// ask any resolver.
//
// The resolvers pick a schema for a whole document, from its file path,
// its preamble, or its content, so n must be the root [niceyaml.Node] of
// a document. A Node from [niceyaml.Node.At] fails with
// [ErrScopedDocument]. The schema that Lookup picks belongs to the
// document, so a caller who applies it to the node checks the node
// against the wrong schema. Validate one node against a schema of its
// own with a [Schema].
//
// Lookup binds every error to the root of the document through
// [niceyaml.Node.Bind], so its message names the file that the document
// came from. An error of the lookup is about the schema rather than a
// value of the document. So it does not carry a position, even when n is
// a Node from [niceyaml.Node.At].
//
// For most uses, prefer [Registry.Check], which combines lookup and
// validation. Use Lookup when you need the validator for custom
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

// Resolve implements [Resolver], so one registry is a resolver of
// another. Resolve names the schema that [Registry.Lookup] finds for
// doc, as a [Ref] from [Schema.Ref]. This registry loads and compiles
// that schema under its own [Access], cache, and limits. The registry
// that asked validates with the schema unchanged and does not load
// anything for it. A $ref that the validation reaches loads through this
// registry too.
//
// So a program can confine the schemas that its documents pick and trust
// its own schemas and those of a catalog, each in a registry of its own:
//
//	untrusted := schema.NewRegistry(
//	    schema.WithAccess(access),
//	    schema.WithCacheLimit(1024),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    untrusted,
//	    schemastore.New(),
//	    schema.File("/etc/app/schema.json"),
//	))
//
// A schema that both registries name compiles once in each of them.
//
// # Errors
//
// When every resolver of this registry declines doc, Resolve returns the
// error that Lookup returns, unbound. That error matches [ErrNoMatch].
// The registry that asked then moves on to its next resolver. It lists
// the reason that each resolver of this registry gave among the reasons
// of its own resolvers, as Lookup describes. A resolver that wraps that
// error in context of its own, such as a [ResolverFunc] that calls
// Resolve, gives one reason with that context.
//
// Any other error ends the lookup of the registry that asked, and that
// registry returns it without adding an ErrResolve of its own. An error
// that matches [ErrResolve], [ErrLoad], or [ErrCompile] ends the lookup
// even when it wraps ErrNoMatch. So a directive that names a schema that
// does not load fails the lookup with that load error.
//
// Lookup binds its error to the document, but Resolve returns the error
// unbound, because the registry that asked binds it. For a document that
// did not parse, Resolve returns the syntax error that
// [niceyaml.Node.Err] returns.
func (r *Registry) Resolve(ctx context.Context, doc *niceyaml.Node) (Ref, error) {
	err := doc.Err()
	if err != nil {
		//nolint:wrapcheck // The source bound the syntax error already.
		return Ref{}, err
	}

	s, _, err := r.lookup(ctx, doc)
	if err != nil {
		return Ref{}, err
	}

	return s.Ref(), nil
}

// lookup is [Registry.Lookup] before Lookup binds the error to the
// document. It reports true when every resolver declined the document. A
// load error might wrap [ErrNoMatch] too, so the error alone cannot
// tell.
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
			if err == nil || isDecline(err) || !errors.Is(err, ctxErr) {
				err = ctxErr
			}

			return nil, false, resolveError(err)
		}

		if err != nil && !isDecline(err) {
			return nil, false, resolveError(err)
		}

		if err != nil {
			reasons = append(reasons, declineReasons(err)...)

			continue
		}

		v, err := r.Schema(ctx, ref)

		return v, false, err
	}

	return nil, true, noMatch(reasons)
}

// isDecline reports whether err, the error of a resolver, declines the
// document. A decline matches [ErrNoMatch] and does not match
// [ErrResolve], [ErrLoad], or [ErrCompile]. The error of a [Registry]
// that is a resolver can match ErrNoMatch beside one of those, as a load
// error does. Such an error is not a decline.
func isDecline(err error) bool {
	return errors.Is(err, ErrNoMatch) && !isLookupError(err)
}

// resolveError wraps err, the error of a resolver that did not name a
// schema, in [ErrResolve]. It returns err unchanged when err already
// matches ErrResolve, [ErrLoad], or [ErrCompile], as the error of a
// [Registry] that is a resolver does.
func resolveError(err error) error {
	if isLookupError(err) {
		return err
	}

	return fmt.Errorf("%w: %w", ErrResolve, err)
}

// isLookupError reports whether err matches [ErrResolve], [ErrLoad], or
// [ErrCompile], the sentinels of the errors that a lookup returns.
func isLookupError(err error) bool {
	return errors.Is(err, ErrResolve) || errors.Is(err, ErrLoad) || errors.Is(err, ErrCompile)
}

// declineReasons returns the reasons that a lookup lists for err, the
// decline of a resolver. A [Registry] that is a resolver declines with
// the reasons of its own resolvers. The lookup lists each of them in
// place of that decline. A decline that adds context of its own around
// the decline of a Registry is one reason, so the lookup keeps that
// context. Any other decline is a reason of its own too.
func declineReasons(err error) []error {
	declined, ok := errors.AsType[noMatchError](err)
	if !ok || err.Error() != ErrNoMatch.Error() {
		return []error{err}
	}

	return declined.reasons
}

// noMatch returns the error that a lookup reports when every resolver
// declined. The error is a [noMatchError] inside a [*niceyaml.Error]
// from [niceyaml.Invalid]. The reason of each resolver that said more
// than ErrNoMatch becomes a detail from [niceyaml.WithDetails], in
// lookup order. The document does not name a schema that the registry
// knows, so [niceyaml.IsInvalid] reports the error. The lookup failed
// once, so the error is one problem, and its message is one line.
// [niceyaml.FormatError] lists the reasons below the message.
func noMatch(reasons []error) error {
	var details []error

	for _, reason := range reasons {
		if reason.Error() != ErrNoMatch.Error() {
			details = append(details, reasonError{err: reason})
		}
	}

	return niceyaml.Invalid(noMatchError{reasons: reasons}, niceyaml.WithDetails(details...))
}

// noMatchError is the [ErrNoMatch] that a lookup reports when every
// resolver declined. Its message is the message of ErrNoMatch. The
// reasons that the resolvers gave show as details of the
// [*niceyaml.Error] around it. [errors.Is] and [errors.As] pass over
// details, so noMatchError matches each reason itself, as its Is and As
// methods describe.
type noMatchError struct {
	// The error that each resolver declined with, in lookup order.
	reasons []error
}

func (e noMatchError) Error() string {
	return ErrNoMatch.Error()
}

// Is reports whether target is [ErrNoMatch], or whether a reason matches
// target, as [errors.Is] reports it. So a lookup matches a decline
// sentinel such as [ErrNoDirective], and a sentinel that a decline wraps
// beside ErrNoMatch with a second %w verb.
//
// The error of a context is the exception, and the no-match matches
// neither [context.Canceled] nor [context.DeadlineExceeded]. A reason can
// wrap such an error when a context ended inside a resolver with a
// deadline of its own. The lookup continued past that resolver, so the
// call did not stop. [niceyaml.Invalid] declares the document at fault
// for every no-match, but not for an error that matches the error of a
// context.
func (e noMatchError) Is(target error) bool {
	if target == ErrNoMatch {
		return true
	}

	if errors.Is(target, context.Canceled) || errors.Is(target, context.DeadlineExceeded) {
		return false
	}

	return slices.ContainsFunc(e.reasons, func(reason error) bool { return errors.Is(reason, target) })
}

// As reports whether [errors.As] finds target in a reason, and sets
// target from the first reason that holds it.
func (e noMatchError) As(target any) bool {
	return slices.ContainsFunc(e.reasons, func(reason error) bool { return errors.As(reason, target) })
}

// reasonError is the reason that one resolver declined a document, a
// detail of the [ErrNoMatch] that the lookup reports. The decline of
// each resolver wraps ErrNoMatch, so its message repeats the sentinel
// that the message of the lookup states already. The reason reads
// without that sentinel.
type reasonError struct {
	err error
}

func (e reasonError) Error() string {
	return strings.TrimPrefix(e.err.Error(), ErrNoMatch.Error()+": ")
}

func (e reasonError) Unwrap() error {
	return e.err
}

// Check validates a document using the first matching schema. It
// combines schema lookup and validation into a single call. Use
// [Registry.Lookup] when you need the validator for custom processing.
// Check takes the root [niceyaml.Node] of a document. It returns the
// violations of a document that does not conform to its schema. It
// returns [ErrNoMatch] when every resolver declines the document, unless
// [WithRequireSchema] set false. Check implements [niceyaml.Validator],
// so [niceyaml.WithValidator] runs it before a decode and
// [niceyaml.Node.Validate] runs it on its own:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// # Scoped Nodes
//
// The resolvers pick a schema for a whole document, so n must be the
// root [niceyaml.Node] of one. A Node from [niceyaml.Node.At] fails with
// [ErrScopedDocument], as it does in [Registry.Lookup]. A registry
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
// # Unmatched Documents
//
// Returns [ErrNoMatch] if every resolver declines the document. The
// document does not name a schema that the registry knows, so
// [niceyaml.IsInvalid] reports that error. A caller that wants such a
// document to pass builds the registry with [WithRequireSchema] set
// false. Check then accepts the document:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schema.Directive()),
//	    schema.WithRequireSchema(false),
//	)
//
// A document that a resolver applies to never passes that way, even when
// the load error of its schema wraps ErrNoMatch. A caller that holds a
// registry that someone else built to require a schema checks for the
// error at its own call site instead. Such a load error matches both
// [ErrLoad] and ErrNoMatch, so the check tests for ErrLoad first:
//
//	err := doc.Validate(ctx, reg)
//	if err != nil && (errors.Is(err, ErrLoad) || !errors.Is(err, ErrNoMatch)) {
//	    return err
//	}
//
// # Empty Documents
//
// An empty document validates as any other does, so it fails with
// ErrNoMatch when every resolver declines it.
// [niceyaml.Source.Documents] and [niceyaml.Source.ValidateDocuments]
// leave out the empty documents of a file that holds a document with
// content, such as the one that a trailing "---" leaves at the end of a
// file. [niceyaml.SkipEmpty] wraps the registry to pass an empty
// document wherever the registry runs, such as on a file that is empty
// as a whole:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// # Errors
//
// Returns validation errors if the document does not conform to the
// schema. Returns resolution, loading, or compilation errors if schema
// preparation fails. When ctx ends before the lookup finishes, Check
// returns the error that [Registry.Lookup] returns for it, even with
// [WithRequireSchema] set false. For a document that did not parse,
// Check returns the syntax error that [niceyaml.Node.Err] returns, even
// with [WithRequireSchema] set false. So a loop that validates every
// document of a file reports each syntax error beside the violations of
// the documents that parsed.
//
// Check binds every error that it returns. The error of the lookup is
// about the schema and not a value of the document. So Check binds that
// error through the root of the document, as [Registry.Lookup] does. It
// runs the schema through [niceyaml.Node.Validate], which binds each
// violation. So a direct call returns the error that Node.Validate
// returns for the registry, but [Schema.Check] returns its violations as
// it wrote them.
func (r *Registry) Check(ctx context.Context, n *niceyaml.Node) error {
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

// Schema returns the compiled schema that ref names. For a Ref from
// [Schema.Ref], Schema returns the schema that the Ref carries,
// unchanged. For any other Ref, Schema compiles the bytes that
// [Registry.Load] loads for it on the first request for its cache key.
// It compiles with the options that [WithCompileOptions] gave the
// registry, and it serves the result from the cache after that. The
// cache key is the [Ref.Key], plus the file system for a Ref from
// [FileFS]. So one path in two file systems is two schemas.
//
// A load that fails returns an error matching [ErrLoad], and a compile
// that fails returns one matching [ErrCompile]. [Registry.Lookup] gets
// the schema that it validates with from Schema. So a caller that holds
// a Ref of its own, such as one that checks a Go value with
// [Schema.ValidateValue], shares the same load and compile:
//
//	s, err := reg.Schema(ctx, schema.URL(schemaURL))
//	if err != nil {
//		return err
//	}
//
//	return s.ValidateValue(ctx, value)
//
// The value did not come from a document. The text of the error that
// ValidateValue returns names the path of each violation, as in "$.port:
// 0 is less than 1", so the caller returns it unchanged. A caller that
// places the errors in a document binds the same result there, as
// ValidateValue describes.
//
// # Errors
//
// The zero Ref does not name a schema, so it fails with [ErrResolve]. A
// failed load returns [ErrLoad], and a failed compile returns
// [ErrCompile]. The registry remembers either failure under the cache
// key for the interval that [WithRetryAfter] sets. Every request for the
// key inside the interval returns the error of that attempt and does not
// load anything. So a schema that many documents name costs one attempt
// per interval. The first request after the interval loads again.
//
// When ctx ends before the schema loads, Schema returns [ErrLoad]
// wrapping the context's error without waiting for the load to finish. A
// load that fails under a context that ended does not say anything about
// the schema. So the registry does not remember the failure, and the
// next request loads again.
//
// The error of a failed load or compile is a [*LoadError] that names
// ref. It holds a [*FetchError] for each file or URL that the registry
// could not read, such as one past the limit of [WithLoadLimit] or a
// response with a status other than 200 OK.
//
// # References
//
// A schema from [File], [FileFS], or [URL] loads only when each document
// that its $refs name loads too. The compile resolves every $ref in the
// schema and in each document that it loads for a $ref, whether or not a
// document can reach the $ref. When one of those documents does not
// load, Schema returns an error wrapping [ErrLoad] that names the schema
// and holds the error of each such document:
//
//	load schema: "file:///srv/root.json": cannot resolve $ref: read /srv/defs.json: ...
//
// The registry remembers those errors as it does any other failed load.
// A request inside the retry interval returns them and does not load
// anything. The first request after the interval loads the schema and
// those documents again. When a document failed to load because ctx
// ended, the registry does not remember anything, and the next request
// compiles again.
//
// When the options of [WithCompileOptions] set [WithRequireRefs] to
// false, the registry compiles such a schema. A validation that reaches
// the $ref then fails with an error wrapping [ErrValidate]. For the
// retry interval, the registry remembers the document that failed to
// load. So the validations inside the interval do not load anything, and
// the first one after it loads the document again.
//
// A schema that the registry holds as bytes, such as one from
// [Embedded], does not have a location to resolve a $ref against. So a
// $ref to another document fails its compile with [ErrCompile], as it
// does in [Compile].
//
// # Concurrent Requests
//
// Concurrent requests for one cache key share a single load and compile.
// A request that starts after [Registry.Invalidate] or [Registry.Purge]
// returns does not share a load that started before the call.
//
// Each caller waits for the shared load only until its own context ends.
// The shared load runs under the context of the caller that started it
// and reports whether that context ended before the load failed. When it
// did, a caller with a live context loads again, and a caller whose
// context has ended returns [ErrLoad] wrapping its own context's error.
// Any other failure reaches every caller that shared the load, including
// a timeout inside the load whose error wraps a context error. A panic
// or a call to [runtime.Goexit] in the load happens again in every
// caller that shared it. The registry does not remember a panic or a
// Goexit, so the next request runs the load again.
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
		return nil, &LoadError{Ref: ref, Phase: ErrLoad, Err: errNotHTTPURL}
	}

	// A Ref from File that names no file in this registry has no cache
	// key, and the check runs before the cache. A path File could not make
	// absolute shares its Key with the same path under the root, so it
	// would otherwise return the schema of that other file.
	key, err := keyOf(ref)
	if err != nil {
		return nil, &LoadError{Ref: ref, Phase: ErrLoad, Err: err}
	}

	for {
		e, gen, ok := r.cache.schema(key)
		if ok {
			return r.answer(ref, e)
		}

		// A request after a release starts a load of its own, so it never
		// shares a load that started before the release.
		call := r.group.do(flightKey{key: key, gen: gen}, func() (flight, error) {
			s, err := r.compile(ctx, ref, key, gen)

			// Report whether this caller's context ended before the load
			// failed, so a joiner can tell that cancellation apart from a
			// failure of the load itself.
			return flight{schema: s, starterEnded: err != nil && ctx.Err() != nil}, err
		})

		select {
		case <-ctx.Done():
			// The load this caller waits on may have finished in the same
			// instant the context ended. Its result counts when it is ready,
			// so the outcome does not depend on which case the select picks.
			// A schema that a later load cached does not count, because this
			// caller's context ended before that load began.
			select {
			case <-call.done:
			default:
				return nil, &LoadError{Ref: ref, Phase: ErrLoad, Err: ctx.Err()}
			}

		case <-call.done:
		}

		// The load hands back a panic or a call to runtime.Goexit as an
		// error, and each caller that shared it raises it again here.
		capture.Reraise(call.err)

		if call.err == nil {
			return call.val.schema, nil
		}

		// A load that failed because its starter's context ended says
		// nothing about the schema, so a caller with a live context loads
		// again, and a caller whose context has ended reports its own
		// context's error rather than the starter's.
		if call.val.starterEnded {
			if ctx.Err() == nil {
				continue
			}

			return nil, &LoadError{Ref: ref, Phase: ErrLoad, Err: ctx.Err()}
		}

		//nolint:wrapcheck // compile returns a LoadError already.
		return nil, call.err
	}
}

// Load returns the bytes of the schema that ref names. For a Ref from
// [File], Load reads the file from disk, and the [Access] of the registry
// decides which files Load reads. For a Ref from [FileFS], it reads the
// file from the file system of the Ref. For a Ref from [URL], it fetches
// the URL with the client that [WithHTTPClient] gave the registry. It
// refuses a URL that the Access does not allow. For a Ref from
// [Loadable], it returns the bytes that the load function of the Ref
// returns.
//
// Load reads the bytes on every call. It does not cache anything, and it
// reads again after a failure without waiting for the interval of
// [WithRetryAfter]. [Registry.Schema] loads the same bytes once and
// compiles them, so Load is for a caller that wants the bytes
// themselves, such as one that prints a schema. For a file or URL whose
// fragment selects a subschema, Load returns the bytes of the whole
// document. A file or URL larger than the limit of [WithLoadLimit] fails
// to load. Each error is a [*LoadError] that matches [ErrLoad], with a
// [*FetchError] inside it for a file or URL that Load could not read.
//
// The bytes are the text that the file, the URL, or the load holds, in
// JSON or YAML. Load does not convert one format into the other, so a
// caller that parses the bytes picks the reader by their first byte, as
// [Compile] does.
//
// The zero Ref does not name any bytes. A Ref from [Schema.Ref] carries
// a compiled schema rather than bytes, and [Ref.Schema] returns that
// schema. Load returns an error for both kinds of Ref.
func (r *Registry) Load(ctx context.Context, ref Ref) ([]byte, error) {
	data, err := r.load(ctx, ref)
	if err != nil {
		return nil, &LoadError{Ref: ref, Phase: ErrLoad, Err: err}
	}

	return data, nil
}

// Invalidate drops each schema, remembered failure, and $ref document
// that the registry keeps from the documents that refs name. It also
// drops each schema or remembered failure whose compile, or a validation
// with it, reached one of those documents through a $ref. The next
// request for a schema that Invalidate dropped loads and compiles it
// again. A program that watches its schema files, such as an editor,
// calls Invalidate for the files that change:
//
//	reg.Invalidate(schema.File(path))
//
// Invalidate matches schemas by the document that they reached, not by
// the Ref that a request named. Two schemas whose $refs name one
// defs.json share the document. Invalidate for defs.json drops both
// schemas and the document, so every schema that the registry compiles
// after the call reads the new defs.json.
//
// A Ref names its document by its [Ref.Key] without the fragment, so
// Invalidate drops the schema of every fragment of the document. A Ref
// from [FileFS] names the document in its file system. A $ref names a
// file by the path that the registry reads, whatever query or localhost
// authority its URL carries. A URL names its document whatever userinfo
// it carries. It names the same document in every spelling that the
// normal form of RFC 3986 section 6.2.2 makes one URL, such as one with
// an upper-case host or dot segments.
//
// Invalidate matches a file by its path as the Ref spells it. A symbolic
// link to the file, or its path in another letter case, names another
// document. Invalidate ignores the zero Ref, a Ref from [Schema.Ref],
// and a Ref that does not name a file that the registry can read. A Ref
// from FileFS does not name such a file when its path leaves the file
// system, or when the file system does not have an identity, as FileFS
// describes.
//
// The registry does not check a URL that it fetched for changes.
// Invalidate for a URL drops that document and each schema that reached
// it. It does not drop the documents that its $refs name, which a
// program cannot list. A program that needs fresh copies of its remote
// schemas calls [Registry.Purge] on a schedule of its own.
//
// Invalidate acts only on the cache. A [*Schema] that the registry
// returned before Invalidate stays unchanged, so a caller that holds one
// asks the registry for the schema again. A load or compile that runs
// when Invalidate starts still answers the requests that share it. The
// registry does not keep anything from such a load, whatever documents
// it names. A request that starts after Invalidate returns does not
// share that load.
//
// So each call to Invalidate that names a document keeps every load in
// flight out of the cache. A program that sees several files change at
// once, such as an editor that waits for a burst of saves to end, passes
// them all to one call. It passes only the files that the registry might
// have read, such as its schema files. Otherwise a slow load might never
// enter the cache, and every request loads it again.
func (r *Registry) Invalidate(refs ...Ref) {
	ids := make(map[docID]struct{}, len(refs))

	for _, ref := range refs {
		if ref.Key() == "" {
			continue
		}

		// A Ref that has no cache key names no file the registry reads,
		// so the registry keeps nothing under it.
		key, err := keyOf(ref)
		if err != nil {
			continue
		}

		ids[docOf(key)] = struct{}{}
	}

	if len(ids) == 0 {
		return
	}

	r.cache.invalidate(ids)
}

// Forget drops every schema, remembered failure, and $ref document that
// the registry keeps from fsys, so the registry stops holding fsys. A
// service that opens each upload in a file system of its own calls
// Forget when it finishes with an upload. The registry then stops
// holding the upload without waiting for the limit of [WithCacheLimit]:
//
//	source, err := niceyaml.NewSourceFromFS(upload, "app.yaml")
//	if err != nil {
//	    return err
//	}
//
//	defer reg.Forget(upload)
//
//	return source.ValidateDocuments(ctx, reg)
//
// The next request for a schema in fsys loads it again. Forget acts on
// the file system of a Ref from [FileFS], or of a document in a file
// system, as [RefBeside] describes. The registry knows every file that
// it reads through the root of [AllowRoot] as a file on disk, so Forget
// leaves those schemas in place. For a nil fsys, or a file system that
// the registry does not keep anything from, Forget does not change the
// registry.
//
// Forget acts only on the cache, as [Registry.Invalidate] does, so a
// [*Schema] that the registry returned before Forget stays unchanged.
// Unlike Invalidate, Forget does not discard the loads in flight, so the
// schemas that other uploads load at the same time still enter the
// cache. When a load from fsys runs at the same time as Forget, the
// registry might keep what that load returns after Forget returns. A
// validation with a Schema from fsys after Forget might load a $ref
// document from fsys again, and the registry then holds fsys again.
func (r *Registry) Forget(fsys fs.FS) {
	if fsys == nil {
		return
	}

	id, err := identify(fsys)
	if err != nil {
		return
	}

	r.cache.forget(id)
}

// Purge drops every schema, remembered failure, and $ref document that
// the registry keeps. The registry then stops holding every file system.
// The next request for any schema loads and compiles it again. Every
// holder of the registry, such as a [niceyaml.WithValidator] option, sees
// the emptied cache. So a program calls Purge instead of building a new
// registry and giving it to every holder again.
//
// Purge acts only on the cache, as [Registry.Invalidate] does. A
// [*Schema] that the registry returned before Purge stays unchanged. The
// registry does not keep anything from a load or compile that runs when
// Purge starts. A validation with a Schema from before Purge might load
// a $ref document again, and the registry then keeps it.
func (r *Registry) Purge() {
	r.cache.purge()
}

// load is [Registry.Load] before Load wraps the error in a [*LoadError].
func (r *Registry) load(ctx context.Context, ref Ref) ([]byte, error) {
	switch {
	case ref.url:
		if !httpfetch.IsHTTPURL(ref.key) {
			return nil, errNotHTTPURL
		}

		// The caller names the URL beside the error, so the refusal
		// does not repeat it.
		if !r.access.allowsURL(ref.key) {
			return nil, ErrURLNotAllowed
		}

		return r.fetch(ctx, ref.key)

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

// fetch returns the body of rawURL, an HTTP or HTTPS URL, which the
// registry fetches with its client within its load limit. A fetch that
// fails after the URL passes the checks of [httpfetch.Get] returns a
// [*FetchError].
func (r *Registry) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	data, err := httpfetch.Get(ctx, r.client, rawURL, r.loadLimit)
	if fetchErr, ok := errors.AsType[*httpfetch.Error](err); ok {
		return nil, &FetchError{URL: fetchErr.URL, StatusCode: fetchErr.StatusCode, Err: fetchErr.Err}
	}

	//nolint:wrapcheck // The error of a URL that fails the checks of Get names the URL already.
	return data, err
}

// localFileURL returns the first file URL that the schema document data
// names in a $id, $ref, or $dynamicRef member, or "" when it does not
// name one. A member name matches in any case, because the schema
// decoder reads "$REF" as $ref. The search covers every object in the
// document, not only its subschemas, because a JSON pointer $ref can
// reach an object under an unknown keyword and resolve the references
// there.
//
// It reads data as JSON or as YAML by the rule that [parseDocument]
// follows, with YAML text within limit. For YAML, it searches the
// decoded value, where each alias, merge key, and escape holds what it
// stands for.
func localFileURL(ctx context.Context, data []byte, limit int) (string, error) {
	if !isJSONObject(data) {
		doc, err := decodeYAML(ctx, data, limit)
		if err != nil {
			return "", fmt.Errorf("decode schema: %w", err)
		}

		return findFileURL(doc), nil
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var doc any

	err := dec.Decode(&doc)
	if err != nil {
		return "", fmt.Errorf("decode schema: %w", err)
	}

	return findFileURL(doc), nil
}

// findFileURL is [localFileURL] for a decoded value. It visits object
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
// Unicode case folding, the match that encoding/json uses for struct
// fields.
func isRefKeyword(key string) bool {
	return slices.ContainsFunc([]string{"$id", "$ref", "$dynamicRef"}, func(kw string) bool {
		return strings.EqualFold(key, kw)
	})
}

// refDoc is what the registry keeps for a document that a $ref names:
// the document, or the error of the last attempt to load and parse it.
// The registry answers with the error until the retry interval has
// passed since the attempt ended.
type refDoc struct {
	// The error of an attempt that did not load a document, or nil for a
	// document that loaded.
	err error
	// The error that the search for fileURL returned.
	fileURLErr error
	// The document, parsed. Every schema that references the document
	// shares it, so code must not change it. The compiler copies each
	// document that it resolves and never changes the one that it
	// receives.
	schema *jsonschema.Schema
	// The first file URL that an HTTP or HTTPS document names, as
	// [localFileURL] finds it. The fileURL and fileURLErr fields stay empty
	// for a document from any other scheme.
	fileURL string
}

// refDocument returns the document that a $ref names by uri, a URI
// without a fragment, for the schema whose reach is rc. It records in rc
// that the schema reached the document. The registry keeps the document
// under key.
//
// The first call whose load succeeds and whose bytes parse keeps the
// document in the registry. Every later call for key returns the kept
// document instead of calling load. The registry keeps the document
// until [WithCacheLimit] or a release such as [Registry.Invalidate]
// drops it. When two calls load one document at once, both return the
// document that the first of them kept.
//
// The registry remembers a load that fails, and bytes that do not parse,
// for the retry interval. A call inside the interval returns that error
// instead of calling load, and the first call after the interval loads
// again. A failure under a context that ended does not say anything
// about the document, so the registry does not remember the failure. A
// document that loads takes the place of a failure.
func (r *Registry) refDocument(
	ctx context.Context, key cacheKey, uri string, load func() ([]byte, error), rc *reach,
) (refDoc, error) {
	doc, gen, ok := r.cache.doc(key, rc)
	if ok {
		return doc, doc.err
	}

	doc, err := loadRefDoc(ctx, uri, load, r.loadLimit)

	var failedAt time.Time

	if err != nil {
		if ctx.Err() != nil {
			return refDoc{}, err
		}

		doc, failedAt = refDoc{err: err}, time.Now()
	}

	doc = r.cache.keepDoc(key, doc, failedAt, gen)

	return doc, doc.err
}

// loadRefDoc calls load for the bytes of the document that a $ref names
// by uri and parses them, with YAML text within limit. For an HTTP or
// HTTPS uri, it also searches the document for a file URL, as
// [localFileURL] describes.
func loadRefDoc(ctx context.Context, uri string, load func() ([]byte, error), limit int) (refDoc, error) {
	data, err := load()
	if err != nil {
		return refDoc{}, err
	}

	s, err := parseDocument(ctx, data, limit)
	if err != nil {
		return refDoc{}, fmt.Errorf("parse %s: %w", httpfetch.Redacted(uri), err)
	}

	doc := refDoc{schema: s}
	if httpfetch.IsHTTPURL(uri) {
		doc.fileURL, doc.fileURLErr = localFileURL(ctx, data, limit)
	}

	return doc, nil
}

// refOptions returns the compile options for the schema that ref names.
// They are the options that [WithCompileOptions] gave the registry, and
// options that let a $ref in a schema from [File] or [URL] name a
// document beside it. Such a schema takes base as the base URI of its
// references. The base is the URL that the registry caches a file under,
// or the key of a URL without its userinfo.
//
// The registry reads each document that a reference names as it reads
// the schema, so a relative $ref resolves against the file or URL that
// holds it. The registry loads each such document once and keeps it for
// every schema that references it, at compile time and during
// validation. It keeps the document until [WithCacheLimit] or a release
// such as [Registry.Invalidate] drops it. The resolver records each
// document in rc, the reach of the schema, so a release of the document
// finds the schema.
//
// For the retry interval, the registry remembers a document that fails
// to load, as [WithRetryAfter] describes. So a validation that reaches
// the reference loads it again only after the interval has passed. A
// non-nil rec records the error of each such load for a registry that
// requires every $ref to resolve. Such a registry then does not return a
// schema to validate with.
//
// A schema from [File] reaches local files and URLs, and one from [URL]
// reaches only URLs. So a remote schema cannot read the local disk, even
// when a schema from [File] loaded the file first. The resolver refuses
// a document fetched over HTTP or HTTPS that names a file URL. So a
// remote document that a schema from [File] reaches cannot read the
// local disk either. The refusal holds whatever order the compiler
// reaches documents in, and it covers a document that the registry kept
// for a schema from [URL]. The resolver refuses a URL that the [Access]
// of the registry does not allow before it looks for a kept document.
//
// The argument user holds the userinfo of a key from [URL], such as a
// password. That userinfo stays out of every URI that the compiler and
// the validator resolve, so an error that quotes such a URI never holds
// the password. The registry adds user to each fetch of a URL that names
// the scheme and host of the key and does not carry userinfo of its own.
// It keeps the document under that URL with user in it, so a document
// fetched with the password stays apart from one fetched without it.
//
// When ref names a subschema by a fragment, doc is the parsed document
// that the registry loaded for ref, and its URL is base without the
// fragment. A reference to that URL resolves to doc without a second
// load, and a relative $ref in doc resolves against it. The compiler
// passes each URI to a resolver in its RFC 3986 normal form, so the
// registry compares the two in that form. A key that spells the URL
// another way, such as with an upper-case host, then still resolves to
// doc. A nil doc means that ref names a whole document.
//
// For a whole document, the options from [WithCompileOptions] come after
// the registry's own, so they win. For a fragment, they come first, and
// the registry's resolver wins, because only it can serve doc.
func (r *Registry) refOptions(
	ref Ref, base string, user keyUserinfo, doc *jsonschema.Schema, rec *refRecorder, rc *reach,
) []CompileOption {
	if !ref.url && ref.file == "" {
		return r.compileOpts
	}

	allowFile := !ref.url

	// The cache key of ref held this identity already, so the file system
	// has one. A file URL names a file in the file system of ref, and the
	// registry keeps the document apart from one of that URL in another.
	inFS, _ := identify(ref.fsys) //nolint:errcheck // The cache key of ref reported it.

	resolver := jsonschema.RefResolverFunc(func(ctx context.Context, uri string) (*jsonschema.Schema, error) {
		if i := strings.IndexByte(uri, '#'); i >= 0 {
			uri = uri[:i]
		}

		// The scheme decides what a schema may reach before the registry
		// looks for a kept document, so a schema from URL never receives a
		// local file that a schema from File loaded. The registry keeps the
		// document under key, which holds uri and, for a file, the identity
		// of the file system that holds the file.
		var (
			load func() ([]byte, error)
			key  cacheKey
		)

		switch {
		case httpfetch.IsHTTPURL(uri):
			// The access judges the URL as the schema names it, before
			// the registry adds the userinfo of the key.
			err := r.access.checkURL(uri)
			if err != nil {
				return nil, err
			}

			uri = user.apply(uri)
			key = cacheKey{uri: uri}
			load = func() ([]byte, error) {
				return r.fetch(ctx, uri)
			}

		case allowFile && isFileURL(uri):
			path, ok := fileURLPath(uri)
			if !ok {
				return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
			}

			key = cacheKey{fs: inFS, uri: uri}
			load = func() ([]byte, error) {
				return r.readFile(ref.fsys, path)
			}

		default:
			return nil, fmt.Errorf("%w: %q", jsonschema.ErrNotResolved, httpfetch.Redacted(uri))
		}

		doc, err := r.refDocument(ctx, key, uri, load, rc)
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

	// A registry that requires every $ref to resolve records each error
	// the resolver returns while the schema compiles.
	var recorded jsonschema.RefResolver = resolver

	if rec != nil {
		recorded = rec.resolver(resolver)
	}

	refOpts := []jsonschema.ValidateOption{
		jsonschema.WithBaseURI(base),
		jsonschema.WithRefResolver(recorded),
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
			jsonschema.WithRefResolver(jsonschema.ChainResolvers(preload, recorded)),
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

// A cacheKey names an entry that the registry keeps: a schema, or a
// document that a $ref names. The registry keeps a file in a file system
// apart from the file of the same URL in another file system. So the key
// holds the identity of the file system beside the URI. A key keeps the
// file system that it names alive, as [fsID] describes, for as long as
// the registry keeps an entry or runs a load under the key.
//
//nolint:unused // The fields tell the keys of the registry apart.
type cacheKey struct {
	// The file system that uri names a file in, or the zero fsID for a file
	// on disk and for every key that does not name a file.
	fs  fsID
	uri string
}

// keyOf returns the key that the registry caches the schema of ref
// under. Its URI is [Ref.Key], except for a [Ref] that names a file,
// whose URI is the URL that [fileBase] returns for it. For a file in a
// file system, the key holds the identity of the file system too, so one
// path in two file systems is two entries. A Ref that does not name a
// file that the registry can read does not have a key. For such a Ref,
// keyOf returns the error that its read reports.
func keyOf(ref Ref) (cacheKey, error) {
	if ref.file == "" {
		return cacheKey{uri: ref.Key()}, nil
	}

	base, err := fileBase(ref)
	if err != nil {
		return cacheKey{}, err
	}

	inFS, err := identify(ref.fsys)
	if err != nil {
		return cacheKey{}, readError(ref.file, err)
	}

	return cacheKey{fs: inFS, uri: base}, nil
}

// fileBase returns the URL that the registry knows the file of ref by,
// with the fragment of its key. The ref is a [Ref] from [File] or
// [FileFS]. A $ref in the schema resolves against the URL.
func fileBase(ref Ref) (string, error) {
	path, err := filePath(ref)
	if err != nil {
		return "", err
	}

	return fileURL(path) + fileFragment(ref), nil
}

// An fsID distinguishes one file system from every other in the keys of
// the registry. The zero fsID stands for the disk.
//
// The registry knows a file system by its value when Go can compare the
// value. Go can compare an [embed.FS], a pointer, and the file system
// that [os.DirFS] returns. The registry knows a map, such as an
// [fstest.MapFS], by the address of the map, which the key holds as a
// [mapAddr]. Either way, a key keeps its file system alive. While the
// registry keeps an entry or runs a load under the key of a map, another
// map cannot take the address of that map.
//
//nolint:unused // The field tells the keys of the registry apart.
type fsID struct {
	v any
}

// A mapAddr is the address of a file system that is a map. The garbage
// collector follows it as it does any other pointer.
type mapAddr unsafe.Pointer

// identify returns the [fsID] of fsys, or the zero fsID for a nil fsys,
// which stands for the disk. A file system whose value Go cannot compare
// and that is not a map does not have an identity that the registry can
// read. For such a file system, identify returns [fs.ErrInvalid].
func identify(fsys fs.FS) (fsID, error) {
	if fsys == nil {
		return fsID{}, nil
	}

	switch v := reflect.ValueOf(fsys); {
	case v.Comparable():
		return fsID{v: fsys}, nil
	case v.Kind() == reflect.Map:
		return fsID{v: mapAddr(v.UnsafePointer())}, nil
	default:
		return fsID{}, fmt.Errorf(
			"%w: a file system of type %T has no identity to cache a schema by: pass a pointer to it",
			fs.ErrInvalid, fsys,
		)
	}
}

// fileFragment returns the fragment of the key of ref, a [Ref] from
// [File], behind its '#', or "" when the key does not have one.
// [fileURL] escapes a '#' in a file name, so the first one in the key
// opens the fragment that [FileOrURL] or [Directive] put there.
func fileFragment(ref Ref) string {
	i := strings.IndexByte(ref.key, '#')
	if i < 0 {
		return ""
	}

	return ref.key[i:]
}

// A cacheEntry is what the registry keeps for a cache key. It holds the
// compiled schema, or the failure of the last attempt to load and
// compile one, with the reach of that attempt. The registry keeps a
// schema until [WithCacheLimit] or a release such as
// [Registry.Invalidate] drops it. It answers with a failure until the
// retry interval has passed since the attempt, so the requests inside
// the interval do not load anything. After the interval, it drops the
// failure.
type cacheEntry struct {
	schema *Schema
	failed *failure
	reach  *reach
}

// A failure is why an attempt to load and compile a schema did not
// produce one. Two Refs that share a cache key can name one file by two
// paths, and each request reads its own Ref in the message. So a failure
// does not hold a name.
type failure struct {
	// The sentinel that the error of a request wraps, [ErrLoad] or
	// [ErrCompile].
	kind error
	// The error behind kind.
	cause error
}

// answer returns what a request for ref gets from e, the entry for its
// cache key. It returns the schema of the entry, or a [*LoadError] that
// names ref and holds the sentinel and the cause of the failure of the
// entry.
func (r *Registry) answer(ref Ref, e cacheEntry) (*Schema, error) {
	if e.failed != nil {
		return nil, &LoadError{Ref: ref, Phase: e.failed.kind, Err: e.failed.cause}
	}

	return e.schema, nil
}

// A flightKey names a shared load by the cache key that it loads and the
// generation of the cache that it started in, as [cache] describes.
//
//nolint:unused // The fields tell the loads of the registry apart.
type flightKey struct {
	key cacheKey
	gen uint64
}

// A flight carries the result of a shared load to every caller that
// joined it. It holds the schema that the load compiled and whether the
// context of the caller that started the load ended before the load
// failed.
type flight struct {
	schema       *Schema
	starterEnded bool
}

// compile loads and compiles the schema that ref names, keeps the
// outcome under key, and returns the outcome. The key is the one that
// [keyOf] returns for ref. When an earlier flight left an entry under
// key that the cache still answers with, compile answers with that
// entry. The group runs one compile per key and generation at a time,
// and each compile checks the cache first. So every caller sees one
// schema per key while the cache keeps it.
//
// The compile started in gen, a generation of the cache. When a release
// has started a new generation since then, compile does not keep
// anything, and the callers that share the compile get what it compiled.
//
// A failure goes into the cache with the time that it happened, for the
// requests of the retry interval. A failure under a context that ended
// does not say anything about the schema, so compile does not keep that
// failure, and the next request tries again.
func (r *Registry) compile(ctx context.Context, ref Ref, key cacheKey, gen uint64) (*Schema, error) {
	if e, _, ok := r.cache.schema(key); ok {
		return r.answer(ref, e)
	}

	rc := newReach(key)
	compiled, failed := r.attempt(ctx, ref, rc)
	entry := cacheEntry{schema: compiled, failed: failed}

	var failedAt time.Time

	if failed != nil {
		if ctx.Err() != nil {
			return r.answer(ref, entry)
		}

		failedAt = time.Now()
	}

	return r.answer(ref, r.cache.keepSchema(key, entry, failedAt, gen, rc))
}

// attempt loads and compiles the schema that ref names once, and returns
// the schema or the [failure] of the attempt. It records in rc each
// document that a $ref of the schema reaches.
//
// In a registry that requires every $ref to resolve, a schema whose
// $refs name documents that failed to load fails with the errors of
// those loads behind [ErrLoad].
func (r *Registry) attempt(ctx context.Context, ref Ref, rc *reach) (*Schema, *failure) {
	data, err := r.load(ctx, ref)
	if err != nil {
		return nil, &failure{kind: ErrLoad, cause: err}
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
			return nil, &failure{kind: ErrLoad, cause: err}
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
		doc, err = parseDocument(ctx, data, r.loadLimit)
		if err != nil {
			return nil, &failure{kind: ErrCompile, cause: err}
		}

		wrapper := map[string]string{"$ref": base}
		if doc.Schema != "" {
			wrapper["$schema"] = doc.Schema
		}

		data, err = json.Marshal(wrapper)
		if err != nil {
			return nil, &failure{kind: ErrCompile, cause: err}
		}
	}

	// The recorder tells the registry which documents its resolver could
	// not load for the compile. A registry that lets a schema compile
	// without them needs none.
	var rec *refRecorder

	if newCompileConfig(r.compileOpts).requireRefs {
		rec = &refRecorder{}
	}

	compiled, err := compileDocument(ctx, data, r.loadLimit, r.refOptions(ref, base, user, doc, rec, rc))
	if err != nil {
		return nil, &failure{kind: ErrCompile, cause: err}
	}

	if rec == nil {
		return compiled, nil
	}

	unresolved := rec.stop()

	switch {
	case unresolved == nil:
		return compiled, nil

	case ctx.Err() != nil:
		// A document that did not load because the context ended says
		// nothing about the document, so the failure names the context's
		// error in place of the load's.
		return nil, &failure{kind: ErrLoad, cause: ctx.Err()}

	default:
		return nil, &failure{kind: ErrLoad, cause: fmt.Errorf("cannot resolve $ref: %w", unresolved)}
	}
}

// A keyUserinfo is the userinfo of a URL key, with the scheme and host
// that it belongs to. The zero keyUserinfo does not hold any userinfo.
type keyUserinfo struct {
	user   *url.Userinfo
	scheme string
	host   string
}

// splitUserinfo returns rawURL without its userinfo, and that userinfo.
// A URL that does not carry userinfo, or does not parse, comes back
// unchanged with the zero keyUserinfo.
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
// host of k and does not carry userinfo of its own. Otherwise it returns
// uri unchanged.
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

// canonicalURL returns rawURL without its fragment, in the normal form of
// RFC 3986 section 6.2.2. The compiler passes each URI to a resolver in
// that form. The form spells the scheme and host in lower case. It
// decodes each percent-encoded octet that encodes an unreserved character
// and spells the rest in upper-case hex. The path in that form does not
// hold dot segments. A URL that does not parse comes back without its
// fragment and otherwise as written.
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

// canonicalEscapes returns s with each percent-encoded octet decoded
// where it encodes an RFC 3986 unreserved character and spelled in
// upper-case hex otherwise. A malformed escape stays unchanged.
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
