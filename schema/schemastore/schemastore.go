package schemastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Default catalog URL and timings for the store.
const (
	defaultCatalogURL     = "https://www.schemastore.org/api/json/catalog.json"
	defaultCacheTTL       = 1 * time.Hour
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = 1 * time.Minute
)

var (
	// ErrFetchCatalog indicates the store could not fetch the SchemaStore
	// catalog and no earlier fetch succeeded, so there is no catalog to
	// match against.
	ErrFetchCatalog = errors.New("fetch schema catalog")

	// ErrNoCatalogMatch indicates no catalog entry matches the document's file
	// path. It wraps [schema.ErrNoMatch], so a
	// [go.jacobcolvin.com/niceyaml/schema.Registry] moves on to the
	// next resolver.
	ErrNoCatalogMatch = fmt.Errorf("%w: no catalog entry matches", schema.ErrNoMatch)

	// The error that carries a call to [runtime.Goexit] out of the refresh
	// goroutine, so each lookup that waited for the fetch can end its own
	// goroutine the same way.
	errGoexit = errors.New("catalog fetch called runtime.Goexit")

	// An extglob group, such as the "!(config)" in
	// "**/.github/ISSUE_TEMPLATE/!(config).yml". The matcher implements no
	// extglob and reads such a group as literal text.
	extglobRE = regexp.MustCompile(`[?*+@!]\(`)

	// File extensions of formats that a YAML parser rejects, such as TOML
	// and JSON with comments. The catalog lists many YAML and JSON formats
	// under extensions of their own, such as CITATION.cff and
	// *.sublime-syntax, so the store drops only these.
	nonYAMLExtensions = map[string]bool{
		"cjs": true, "cts": true, "ini": true, "js": true,
		"json5": true, "jsonc": true, "mjs": true, "mts": true,
		"patch": true, "toml": true, "ts": true, "xml": true,
	}
)

// Catalog represents the SchemaStore.org catalog structure that the
// catalog API returns.
type Catalog struct {
	Schemas []CatalogEntry `json:"schemas"`
}

// CatalogEntry represents a single schema entry in the catalog.
type CatalogEntry struct {
	// Name is the display name of the schema.
	Name string `json:"name"`
	// Description provides details about the schema's purpose.
	Description string `json:"description"`
	// URL is the HTTP or HTTPS URL to fetch the schema from. When the
	// store loads the catalog, it drops an entry with any other URL.
	URL string `json:"url"`
	// FileMatch contains the glob patterns for the files this schema
	// applies to. When the store loads the catalog, it drops the patterns
	// for formats known not to be YAML, such as *.toml and *.jsonc, so
	// the slice may be shorter than the catalog's.
	FileMatch []string `json:"fileMatch"`

	// The store prepares FileMatch for matching once per catalog load, so
	// a lookup does not rewrite every pattern in the catalog.
	globs filepaths.AnyDepthPatterns
}

// Store matches documents to SchemaStore.org catalog entries.
//
// The store names a matched schema by the entry's URL, and the registry
// fetches it with the client
// [go.jacobcolvin.com/niceyaml/schema.WithHTTPClient] gave the registry.
// The store's own client and refresh timeout apply to the catalog alone.
//
// The store fetches the catalog on the first lookup and caches it for the
// configured TTL. Once the cache expires, the next lookup refreshes it. A
// refresh that fails leaves the previous catalog in use, and a fetch that
// fails before any catalog has loaded reports [ErrFetchCatalog]. After a
// failed fetch the store waits the retry interval before contacting the
// catalog URL again, so an unreachable catalog costs one timeout per
// interval rather than one per lookup.
//
// Concurrent lookups share a single fetch. A lookup that arrives while a
// refresh is running uses the previous catalog without waiting. The lookup
// that starts a fetch, and any lookup with no catalog to fall back on, waits
// for the fetch to finish or for its own context to end. A lookup that stops
// waiting leaves the fetch running, so the fetched catalog still reaches
// later lookups.
//
// Store implements [schema.Resolver] and goes straight into a
// [go.jacobcolvin.com/niceyaml/schema.Registry] through
// [schema.WithResolvers]. [ErrFetchCatalog] does not wrap
// [schema.ErrNoMatch], so while no catalog has loaded, the registry stops
// at the store and does not try the resolvers after it. Place the store
// after any resolver that should still apply without the catalog. Create
// instances with [New].
//
// Example:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
type Store struct {
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

// A panicError carries a panic out of the refresh goroutine as an error,
// so the lookup that waits for the fetch can raise it again.
type panicError struct {
	value any
}

// Error implements error.
func (p *panicError) Error() string {
	return fmt.Sprintf("catalog fetch panicked: %v", p.value)
}

// Option configures [Store] creation.
//
// Available options:
//   - [WithCatalogURL]
//   - [WithHTTPClient]
//   - [WithCacheTTL]
//   - [WithRefreshTimeout]
//   - [WithRetryAfter]
//   - [WithFilter]
type Option func(*Store)

// WithCatalogURL is an [Option] that sets a custom catalog URL.
//
// Defaults to "https://www.schemastore.org/api/json/catalog.json".
func WithCatalogURL(url string) Option {
	return func(s *Store) {
		s.catalogURL = url
	}
}

// WithHTTPClient is an [Option] that sets a custom HTTP client for fetching
// the catalog. A nil client keeps the default, [http.DefaultClient]. The
// registry fetches the schemas the catalog names with its own client, from
// [go.jacobcolvin.com/niceyaml/schema.WithHTTPClient].
func WithHTTPClient(client *http.Client) Option {
	return func(s *Store) {
		if client != nil {
			s.client = client
		}
	}
}

// WithCacheTTL is an [Option] that sets the cache time-to-live for the catalog.
//
// Defaults to 1 hour. Set to 0 to disable caching (fetch on every lookup).
func WithCacheTTL(ttl time.Duration) Option {
	return func(s *Store) {
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
// timeout alone bounds how long it runs. The registry fetches a matched
// schema itself, and its client and the context of the lookup bound that
// fetch.
//
// Defaults to 10 seconds. A timeout of zero or less keeps the default,
// since a fetch under an expired deadline could never succeed.
func WithRefreshTimeout(timeout time.Duration) Option {
	return func(s *Store) {
		if timeout > 0 {
			s.refreshTimeout = timeout
		}
	}
}

// WithRetryAfter is an [Option] that sets how long the store waits after a
// failed catalog fetch before trying again.
//
// Until the interval passes, lookups use the previous catalog when one
// exists and report [ErrFetchCatalog] with the last fetch error otherwise,
// without contacting the catalog URL.
//
// Defaults to 1 minute. An interval of zero or less keeps the default,
// since it would retry on every lookup.
func WithRetryAfter(interval time.Duration) Option {
	return func(s *Store) {
		if interval > 0 {
			s.retryAfter = interval
		}
	}
}

// WithFilter is an [Option] that sets a filter function for catalog entries.
//
// The store matches only the entries for which the filter returns true.
// It calls the filter during catalog refresh, so the filter should avoid
// expensive or stateful operations. A panic or a call to [runtime.Goexit]
// in the filter happens
// again in each lookup that waits for the refresh.
//
// Example:
//
//	// Only match GitHub-related schemas
//	store := schemastore.New(schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	    return strings.Contains(strings.ToLower(e.Name), "github")
//	}))
func WithFilter(fn func(CatalogEntry) bool) Option {
	return func(s *Store) {
		s.filter = fn
	}
}

// New creates a new [*Store].
//
// New performs no I/O. The store fetches the catalog on the first lookup
// and caches it for the configured TTL. Configure with options to
// customize behavior:
//
//	store := schemastore.New(
//	    schemastore.WithCacheTTL(1 * time.Hour),
//	    schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	        return strings.Contains(e.Name, "GitHub")
//	    }),
//	)
func New(opts ...Option) *Store {
	store := &Store{
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
// file path. The returned [schema.Ref] names the entry's URL, which the
// registry fetches with its own client. A document that matches no entry
// reports [ErrNoCatalogMatch], and a catalog that has never loaded reports
// [ErrFetchCatalog].
//
// Implements [schema.Resolver].
func (s *Store) Resolve(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
	entry, err := s.FindMatch(ctx, doc.FilePath())
	if err != nil {
		return schema.Ref{}, err
	}

	// The catalog keeps only entries with an HTTP or HTTPS URL, so the Ref
	// names one the registry can fetch.
	return schema.URL(entry.URL), nil
}

// FindMatch finds the catalog entry matching a file path.
//
// FindMatch resolves a relative path against the working directory before
// matching, since yaml-language-server matches the absolute path of a
// document. A pattern such as ".github/workflows/*.yml" then matches
// "ci.yml" read from inside that directory. A pattern matches at any
// depth, so the directories above the working directory never stop a
// match. For a path from [niceyaml.NewSourceFromFS], the root of the file
// system stands for the working directory.
//
// When several entries match, FindMatch returns the one whose matching
// pattern requires the most characters of the path's names literally,
// ignoring separators and wildcards. So "**/.moon/tasks/**/*.yml" wins
// over "**/tasks/*.yml" for ".moon/tasks/node.yml". Among entries that
// tie, the one listed first in the catalog wins.
//
// The returned entry owns its FileMatch patterns, so writing to them
// leaves the cached catalog alone.
//
// Returns [ErrNoCatalogMatch] when no entry matches, which includes an
// empty file path, and [ErrFetchCatalog] when no catalog has loaded, either
// because the fetch failed or because ctx ended before it finished.
func (s *Store) FindMatch(ctx context.Context, filePath string) (CatalogEntry, error) {
	if filePath == "" {
		return CatalogEntry{}, fmt.Errorf("%w: document has no file path", ErrNoCatalogMatch)
	}

	// Abs fails only when the working directory is unknown, and the path
	// as written is then the closest match left.
	matchPath := filePath

	if !filepath.IsAbs(filePath) {
		abs, err := filepath.Abs(filePath)
		if err == nil {
			matchPath = abs
		}
	}

	entries, err := s.catalog(ctx)
	if err != nil {
		return CatalogEntry{}, err
	}

	cleanPath := filepaths.CleanPath(matchPath)

	var (
		best      CatalogEntry
		bestScore int
		found     bool
	)

	for _, entry := range entries {
		score, ok := entry.globs.SpecificityClean(cleanPath)
		if ok && (!found || score > bestScore) {
			best, bestScore, found = entry, score, true
		}
	}

	if !found {
		return CatalogEntry{}, fmt.Errorf("%w: %q", ErrNoCatalogMatch, filePath)
	}

	// The cached entry shares its pattern slice with s.entries, so hand
	// the caller a copy it can write to. The prepared globs serve lookups
	// alone, so the caller gets none.
	best.FileMatch = slices.Clone(best.FileMatch)
	best.globs = filepaths.AnyDepthPatterns{}

	return best, nil
}

// catalog returns the catalog entries. It fetches or refreshes them first
// when the cache is empty or expired.
//
// A lookup that waits for a fetch stops waiting when ctx ends, unless the
// fetch has finished by then. When the fetch fails or the wait ends early,
// the lookup falls back to the previous entries, or reports ErrFetchCatalog
// when none exist.
func (s *Store) catalog(ctx context.Context) ([]CatalogEntry, error) {
	call, entries, err := s.join(ctx)
	if call == nil {
		return entries, err
	}

	select {
	case <-call.done:
	case <-ctx.Done():
		// The fetch may have finished in the same instant the context
		// ended. Its outcome counts when it is ready, so the result does
		// not depend on which case the select picks.
		select {
		case <-call.done:
		default:
			return s.stale(ctx.Err())
		}
	}

	// The fetch runs on a goroutine of its own, where no caller can recover
	// a panic, so the fetch hands the panic back as an error and each
	// lookup that waited for it raises it here. The fetch hands back a call
	// to runtime.Goexit the same way, and each lookup that waited for it
	// calls runtime.Goexit in turn.
	if pe, ok := errors.AsType[*panicError](call.err); ok {
		panic(pe.value)
	}

	if errors.Is(call.err, errGoexit) {
		runtime.Goexit()
	}

	if call.err != nil {
		return s.stale(call.err)
	}

	return call.entries, nil
}

// join decides under the lock whether a lookup can answer without waiting
// for a fetch. It returns the entries or error to report when the lookup
// can, and otherwise the fetch to wait on, starting one when none is
// running.
func (s *Store) join(ctx context.Context) (*fetchCall, []CatalogEntry, error) {
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
// does not cancel a fetch that other lookups share. The refresh timeout
// bounds the fetch instead.
//
// A fetch that calls [runtime.Goexit] ends the refresh goroutine without
// returning, so refresh records the outcome from a deferred call, which
// reports errGoexit when the fetch never returned.
func (s *Store) refresh(ctx context.Context, call *fetchCall) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.refreshTimeout)
	defer cancel()

	var entries []CatalogEntry

	err := errGoexit

	defer func() {
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
	}()

	entries, err = s.fetchRecovering(ctx)
}

// fetchRecovering runs fetch and turns a panic in the filter, the HTTP
// transport, or the catalog parsing into a [*panicError].
func (s *Store) fetchRecovering(ctx context.Context) ([]CatalogEntry, error) {
	var entries []CatalogEntry

	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = &panicError{value: p}
			}
		}()

		entries, err = s.fetch(ctx)

		return err
	}()

	return entries, err
}

// stale returns the previous entries when a fetch has ever succeeded and
// otherwise wraps cause in ErrFetchCatalog.
func (s *Store) stale(cause error) ([]CatalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.staleLocked(cause)
}

// staleLocked is stale for a caller that already holds mu.
func (s *Store) staleLocked(cause error) ([]CatalogEntry, error) {
	if s.lastFetch.IsZero() {
		return nil, fmt.Errorf("%w: %w", ErrFetchCatalog, cause)
	}

	return s.entries, nil
}

// fetch retrieves the catalog from the configured URL and returns the
// filtered entries. It holds no lock, so a slow catalog server blocks only
// the lookups waiting on this fetch.
//
// The HTTP GET and its size limit are the ones [schema.URL] uses. Only the
// catalog JSON parsing is specific to SchemaStore.
func (s *Store) fetch(ctx context.Context) ([]CatalogEntry, error) {
	data, err := httpfetch.Get(ctx, s.client, s.catalogURL)
	if err != nil {
		//nolint:wrapcheck // The error already names the catalog URL.
		return nil, err
	}

	var catalog Catalog

	err = json.Unmarshal(data, &catalog)
	if err != nil {
		return nil, fmt.Errorf("parse catalog from %s: %w", httpfetch.Redacted(s.catalogURL), err)
	}

	// A body with no schemas array, such as null or an error object,
	// decodes without error, so fetch rejects it here. The store then keeps
	// the previous catalog rather than replacing it with an empty one.
	if catalog.Schemas == nil {
		return nil, fmt.Errorf("parse catalog from %s: no schemas array", httpfetch.Redacted(s.catalogURL))
	}

	// Prefilter entries to those with supported patterns that pass the filter.
	return s.filterAndNormalizeEntries(catalog.Schemas), nil
}

// filterAndNormalizeEntries filters catalog entries to only those with
// supported patterns that pass the configured filter. It also normalizes each
// entry's FileMatch to contain only supported patterns (YAML and JSON files).
func (s *Store) filterAndNormalizeEntries(schemas []CatalogEntry) []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(schemas))

	for _, entry := range schemas {
		// Skip entries without an HTTP or HTTPS URL, which the registry
		// cannot fetch. A file:// URL would name a schema the registry
		// serves only when a File Ref cached it first.
		if !httpfetch.IsHTTPURL(entry.URL) {
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
		entry.globs = filepaths.NewAnyDepthPatterns(supportedPatterns)
		entries = append(entries, entry)
	}

	return entries
}

// filterSupportedPatterns returns the patterns that can match a YAML or
// JSON file, since JSON is a valid subset of YAML. It drops a pattern
// whose last segment carries the literal extension of a format known not
// to be YAML, such as "*.toml" or ".eslintrc.jsonc", and keeps every
// other pattern. A pattern with brace alternatives, such as
// "*.{toml,yaml}", counts when any of its alternatives can match a YAML
// file.
//
// An extglob group makes filterSupportedPatterns drop the pattern, since
// the matcher reads the group literally and the pattern could only match a
// file named after the text of the group. The entry count then reflects
// the entries that can match a file someone would write.
func filterSupportedPatterns(patterns []string) []string {
	var result []string

	for _, pattern := range patterns {
		if extglobRE.MatchString(pattern) {
			continue
		}

		if slices.ContainsFunc(filepaths.ExpandBraces(pattern), canMatchYAML) {
			result = append(result, pattern)
		}
	}

	return result
}

// canMatchYAML reports whether pattern can match a YAML or JSON file, in
// any letter case. It reports false only when the last segment ends in
// the literal extension of a format known not to be YAML, such as
// ".toml". A wildcard extension, as in "azure-pipelines*.y*ml", a name
// without an extension, such as ".clang-format", and the extension of
// another YAML format, such as "CITATION.cff", all pass.
func canMatchYAML(pattern string) bool {
	lower := strings.ToLower(pattern)
	base := lower[strings.LastIndex(lower, "/")+1:]

	// A dot that starts the name marks a hidden file, not an extension.
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return true
	}

	return !nonYAMLExtensions[base[dot+1:]]
}
