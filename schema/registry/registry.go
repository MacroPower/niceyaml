package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

var (
	// ErrNoMatch indicates no matcher matched the document.
	ErrNoMatch = errors.New("no matching schema")

	// ErrLoad indicates the schema could not be loaded.
	ErrLoad = errors.New("load schema")

	// ErrCompile indicates schema compilation failed.
	ErrCompile = errors.New("compile schema")
)

// Registry maps YAML documents to schemas using pluggable resolvers.
//
// Registrations are evaluated in order; the first [Resolver] that does not
// report [ErrNoMatch] wins. Compiled validators are cached by schema URL to
// avoid recompilation.
//
// Example:
//
//	reg := registry.New()
//
//	// Directive matching first (i.e. explicit user intent).
//	reg.Register(registry.Directive())
//
//	// Content-based matching.
//	kindPath := paths.Root().Child("kind").Path()
//	reg.RegisterFunc(
//	    matcher.Content(kindPath, "Deployment"),
//	    loader.Embedded("deployment.json", deploymentSchema),
//	)
//
// Create instances with [New].
type Registry struct {
	cache         map[string]niceyaml.SchemaValidator // compiled validators by schema URL
	resolvers     []Resolver
	validatorOpts []jsonschema.ValidateOption
	mu            sync.RWMutex
}

// Option configures [Registry] creation.
//
// Available options:
//   - [WithValidateOptions]
type Option func(*Registry)

// WithValidateOptions is an [Option] that sets options passed to
// [jsonschema.CompileJSON] when compiling validators.
func WithValidateOptions(opts ...jsonschema.ValidateOption) Option {
	return func(r *Registry) {
		r.validatorOpts = opts
	}
}

// New creates a new [*Registry].
func New(opts ...Option) *Registry {
	r := &Registry{
		cache: make(map[string]niceyaml.SchemaValidator),
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Register adds a [Resolver] to the registry.
//
// Registrations are evaluated in order; the first resolver that does not
// report [ErrNoMatch] wins.
//
// For a separate [matcher.Matcher] and [loader.Loader], use [RegisterFunc].
func (r *Registry) Register(res Resolver) {
	r.resolvers = append(r.resolvers, res)
}

// RegisterFunc adds a [matcher.Matcher] and [loader.Loader] pair to the
// registry as one [Resolver] that loads through l when m matches.
//
// This suits matchers and loaders that share no state. A resolver that
// decides and loads from the same parse implements [Resolver] directly and
// uses [Register].
//
// Registrations are evaluated in order; first match wins.
func (r *Registry) RegisterFunc(m matcher.Matcher, l loader.Loader) {
	r.Register(&pair{matcher: m, loader: l})
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if no resolver matches the document. Returns other
// errors if schema loading or compilation fails.
//
// For most use cases, prefer [ValidateDocument] which combines lookup and
// validation. Use Lookup when you need the validator for custom processing.
func (r *Registry) Lookup(ctx context.Context, doc *niceyaml.DocumentDecoder) (niceyaml.SchemaValidator, error) {
	for _, res := range r.resolvers {
		result, err := res.Resolve(ctx, doc)
		if errors.Is(err, ErrNoMatch) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrLoad, err)
		}

		return r.compileValidator(ctx, result)
	}

	return nil, fmt.Errorf("%w: %q", ErrNoMatch, doc.FilePath())
}

// ValidateDocument validates a document using the first matching schema.
//
// This is the primary entry point for schema validation. It combines schema
// lookup and validation into a single call. Use [Lookup] when you need the
// validator for custom processing.
//
// Returns [ErrNoMatch] if no matcher matches the document. Callers can check
// for this error to allow unmatched documents:
//
//	err := reg.ValidateDocument(ctx, doc)
//	if err != nil && !errors.Is(err, registry.ErrNoMatch) {
//	    return err
//	}
//
// Returns validation errors if the document doesn't conform to the schema.
// Returns loading/compilation errors if schema preparation fails.
func (r *Registry) ValidateDocument(ctx context.Context, doc *niceyaml.DocumentDecoder) error {
	v, err := r.Lookup(ctx, doc)
	if err != nil {
		return err
	}

	//nolint:wrapcheck // Validation errors should be returned directly.
	return doc.ValidateSchema(ctx, v)
}

// compileValidator compiles a validator for result, using cache when possible.
//
// Under concurrent load, multiple goroutines may compile the same schema before
// one caches it. This is intentional to avoid lock contention; the overhead of
// occasional duplicate compilation is acceptable.
func (r *Registry) compileValidator(ctx context.Context, result loader.Result) (niceyaml.SchemaValidator, error) {
	// Check cache.
	r.mu.RLock()

	v, ok := r.cache[result.URL]
	r.mu.RUnlock()

	if ok {
		return v, nil
	}

	compiled, err := jsonschema.CompileJSON(ctx, result.Data, r.validatorOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrCompile, result.URL, err)
	}

	v = schema.NewValidator(compiled)

	// Cache validator by URL. Skip caching for empty URLs to avoid cache
	// collisions where different schemas would share a single cache entry.
	if result.URL != "" {
		r.mu.Lock()

		r.cache[result.URL] = v
		r.mu.Unlock()
	}

	return v, nil
}
