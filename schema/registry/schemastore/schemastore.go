package schemastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

// Default SchemaStore URLs and timeouts.
const (
	defaultCatalogURL     = "https://www.schemastore.org/api/json/catalog.json"
	defaultCacheTTL       = 1 * time.Hour
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = 1 * time.Minute
)

var (
	// ErrFetchCatalog indicates the SchemaStore catalog could not be fetched
	// and no earlier fetch succeeded, so there is no catalog to match
	// against.
	ErrFetchCatalog = errors.New("fetch schema catalog")

	// ErrNoCatalogMatch indicates no catalog entry matches the document's file
	// path. It wraps [schema.ErrNoMatch], so a
	// [go.jacobcolvin.com/niceyaml/schema/registry.Registry] moves on to the
	// next resolver.
	ErrNoCatalogMatch = fmt.Errorf("%w: no catalog entry matches", schema.ErrNoMatch)
)

// Catalog represents the SchemaStore.org catalog structure returned by
// the catalog API.
type Catalog struct {
	Schemas []CatalogEntry `json:"schemas"`
}

// CatalogEntry represents a single schema entry in the catalog, containing
// metadata and file matching patterns.
type CatalogEntry struct {
	// Name is the display name of the schema.
	Name string `json:"name"`
	// Description provides details about the schema's purpose.
	Description string `json:"description"`
	// URL is the HTTP URL to fetch the schema from.
	URL string `json:"url"`
	// FileMatch contains glob patterns for files this schema applies to.
	FileMatch []string `json:"fileMatch"`
}

// SchemaStore matches documents to SchemaStore.org catalog entries.
//
// The catalog is fetched on the first lookup and cached for the configured
// TTL. Once the cache expires, the next lookup refreshes it; a refresh that
// fails leaves the previous catalog in use, and a fetch that fails before
// any catalog has loaded reports [ErrFetchCatalog]. After a failed fetch the
// store waits the retry interval before contacting the catalog URL again,
// so an unreachable catalog costs one timeout per interval rather than one
// per lookup.
//
// Concurrent lookups share a single fetch. A lookup that arrives while a
// refresh is running uses the previous catalog without waiting. The lookup
// that starts a fetch, and any lookup with no catalog to fall back on, waits
// for the fetch to finish or for its own context to end. A lookup that stops
// waiting leaves the fetch running, so the fetched catalog still reaches
// later lookups.
//
// SchemaStore implements [schema.Resolver] and can be registered directly
// with a [go.jacobcolvin.com/niceyaml/schema/registry.Registry].
// [ErrFetchCatalog] does not wrap [schema.ErrNoMatch], so while no catalog
// has loaded, the registry stops at the store and does not try the resolvers
// registered after it. Register the store after any resolver that should
// still apply without the catalog. Create instances with [New].
//
// Example:
//
//	reg.Register(schemastore.New())
type SchemaStore struct {
	lastFetch      time.Time // Last successful fetch; zero until the first one succeeds.
	lastAttempt    time.Time // Last fetch, successful or not.
	client         *http.Client
	filter         func(CatalogEntry) bool
	inflight       *fetchCall // Fetch in progress, or nil when none is running.
	lastErr        error      // Error from the last fetch, or nil when it succeeded.
	catalogURL     string
	entries        []CatalogEntry
	cacheTTL       time.Duration
	refreshTimeout time.Duration
	retryAfter     time.Duration
	mu             sync.Mutex // Guards entries, inflight, lastFetch, lastAttempt, and lastErr.
}

// fetchCall is a catalog fetch in progress. The fetch sets entries and err
// before it closes done.
type fetchCall struct {
	done    chan struct{}
	err     error
	entries []CatalogEntry
}

// Option configures [SchemaStore] creation.
//
// Available options:
//   - [WithCatalogURL]
//   - [WithHTTPClient]
//   - [WithCacheTTL]
//   - [WithRefreshTimeout]
//   - [WithRetryAfter]
//   - [WithFilter]
type Option func(*SchemaStore)

// WithCatalogURL is an [Option] that sets a custom catalog URL.
//
// Defaults to "https://www.schemastore.org/api/json/catalog.json".
func WithCatalogURL(url string) Option {
	return func(s *SchemaStore) {
		s.catalogURL = url
	}
}

// WithHTTPClient is an [Option] that sets a custom HTTP client for fetching
// the catalog and schemas.
func WithHTTPClient(client *http.Client) Option {
	return func(s *SchemaStore) {
		s.client = client
	}
}

// WithCacheTTL is an [Option] that sets the cache time-to-live for the catalog.
//
// Defaults to 1 hour. Set to 0 to disable caching (fetch on every lookup).
func WithCacheTTL(ttl time.Duration) Option {
	return func(s *SchemaStore) {
		s.cacheTTL = ttl
	}
}

// WithRefreshTimeout is an [Option] that sets the timeout for each catalog
// fetch, the first one included.
//
// A lookup that finds the cache expired fetches the catalog under this
// timeout. If the fetch fails or times out, the lookup uses the previous
// catalog when one exists, so a temporarily unreachable SchemaStore.org
// degrades to stale matches rather than errors. The fetch does not inherit
// the cancellation or deadline of the lookup that started it, so this
// timeout alone bounds how long it runs.
//
// Defaults to 10 seconds.
func WithRefreshTimeout(timeout time.Duration) Option {
	return func(s *SchemaStore) {
		s.refreshTimeout = timeout
	}
}

// WithRetryAfter is an [Option] that sets how long the store waits after a
// failed catalog fetch before trying again.
//
// Until the interval passes, lookups use the previous catalog when one
// exists and report [ErrFetchCatalog] with the last fetch error otherwise,
// without contacting the catalog URL.
//
// Defaults to 1 minute.
func WithRetryAfter(interval time.Duration) Option {
	return func(s *SchemaStore) {
		s.retryAfter = interval
	}
}

// WithFilter is an [Option] that sets a filter function for catalog entries.
//
// Only entries for which the filter returns true will be considered for
// matching. This can be used to limit the schemas to a specific subset.
// The filter is called during catalog refresh; avoid expensive or stateful
// operations.
//
// Example:
//
//	// Only match GitHub-related schemas
//	store := schemastore.New(schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	    return strings.Contains(strings.ToLower(e.Name), "github")
//	}))
func WithFilter(fn func(CatalogEntry) bool) Option {
	return func(s *SchemaStore) {
		s.filter = fn
	}
}

// New creates a new [*SchemaStore].
//
// New performs no I/O; the catalog is fetched on the first lookup and
// cached for the configured TTL. Configure with options to customize
// behavior:
//
//	store := schemastore.New(
//	    schemastore.WithCacheTTL(1 * time.Hour),
//	    schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	        return strings.Contains(e.Name, "GitHub")
//	    }),
//	)
func New(opts ...Option) *SchemaStore {
	store := &SchemaStore{
		catalogURL:     defaultCatalogURL,
		client:         http.DefaultClient,
		cacheTTL:       defaultCacheTTL,
		refreshTimeout: defaultRefreshTimeout,
		retryAfter:     defaultRetryAfter,
	}
	for _, opt := range opts {
		opt(store)
	}

	return store
}

// Resolve names the schema for the catalog entry matching the document's
// file path. The returned [schema.Ref] fetches the schema from the entry's
// URL when loaded. A document that matches no entry reports
// [ErrNoCatalogMatch]; a catalog that has never loaded reports
// [ErrFetchCatalog].
//
// Implements [schema.Resolver].
func (s *SchemaStore) Resolve(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
	entry, err := s.FindMatch(ctx, doc.FilePath())
	if err != nil {
		return schema.Ref{}, err
	}

	//nolint:wrapcheck // The URL loader already wraps errors with context.
	return loader.URL(entry.URL, loader.WithHTTPClient(s.client)).Resolve(ctx, doc)
}

// FindMatch finds the catalog entry matching a file path.
//
// Returns [ErrNoCatalogMatch] when no entry matches, which includes an
// empty file path, and [ErrFetchCatalog] when no catalog has loaded, either
// because the fetch failed or because ctx ended before it finished.
func (s *SchemaStore) FindMatch(ctx context.Context, filePath string) (CatalogEntry, error) {
	if filePath == "" {
		return CatalogEntry{}, fmt.Errorf("%w: document has no file path", ErrNoCatalogMatch)
	}

	entries, err := s.catalog(ctx)
	if err != nil {
		return CatalogEntry{}, err
	}

	for _, entry := range entries {
		// Match against both the full path and the base name, since SchemaStore
		// patterns may or may not include directory components.
		if filepaths.MatchAnyWithBase(filePath, entry.FileMatch) {
			return entry, nil
		}
	}

	return CatalogEntry{}, fmt.Errorf("%w: %q", ErrNoCatalogMatch, filePath)
}

// catalog returns the catalog entries, fetching or refreshing them first
// when the cache is empty or expired.
//
// A lookup that waits for a fetch stops waiting when ctx ends. When the
// fetch fails or the wait ends early, the lookup falls back to the previous
// entries, or reports ErrFetchCatalog when none exist.
func (s *SchemaStore) catalog(ctx context.Context) ([]CatalogEntry, error) {
	call, entries, err := s.join(ctx)
	if call == nil {
		return entries, err
	}

	select {
	case <-call.done:
		if call.err != nil {
			return s.stale(call.err)
		}

		return call.entries, nil

	case <-ctx.Done():
		return s.stale(ctx.Err())
	}
}

// join decides under the lock whether a lookup can answer without waiting
// for a fetch. It returns the entries or error to report when the lookup
// can, and otherwise the fetch to wait on, starting one when none is
// running.
func (s *SchemaStore) join(ctx context.Context) (*fetchCall, []CatalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := !s.lastFetch.IsZero()

	switch {
	case loaded && s.cacheTTL > 0 && time.Since(s.lastFetch) < s.cacheTTL:
		return nil, s.entries, nil

	case s.lastErr != nil && time.Since(s.lastAttempt) < s.retryAfter:
		// The last fetch failed within the retry interval, so leave the
		// catalog URL alone.
		entries, err := s.staleLocked(s.lastErr)

		return nil, entries, err

	case ctx.Err() != nil:
		// A lookup whose context has ended starts no fetch.
		entries, err := s.staleLocked(ctx.Err())

		return nil, entries, err

	case loaded && s.inflight != nil:
		// Another lookup is refreshing the catalog, so use the previous one
		// rather than wait for the request.
		return nil, s.entries, nil
	}

	if s.inflight == nil {
		s.inflight = &fetchCall{done: make(chan struct{})}

		go s.refresh(ctx, s.inflight)
	}

	return s.inflight, nil, nil
}

// refresh fetches the catalog for call, records the outcome, and closes
// call.done. The fetch drops the cancellation and deadline of ctx, which
// belongs to the lookup that started it, so a lookup that stops waiting
// does not cancel a fetch that other lookups share; the refresh timeout
// bounds it instead.
func (s *SchemaStore) refresh(ctx context.Context, call *fetchCall) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.refreshTimeout)
	defer cancel()

	entries, err := s.fetch(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.inflight = nil
	s.lastAttempt = time.Now()
	s.lastErr = err

	if err == nil {
		s.entries = entries
		s.lastFetch = s.lastAttempt
	}

	call.entries, call.err = entries, err
	close(call.done)
}

// stale returns the previous entries when a fetch has ever succeeded and
// otherwise wraps cause in ErrFetchCatalog.
func (s *SchemaStore) stale(cause error) ([]CatalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.staleLocked(cause)
}

// staleLocked is stale for a caller that already holds mu.
func (s *SchemaStore) staleLocked(cause error) ([]CatalogEntry, error) {
	if s.lastFetch.IsZero() {
		return nil, fmt.Errorf("%w: %w", ErrFetchCatalog, cause)
	}

	return s.entries, nil
}

// fetch retrieves the catalog from the configured URL and returns the
// filtered entries. It holds no lock, so a slow catalog server blocks only
// the lookups waiting on this fetch.
//
// The HTTP GET and its size limit are the ones [loader.URL] uses; only the
// catalog JSON parsing is specific to SchemaStore.
func (s *SchemaStore) fetch(ctx context.Context) ([]CatalogEntry, error) {
	data, err := httpfetch.Get(ctx, s.client, s.catalogURL)
	if err != nil {
		//nolint:wrapcheck // The error already names the catalog URL.
		return nil, err
	}

	var catalog Catalog

	err = json.Unmarshal(data, &catalog)
	if err != nil {
		return nil, fmt.Errorf("parse catalog from %s: %w", s.catalogURL, err)
	}

	// Prefilter entries: only keep entries with YAML patterns that pass the filter.
	return s.filterAndNormalizeEntries(catalog.Schemas), nil
}

// filterAndNormalizeEntries filters catalog entries to only those with
// supported patterns that pass the configured filter. It also normalizes each
// entry's FileMatch to contain only supported patterns (YAML and JSON files).
func (s *SchemaStore) filterAndNormalizeEntries(schemas []CatalogEntry) []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(schemas))

	for _, entry := range schemas {
		// Skip entries without a URL.
		if entry.URL == "" {
			continue
		}

		// Skip entries without file match patterns.
		if len(entry.FileMatch) == 0 {
			continue
		}

		// Apply user filter if configured.
		if s.filter != nil && !s.filter(entry) {
			continue
		}

		// Only consider YAML and JSON patterns.
		supportedPatterns := filterSupportedPatterns(entry.FileMatch)
		if len(supportedPatterns) == 0 {
			continue
		}

		// Store entry with only supported patterns.
		entry.FileMatch = supportedPatterns
		entries = append(entries, entry)
	}

	return entries
}

// filterSupportedPatterns returns patterns that match YAML-compatible files.
// This includes .yaml, .yml, and .json extensions since JSON is a valid
// subset of YAML.
func filterSupportedPatterns(patterns []string) []string {
	var result []string

	for _, pattern := range patterns {
		lower := strings.ToLower(pattern)
		if strings.HasSuffix(lower, ".yaml") ||
			strings.HasSuffix(lower, ".yml") ||
			strings.HasSuffix(lower, ".json") {
			result = append(result, pattern)
		}
	}

	return result
}
