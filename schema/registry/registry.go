package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/schema/validator"
)

var (
	// ErrNoMatch indicates no matcher matched the document.
	ErrNoMatch = errors.New("no matching schema")

	// ErrLoad indicates the schema could not be loaded.
	ErrLoad = errors.New("load schema")

	// ErrCompile indicates schema compilation failed.
	ErrCompile = errors.New("compile schema")
)

// Cache stores and retrieves compiled validators by URL.
//
// The default implementation is an unbounded thread-safe map. Provide a custom
// implementation via [WithCache] for alternative caching strategies (LRU, TTL,
// external cache, etc.).
type Cache interface {
	Get(url string) (niceyaml.SchemaValidator, bool)
	Set(url string, v niceyaml.SchemaValidator)
}

// mapCache is the default [Cache] implementation using an unbounded map.
type mapCache struct {
	cache map[string]niceyaml.SchemaValidator
	mu    sync.RWMutex
}

func newMapCache() *mapCache {
	return &mapCache{cache: make(map[string]niceyaml.SchemaValidator)}
}

// Get retrieves a validator from the cache.
func (c *mapCache) Get(url string) (niceyaml.SchemaValidator, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.cache[url]

	return v, ok
}

// Set stores a validator in the cache.
func (c *mapCache) Set(url string, v niceyaml.SchemaValidator) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[url] = v
}

// Registry maps YAML documents to schemas using pluggable matchers.
//
// Registrations are evaluated in order; first match wins. Compiled validators
// are cached by schema URL to avoid recompilation.
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
	cache         Cache
	matchLoaders  []MatchLoader
	validatorOpts []validator.Option
}

// Option configures [Registry] creation.
//
// Available options:
//   - [WithCache]
//   - [WithValidatorOptions]
type Option func(*Registry)

// WithCache is an [Option] that sets a custom [Cache] implementation.
//
// By default, the registry uses an unbounded thread-safe map cache. Provide a
// custom implementation for alternative caching strategies (LRU, TTL, external
// cache, etc.).
func WithCache(c Cache) Option {
	return func(r *Registry) {
		r.cache = c
	}
}

// WithValidatorOptions is an [Option] that sets options passed to
// [validator.New] when compiling validators.
func WithValidatorOptions(opts ...validator.Option) Option {
	return func(r *Registry) {
		r.validatorOpts = opts
	}
}

// New creates a new [*Registry].
func New(opts ...Option) *Registry {
	r := &Registry{
		cache: newMapCache(),
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Register adds a [MatchLoader] to the registry.
//
// Registrations are evaluated in order; first match wins.
//
// For stateless [matcher.Matcher] and [loader.Loader] implementations, use
// [RegisterFunc].
func (r *Registry) Register(ml MatchLoader) {
	r.matchLoaders = append(r.matchLoaders, ml)
}

// RegisterFunc adds a [matcher.Matcher] and [loader.Loader] pair to the
// registry.
//
// This is a convenience method for registering separate Matcher and Loader
// implementations. The matcher and loader do not share state; for stateful
// implementations, implement [MatchLoader] directly and use [Register].
//
// Registrations are evaluated in order; first match wins.
func (r *Registry) RegisterFunc(m matcher.Matcher, l loader.Loader) {
	r.Register(&matchLoaderWrapper{matcher: m, loader: l})
}

// Lookup finds the validator for a document.
//
// Returns [ErrNoMatch] if no matcher matches the document. Returns other
// errors if schema loading or compilation fails.
//
// For most use cases, prefer [ValidateDocument] which combines lookup and
// validation. Use Lookup when you need the validator for custom processing.
func (r *Registry) Lookup(ctx context.Context, doc *niceyaml.DocumentDecoder) (niceyaml.SchemaValidator, error) {
	for _, ml := range r.matchLoaders {
		if !ml.Match(ctx, doc) {
			continue
		}

		return r.loadValidator(ctx, doc, ml)
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
	return doc.ValidateSchemaContext(ctx, v)
}

// loadValidator loads and compiles a validator, using cache when possible.
//
// Under concurrent load, multiple goroutines may compile the same schema before
// one caches it. This is intentional to avoid lock contention; the overhead of
// occasional duplicate compilation is acceptable.
func (r *Registry) loadValidator(
	ctx context.Context,
	doc *niceyaml.DocumentDecoder,
	ml MatchLoader,
) (niceyaml.SchemaValidator, error) {
	// Load schema.
	result, err := ml.Load(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoad, err)
	}

	// Check cache.
	if v, ok := r.cache.Get(result.URL); ok {
		return v, nil
	}

	// Get or compile validator.
	var v niceyaml.SchemaValidator
	if result.Validator != nil {
		// Pre-compiled validator provided.
		v = result.Validator
	} else {
		// Compile validator from data.
		var compileErr error

		v, compileErr = validator.New(result.URL, result.Data, r.validatorOpts...)
		if compileErr != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrCompile, result.URL, compileErr)
		}
	}

	// Cache validator by URL. Skip caching for empty URLs to avoid cache
	// collisions where different schemas would share a single cache entry.
	if result.URL != "" {
		r.cache.Set(result.URL, v)
	}

	return v, nil
}
