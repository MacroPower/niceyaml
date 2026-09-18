package schema

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sync/singleflight"

	"go.jacobcolvin.com/niceyaml"
)

var (
	// ErrResolve indicates a resolver applied to the document but could not
	// name its schema.
	ErrResolve = errors.New("resolve schema")

	// ErrNoKey indicates a resolver returned a [Ref] with neither a
	// Validator nor a Key, which leaves the registry nothing to cache the
	// schema under.
	ErrNoKey = errors.New("schema ref has no key")

	// ErrNoLoad indicates a resolver returned a [Ref] with neither a
	// Validator nor a Load function, which leaves the registry no way to
	// read the schema.
	ErrNoLoad = errors.New("schema ref has no Load function")

	// ErrLoad indicates the schema could not be loaded.
	ErrLoad = errors.New("load schema")
)

// Registry maps YAML documents to schemas using pluggable resolvers.
//
// Lookup tries registrations in order; the first [Resolver] that
// does not report [ErrNoMatch] wins. The registry caches compiled
// validators by [Ref.Key] and consults that cache before loading, so it
// loads and compiles each schema once however many documents name it. A
// Ref that carries a [*Validator] is used as it is.
//
// Example:
//
//	reg := New()
//
//	// Directive matching first (i.e. explicit user intent).
//	reg.Register(schema.Directive())
//
//	// Content-based matching.
//	kindPath := paths.Root().Child("kind")
//	reg.Register(schema.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    schema.Embedded(deploymentSchema),
//	))
//
// Create instances with [NewRegistry].
type Registry struct {
	group       singleflight.Group    // one load and compile in flight per Key
	cache       map[string]*Validator // compiled validators by Ref.Key
	resolvers   []Resolver
	compileOpts []CompileOption
	mu          sync.RWMutex
	// Makes Validate report ErrNoMatch when no resolver applies.
	requireSchema bool
}

// RegistryOption configures [Registry] creation.
//
// Available options:
//   - [WithCompileOptions]
//   - [WithRequireSchema]
type RegistryOption func(*Registry)

// WithRequireSchema is a [RegistryOption] that sets whether
// [Registry.Validate] reports a document no resolver applies to. The
// default is true, and such a document then fails with [ErrNoMatch]. With
// false, Validate accepts it, so a registry that validates what it
// recognizes and passes the rest runs inside a decode through
// [niceyaml.WithValidator]:
//
//	reg := schema.NewRegistry(schema.WithRequireSchema(false))
//	reg.Register(schema.Directive(), schemastore.New())
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
// They apply when a schema is compiled, which happens once per [Ref.Key],
// so an option such as a format validator takes effect for every document
// validated against that schema. A [*Validator] a Ref carries was compiled
// elsewhere, so they do not reach it:
//
//	reg := schema.NewRegistry(schema.WithCompileOptions(
//	    schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
//	))
//
// The registry keeps its own copy of opts, so writing to the caller's slice
// afterwards changes nothing.
func WithCompileOptions(opts ...CompileOption) RegistryOption {
	return func(r *Registry) {
		r.compileOpts = slices.Clone(opts)
	}
}

// NewRegistry creates a new [*Registry].
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		cache:         make(map[string]*Validator),
		requireSchema: true,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Register appends resolvers to the end of the lookup order.
//
// Registrations are evaluated in order; the first resolver that does not
// report [ErrNoMatch] wins. Register is safe to call concurrently
// with [Lookup] and [Validate]; a lookup already in progress keeps
// the resolver list it started with.
func (r *Registry) Register(res ...Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resolvers = append(r.resolvers, res...)
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if no resolver applies to the document,
// [ErrResolve] if a resolver applied but could not name the schema, and
// [ErrLoad] or [ErrCompile] if loading or compiling the schema fails. When
// ctx ends before the schema loads, Lookup returns [ErrLoad] wrapping the
// context's error without waiting for the load to finish.
//
// A document holding nothing but comments and %YAML or %TAG directives,
// which the parser splits off from the content below the next "---", is
// [ErrNoMatch] before any resolver runs. An explicitly empty document
// counts as content, since it is the null document a schema may validate.
//
// Every error comes back bound to the document through
// [niceyaml.Document.Bind], so its message names the file the document
// came from.
//
// For most use cases, prefer [Validate] which combines lookup and
// validation. Use Lookup when you need the validator for custom processing.
func (r *Registry) Lookup(ctx context.Context, doc *niceyaml.Document) (*Validator, error) {
	v, err := r.lookup(ctx, doc)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return nil, doc.Bind(err)
	}

	return v, nil
}

// lookup is [Registry.Lookup] before binding the error to the document.
func (r *Registry) lookup(ctx context.Context, doc *niceyaml.Document) (*Validator, error) {
	// No resolver sees a content-free document, so a resolver registered
	// after one that declines cannot resurrect it.
	if !doc.HasContent() {
		return nil, fmt.Errorf("%w: document has no content", ErrNoMatch)
	}

	r.mu.RLock()

	resolvers := r.resolvers
	r.mu.RUnlock()

	for _, res := range resolvers {
		ref, err := res.Resolve(ctx, doc)
		if errors.Is(err, ErrNoMatch) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrResolve, err)
		}

		return r.validator(ctx, ref)
	}

	return nil, ErrNoMatch
}

// Validate validates a document using the first matching schema.
//
// This is the primary entry point for schema validation. It combines schema
// lookup and validation into a single call. Use [Lookup] when you need the
// validator for custom processing. Validate implements
// [niceyaml.Validator], so [niceyaml.WithValidator] runs it before a
// decode:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// Returns [ErrNoMatch] if no resolver applies to the document, unless
// [WithRequireSchema] set false, in which case such a document passes.
// Callers of a registry that requires a schema can check for the error to
// allow unmatched documents at one call site:
//
//	err := reg.Validate(ctx, doc)
//	if err != nil && !errors.Is(err, ErrNoMatch) {
//	    return err
//	}
//
// Returns validation errors if the document doesn't conform to the schema.
// Returns resolution, loading, or compilation errors if schema preparation
// fails.
func (r *Registry) Validate(ctx context.Context, doc *niceyaml.Document) error {
	v, err := r.Lookup(ctx, doc)
	if err != nil {
		if !r.requireSchema && errors.Is(err, ErrNoMatch) {
			return nil
		}

		return err
	}

	//nolint:wrapcheck // Validation errors should be returned directly.
	return doc.Validate(ctx, v)
}

// validator returns the validator for ref: the one it carries, or the
// compiled schema, loaded and compiled on the first request for its Key and
// served from the cache after that.
//
// Concurrent requests for one Key share a single load and compile through
// the singleflight group, and each caller waits for it only while its own
// context is live. The shared load runs under the context of the caller
// that started it and reports whether that context had ended when the load
// failed. A caller that joined with a live context loads again only in that
// case. Any other failure reaches every caller that shared the load,
// including a timeout inside the load whose error wraps a context error.
func (r *Registry) validator(ctx context.Context, ref Ref) (*Validator, error) {
	if ref.Validator != nil {
		return ref.Validator, nil
	}

	if ref.Key == "" {
		return nil, fmt.Errorf("%w: %w", ErrResolve, ErrNoKey)
	}

	if ref.Load == nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrResolve, ref.Key, ErrNoLoad)
	}

	if v, ok := r.cached(ref.Key); ok {
		return v, nil
	}

	for {
		ch := r.group.DoChan(ref.Key, func() (any, error) {
			err := r.compile(ctx, ref)

			// Report whether this caller's context had ended when the load
			// failed, so a joiner can tell that cancellation apart from a
			// failure of the load itself.
			return err != nil && ctx.Err() != nil, err
		})

		var res singleflight.Result

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %q: %w", ErrLoad, ref.Key, ctx.Err())

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

	v, ok := r.cached(ref.Key)
	if !ok {
		return nil, fmt.Errorf("%w: %q: validator missing after compile", ErrCompile, ref.Key)
	}

	return v, nil
}

// cached returns the validator cached under key, if any.
func (r *Registry) cached(key string) (*Validator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	v, ok := r.cache[key]

	return v, ok
}

// compile loads and compiles the schema ref names and caches the result
// under its Key. A cache entry stored by an earlier call is left in place,
// so every caller sees one validator per Key.
func (r *Registry) compile(ctx context.Context, ref Ref) error {
	if _, ok := r.cached(ref.Key); ok {
		return nil
	}

	data, err := ref.Load(ctx)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrLoad, ref.Key, err)
	}

	compiled, err := Compile(ctx, data, r.compileOpts...)
	if err != nil {
		return fmt.Errorf("%q: %w", ref.Key, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.cache[ref.Key]; !ok {
		r.cache[ref.Key] = compiled
	}

	return nil
}
