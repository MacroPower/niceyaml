package schemastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

// Default SchemaStore URLs and timeouts.
const (
	defaultCatalogURL     = "https://www.schemastore.org/api/json/catalog.json"
	defaultCacheTTL       = 1 * time.Hour
	defaultRefreshTimeout = 10 * time.Second
)

var (
	// ErrFetchCatalog indicates the SchemaStore catalog could not be fetched.
	ErrFetchCatalog = errors.New("fetch schema catalog")

	// ErrNoCatalogMatch indicates no catalog entry matches the document's file path.
	ErrNoCatalogMatch = errors.New("no catalog entry matches")
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

// SchemaStore manages the SchemaStore.org catalog with caching.
//
// The catalog is fetched during construction and cached for the configured TTL.
// SchemaStore implements [registry.MatchLoader] and can be registered directly
// with a [registry.Registry]. Create instances with [New].
//
// Example:
//
//	store, err := schemastore.New(ctx)
//	if err != nil {
//	    log.Printf("schemastore unavailable: %v", err)
//	    return
//	}
//	reg.Register(store)
type SchemaStore struct {
	lastFetch      time.Time
	client         *http.Client
	filter         func(CatalogEntry) bool
	catalogURL     string
	entries        []CatalogEntry
	cacheTTL       time.Duration
	refreshTimeout time.Duration
	mu             sync.RWMutex
}

// Option configures [SchemaStore] creation.
//
// Available options:
//   - [WithCatalogURL]
//   - [WithHTTPClient]
//   - [WithCacheTTL]
//   - [WithRefreshTimeout]
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

// WithRefreshTimeout is an [Option] that sets the timeout for background
// catalog refresh operations.
//
// When [SchemaStore.FindMatch] is called with an expired cache, it attempts
// a background refresh with this timeout. If the refresh fails or times out,
// the stale cached data is used. This provides fault tolerance when
// SchemaStore.org is temporarily unavailable.
//
// Defaults to 10 seconds.
func WithRefreshTimeout(timeout time.Duration) Option {
	return func(s *SchemaStore) {
		s.refreshTimeout = timeout
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
//	store, err := schemastore.New(ctx, schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	    return strings.Contains(strings.ToLower(e.Name), "github")
//	}))
func WithFilter(fn func(CatalogEntry) bool) Option {
	return func(s *SchemaStore) {
		s.filter = fn
	}
}

// New creates a new [*SchemaStore] by fetching the catalog.
//
// The catalog is fetched immediately and cached for the configured TTL.
// Returns an error if the catalog cannot be fetched. Configure with options
// to customize behavior:
//
//	store, err := schemastore.New(ctx,
//	    schemastore.WithCacheTTL(1 * time.Hour),
//	    schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	        return strings.Contains(e.Name, "GitHub")
//	    }),
//	)
//	if err != nil {
//	    log.Printf("schemastore unavailable: %v", err)
//	    return
//	}
func New(ctx context.Context, opts ...Option) (*SchemaStore, error) {
	store := &SchemaStore{
		catalogURL:     defaultCatalogURL,
		client:         http.DefaultClient,
		cacheTTL:       defaultCacheTTL,
		refreshTimeout: defaultRefreshTimeout,
	}
	for _, opt := range opts {
		opt(store)
	}

	err := store.fetchCatalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFetchCatalog, err)
	}

	return store, nil
}

// Match reports whether a document matches a SchemaStore catalog pattern.
//
// Implements [registry.MatchLoader].
//
// Note: Match and Load both call [SchemaStore.FindMatch] independently
// rather than caching the result between calls. This is intentional:
// FindMatch only does cheap glob matching on already-cached catalog entries,
// so caching would add complexity (mutex operations, pointer identity
// contracts) for negligible benefit. The expensive catalog fetch is already
// cached via ensureCatalog with TTL. This differs from [Directive] which
// caches because token parsing is more expensive than glob matching.
func (s *SchemaStore) Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
	_, ok := s.FindMatch(ctx, doc.FilePath())

	return ok
}

// Load fetches the schema for a matching document from SchemaStore.
//
// Implements [registry.MatchLoader].
func (s *SchemaStore) Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	entry, ok := s.FindMatch(ctx, doc.FilePath())
	if !ok {
		return loader.Result{}, fmt.Errorf("%w: %q", ErrNoCatalogMatch, doc.FilePath())
	}

	//nolint:wrapcheck // URLLoader already wraps errors with context.
	return loader.URL(entry.URL, loader.WithHTTPClient(s.client)).Load(ctx, doc)
}

// FindMatch finds a matching catalog entry for a file path.
//
// Returns the matching entry and true if found, or a zero value and false
// if no entry matches.
func (s *SchemaStore) FindMatch(ctx context.Context, filePath string) (CatalogEntry, bool) {
	if filePath == "" {
		return CatalogEntry{}, false
	}

	// Best-effort refresh: if the catalog fetch fails, continue with stale
	// cached data rather than failing the lookup. This provides fault tolerance
	// when SchemaStore.org is temporarily unavailable.
	select {
	case <-ctx.Done():
		// Skip refresh if context already canceled.
	default:
		// Use a timeout to prevent hung connections from blocking indefinitely.
		refreshCtx, cancel := context.WithTimeout(ctx, s.refreshTimeout)
		defer cancel()

		_ = s.ensureCatalog(refreshCtx) //nolint:errcheck // Best-effort refresh.
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, entry := range s.entries {
		if matchGlobPatterns(filePath, entry.FileMatch) {
			return entry, true
		}
	}

	return CatalogEntry{}, false
}

// ensureCatalog refreshes the catalog if the cache has expired.
func (s *SchemaStore) ensureCatalog(ctx context.Context) error {
	s.mu.RLock()
	if s.cacheTTL > 0 && time.Since(s.lastFetch) < s.cacheTTL {
		s.mu.RUnlock()

		return nil
	}

	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if s.cacheTTL > 0 && time.Since(s.lastFetch) < s.cacheTTL {
		return nil
	}

	err := s.fetchCatalogLocked(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFetchCatalog, err)
	}

	return nil
}

// fetchCatalog retrieves the catalog from the configured URL and stores
// the filtered entries. Acquires the write lock internally.
func (s *SchemaStore) fetchCatalog(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if s.entries != nil && s.cacheTTL > 0 && time.Since(s.lastFetch) < s.cacheTTL {
		return nil
	}

	return s.fetchCatalogLocked(ctx)
}

// maxCatalogSize is the maximum size of the catalog response body.
const maxCatalogSize = 10 * 1024 * 1024 // 10 MB.

// fetchCatalogLocked retrieves the catalog from the configured URL and stores
// the filtered entries. Caller must hold the write lock.
func (s *SchemaStore) fetchCatalogLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.catalogURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", s.catalogURL, err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", s.catalogURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", s.catalogURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize))
	if err != nil {
		return fmt.Errorf("read response from %s: %w", s.catalogURL, err)
	}

	// If we read exactly maxCatalogSize bytes, try reading one more to detect
	// if the response was truncated. If we can read another byte (err == nil)
	// or get any error other than EOF, the actual size exceeds our limit.
	if int64(len(data)) == maxCatalogSize {
		var extra [1]byte

		_, err = resp.Body.Read(extra[:])
		if err == nil || !errors.Is(err, io.EOF) {
			return fmt.Errorf("fetch %s: catalog exceeds %d bytes", s.catalogURL, maxCatalogSize)
		}
	}

	var catalog Catalog

	err = json.Unmarshal(data, &catalog)
	if err != nil {
		return fmt.Errorf("parse catalog from %s: %w", s.catalogURL, err)
	}

	// Prefilter entries: only keep entries with YAML patterns that pass the filter.
	s.entries = s.filterAndNormalizeEntries(catalog.Schemas)
	s.lastFetch = time.Now()

	return nil
}

// filterAndNormalizeEntries filters catalog entries to only those with YAML
// patterns that pass the configured filter. It also normalizes each entry's
// FileMatch to contain only YAML-related patterns.
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

		// Only consider YAML-related patterns.
		yamlPatterns := filterYAMLPatterns(entry.FileMatch)
		if len(yamlPatterns) == 0 {
			continue
		}

		// Store entry with only YAML patterns.
		entry.FileMatch = yamlPatterns
		entries = append(entries, entry)
	}

	return entries
}

// filterYAMLPatterns returns only patterns that match YAML files.
func filterYAMLPatterns(patterns []string) []string {
	var result []string

	for _, pattern := range patterns {
		lower := strings.ToLower(pattern)
		if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
			result = append(result, pattern)
		}
	}

	return result
}

// matchGlobPatterns checks if a file path matches any of the given glob patterns.
// Patterns are matched against both the full path and the base name to handle
// SchemaStore patterns that may or may not include directory components.
func matchGlobPatterns(filePath string, patterns []string) bool {
	return filepaths.MatchAnyWithBase(filePath, patterns)
}
