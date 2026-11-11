package schemastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/capture"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/schema"
)

// These constants hold the default catalog URL and timings of the store.
const (
	defaultCatalogURL     = "https://www.schemastore.org/api/json/catalog.json"
	defaultCacheTTL       = 1 * time.Hour
	defaultRefreshTimeout = 10 * time.Second
	defaultRetryAfter     = 1 * time.Minute
)

var (
	// ErrFetchCatalog indicates that the store could not load the catalog
	// that [WithCatalog] names. A lookup reports it only when the store
	// does not hold an earlier catalog to match against. [Store.Refresh]
	// reports it for every fetch that fails.
	ErrFetchCatalog = errors.New("fetch schema catalog")

	// ErrNoCatalogMatch indicates that the file path of the document does
	// not match any catalog entry. It wraps [schema.ErrNoMatch], so a
	// [go.jacobcolvin.com/niceyaml/schema.Registry] moves on to the
	// next resolver.
	ErrNoCatalogMatch = fmt.Errorf("%w: no catalog entry matches", schema.ErrNoMatch)

	// The extglobRE pattern detects an extglob group, such as the
	// "!(config)" in "**/.github/ISSUE_TEMPLATE/!(config).yml". The matcher
	// does not implement extglob and reads such a group as literal text.
	extglobRE = regexp.MustCompile(`[?*+@!]\(`)

	// The nonYAMLExtensions map holds the file extensions of formats that a
	// YAML parser rejects, such as TOML and JSON with comments. The catalog
	// lists many YAML and JSON formats under extensions of their own, such
	// as CITATION.cff and *.sublime-syntax. So the store drops only the
	// patterns for these extensions.
	nonYAMLExtensions = map[string]bool{
		"cjs": true, "cts": true, "ini": true, "js": true,
		"json5": true, "jsonc": true, "mjs": true, "mts": true,
		"patch": true, "toml": true, "ts": true, "xml": true,
	}
)

// Catalog represents the SchemaStore.org catalog as the catalog API
// returns it. The bytes that the [schema.Ref] of [WithCatalog] names hold
// a Catalog in JSON. You can marshal a Catalog of your own to JSON and
// pass the bytes to the store through [schema.Embedded], such as for a
// test or for a program that ships a catalog of its own:
//
//	data, err := json.Marshal(catalog)
//	if err != nil {
//	    return err
//	}
//
//	store := schemastore.New(schemastore.WithCatalog(schema.Embedded(data)))
//
// A Catalog holds every entry as the catalog lists it. The store drops
// some entries and patterns when it loads the catalog, and
// [Store.Entries] returns what the store matches against.
type Catalog struct {
	Schemas []CatalogEntry `json:"schemas"`
}

// CatalogEntry represents a single schema entry in the catalog.
type CatalogEntry struct {
	// Name is the display name of the schema.
	Name string `json:"name"`
	// Description describes the purpose of the schema.
	Description string `json:"description"`
	// URL is the HTTP or HTTPS URL to fetch the schema from. When the
	// store loads the catalog, it drops the entries with any other URL.
	URL string `json:"url"`
	// FileMatch contains the glob patterns for the files that this schema
	// applies to. When the store loads the catalog, it drops some
	// patterns, so the slice might be shorter than the one in the catalog.
	// It drops the patterns for formats that are not YAML, such as *.toml
	// and *.jsonc. It drops the patterns that hold an extglob group, such
	// as "!(config).yml". It also drops the patterns whose braces expand to
	// too many patterns or take too much work to expand.
	FileMatch []string `json:"fileMatch"`

	// The globs field holds FileMatch prepared for matching. The store
	// prepares it once per catalog load, so a lookup does not rewrite every
	// pattern in the catalog.
	globs filepaths.AnyDepthPatterns
}

// Store matches documents to SchemaStore.org catalog entries.
//
// The store names a matched schema by the URL of the entry. The registry
// fetches that URL with the client that
// [go.jacobcolvin.com/niceyaml/schema.WithHTTPClient] gave the registry,
// or with the loader of [go.jacobcolvin.com/niceyaml/schema.WithLoader]
// whose prefix covers the URL.
// The store's own client, refresh timeout, and load limit apply only to
// the catalog, which [WithCatalog] names.
//
// The store fetches the catalog on the first lookup and caches it for the
// configured time to live (TTL). After the cache expires, the next lookup
// refreshes it. A refresh that fails leaves the previous catalog in use.
// When a fetch fails before any catalog has loaded, the lookup reports
// [ErrFetchCatalog]. After a failed fetch, the store waits the retry
// interval before it loads the catalog again. So an unreachable catalog
// costs one timeout per interval rather than one per lookup.
//
// [Store.Entries] loads the catalog as a lookup does, so a program can
// call it at startup to fetch the catalog before the first document.
// [Store.Refresh] fetches the catalog at once and reports the error of
// each failed fetch. Lookups report that error only before any catalog
// has loaded.
//
// The TTL covers only the catalog. By default the registry keeps each
// schema that it fetched for an entry, even after a refresh of the
// catalog. A long-running program gives the registry
// [go.jacobcolvin.com/niceyaml/schema.WithRefreshAfter]. The registry
// then fetches each schema again after that interval, with the documents
// that its $refs name. When the fetch fails, the registry serves the copy
// that it holds, as the store serves the previous catalog. A client with
// a caching Transport, from
// [go.jacobcolvin.com/niceyaml/schema.WithHTTPClient], turns each such
// fetch into a conditional request.
//
// Concurrent lookups share a single fetch. A lookup that arrives while a
// refresh is running uses the previous catalog without waiting. The lookup
// that starts a fetch waits for the fetch to finish or for its own context
// to end. So does every lookup that does not have a previous catalog to
// use. Lookups that stop waiting leave the fetch running, so the fetched
// catalog still reaches later lookups.
//
// Store implements [schema.Resolver], so you can pass it to a
// [go.jacobcolvin.com/niceyaml/schema.Registry] through
// [schema.WithResolvers]. [ErrFetchCatalog] does not wrap
// [schema.ErrNoMatch]. So until a catalog has loaded, the registry stops
// at the store and does not try the resolvers after it. Place the store
// after every resolver that should still apply without the catalog.
// Create instances with [New].
//
// Example:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
type Store struct {
	lastFetch   time.Time                 // Last successful fetch, or zero before the first one succeeds.
	lastAttempt time.Time                 // Last fetch, successful or not.
	inflight    *fetchCall                // Fetch in progress, or nil when the store is not fetching.
	lastErr     error                     // Error from the last fetch, or nil when it succeeded.
	loader      *schema.Registry          // Loads the catalog Ref with the client and limit of the config.
	lastMatch   atomic.Pointer[matchMemo] // Outcome of the last scan in FindMatch.
	entries     []CatalogEntry
	config
	mu sync.Mutex // Guards entries, inflight, lastFetch, lastAttempt, and lastErr.
}

// fetchCall is a catalog fetch in progress. The fetch sets entries and err
// before it closes done.
type fetchCall struct {
	done    chan struct{}
	err     error
	entries []CatalogEntry
}

// Option configures [Store] creation.
//
// Available options:
//   - [WithCatalog]
//   - [WithHTTPClient]
//   - [WithCacheTTL]
//   - [WithRefreshTimeout]
//   - [WithRetryAfter]
//   - [WithFilter]
//   - [WithLoadLimit]
type Option func(*config)

// config holds the settings that [Option] functions configure. Each option
// takes a config in place of the [Store] that embeds it, so only [New] can
// apply an option.
type config struct {
	client         *http.Client
	filter         func(CatalogEntry) bool
	catalogRef     schema.Ref
	cacheTTL       time.Duration
	refreshTimeout time.Duration
	retryAfter     time.Duration
	loadLimit      int
}

// WithCatalog is an [Option] that sets the [schema.Ref] that names the
// catalog. The bytes of the Ref hold a [Catalog] in JSON.
//
// The default is the Ref of [schema.URL] for
// "https://www.schemastore.org/api/json/catalog.json". The store loads
// the catalog as [schema.Registry.Load] does, in a registry of its own.
// That registry fetches a Ref from [schema.URL] with the client of
// [WithHTTPClient], and it reads a file or a URL within the limit of
// [WithLoadLimit]. It reads every file that a Ref from [schema.File]
// names. The registry that the store serves does not take part, so its
// client, its [schema.Access], and its loaders do not apply to the
// catalog.
//
// The store loads the Ref again each time the cache TTL expires, so a Ref
// from [schema.File] or [schema.Loadable] can supply a catalog that
// changes. The registry of the store keeps nothing by key, and it runs
// the load of a Ref from [schema.Loadable] on each load of the catalog.
// So the load of a Ref that names the catalog can return different bytes
// each time it runs, unlike the load of a Ref that names a schema:
//
//	//go:embed catalog.json
//	var catalogJSON []byte
//
//	// A catalog that the program ships.
//	pinned := schemastore.New(schemastore.WithCatalog(schema.Embedded(catalogJSON)))
//
//	// A catalog on disk.
//	local := schemastore.New(schemastore.WithCatalog(schema.File("/etc/app/catalog.json")))
//
//	// A mirror of the catalog, fetched through a proxy.
//	mirror := schemastore.New(
//	    schemastore.WithCatalog(schema.URL("https://mirror.example.com/catalog.json")),
//	    schemastore.WithHTTPClient(proxied),
//	)
//
//	// The live catalog, with the shipped one when the download fails.
//	live := schemastore.New(schemastore.WithCatalog(
//	    schema.Loadable("catalog", func(ctx context.Context) ([]byte, error) {
//	        data, err := download(ctx)
//	        if err != nil {
//	            return catalogJSON, nil
//	        }
//
//	        return data, nil
//	    }),
//	))
//
// The zero Ref and a Ref that carries a compiled schema, such as one from
// [schema.Schema.Ref], do not name any bytes. With either Ref, every load
// fails, so the first lookup reports [ErrFetchCatalog]. The registry of
// the store has no [schema.Access], so it refuses a Ref that a document
// names, such as one from [schema.RefBeside]. With such a Ref, the first
// lookup reports [ErrFetchCatalog] with a cause that matches
// [schema.ErrAccessNotSet].
func WithCatalog(ref schema.Ref) Option {
	return func(c *config) {
		c.catalogRef = ref
	}
}

// WithHTTPClient is an [Option] that sets a custom HTTP client for fetching
// the catalog, when [WithCatalog] names it with a [schema.URL]. A nil
// client keeps the default, [http.DefaultClient]. The
// registry fetches the schemas that the catalog names with its own client,
// from [go.jacobcolvin.com/niceyaml/schema.WithHTTPClient].
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) {
		if client != nil {
			c.client = client
		}
	}
}

// WithCacheTTL is an [Option] that sets the cache time to live (TTL) for
// the catalog.
//
// The default is 1 hour. A TTL of 0 disables the cache, so the store
// fetches the catalog on every lookup.
func WithCacheTTL(ttl time.Duration) Option {
	return func(c *config) {
		c.cacheTTL = ttl
	}
}

// WithRefreshTimeout is an [Option] that sets the timeout for each catalog
// fetch, the first one included.
//
// A lookup that finds the cache expired fetches the catalog under this
// timeout. If the fetch fails or times out, the lookup uses the previous
// catalog when one exists. So a temporarily unreachable SchemaStore.org
// degrades to stale matches rather than errors. The fetch does not inherit
// the cancellation or deadline of the call that started it, so only this
// timeout bounds how long it runs. The registry fetches a matched schema
// itself, and its client and the context of the lookup bound that fetch.
//
// The default is 10 seconds. A timeout of zero or less keeps the default,
// because a fetch cannot succeed under an expired deadline.
func WithRefreshTimeout(timeout time.Duration) Option {
	return func(c *config) {
		if timeout > 0 {
			c.refreshTimeout = timeout
		}
	}
}

// WithRetryAfter is an [Option] that sets how long the store waits after a
// failed catalog fetch before it tries again.
//
// Until the interval passes, lookups do not load the catalog. They
// use the previous catalog when one exists, and otherwise report
// [ErrFetchCatalog] with the last fetch error. The interval does not
// apply to [Store.Refresh], which fetches at once.
//
// The default is 1 minute. An interval of zero or less keeps the default,
// so the store does not retry on every lookup.
func WithRetryAfter(interval time.Duration) Option {
	return func(c *config) {
		if interval > 0 {
			c.retryAfter = interval
		}
	}
}

// WithLoadLimit is an [Option] that sets the most bytes of the catalog
// that the store reads from a file or a URL, when [WithCatalog] names it
// with a [schema.File], [schema.FileFS], or [schema.URL]. The read or
// fetch of a larger catalog fails with a [*schema.FetchError] whose cause
// matches [niceyaml.ErrLimit]. The store treats that failure like any
// other failed fetch. It keeps the previous catalog when one has loaded,
// and otherwise reports [ErrFetchCatalog]. The limit does not cover the
// bytes of a Ref from [schema.Embedded] or [schema.Loadable].
//
// The limit covers only the catalog. The registry reads each schema that
// the catalog names within the limit of [schema.WithLoadLimit].
//
// The default is [schema.DefaultLoadLimit]. [niceyaml.NoLimit] removes
// it, so the store reads a catalog of any size. A limit of zero or less
// keeps the limit as it is, as [WithRetryAfter] keeps its interval. Given
// more than once, the last option with a positive limit wins.
func WithLoadLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.loadLimit = n
		}
	}
}

// WithFilter is an [Option] that sets a filter function for catalog entries.
//
// The store matches only the entries for which the filter returns true.
// It calls the filter during catalog refresh, so the filter should avoid
// expensive or stateful operations. A panic or a call to [runtime.Goexit]
// in the filter repeats in each call that waits for the refresh. Lookups,
// [Store.Entries], and [Store.Refresh] all wait for it.
//
// Example:
//
//	// Match only GitHub-related schemas.
//	store := schemastore.New(schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	    return strings.Contains(strings.ToLower(e.Name), "github")
//	}))
func WithFilter(fn func(CatalogEntry) bool) Option {
	return func(c *config) {
		c.filter = fn
	}
}

// New creates a new [*Store].
//
// New does not perform I/O. The store fetches the catalog on the first
// lookup, or on the first call to [Store.Entries] or [Store.Refresh], and
// caches it for the configured TTL. Pass options to configure
// the store:
//
//	store := schemastore.New(
//	    schemastore.WithCacheTTL(1 * time.Hour),
//	    schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
//	        return strings.Contains(e.Name, "GitHub")
//	    }),
//	)
func New(opts ...Option) *Store {
	store := &Store{
		catalogRef:     schema.URL(defaultCatalogURL),
		client:         http.DefaultClient,
		cacheTTL:       defaultCacheTTL,
		refreshTimeout: defaultRefreshTimeout,
		retryAfter:     defaultRetryAfter,
		loadLimit:      schema.DefaultLoadLimit,
	}
	for _, opt := range opts {
		opt(&store.config)
	}

	store.loader = schema.NewRegistry(
		schema.WithHTTPClient(store.client),
		schema.WithLoadLimit(store.loadLimit),
	)

	return store
}

// Resolve names the schema for the catalog entry that matches the file
// path of the document. The returned [schema.Ref] names the URL of the
// entry, which the registry fetches with its own client. Resolve returns
// [ErrNoCatalogMatch] for a document that does not match any entry, and
// [ErrFetchCatalog] when the store has not loaded a catalog.
//
// Resolve implements [schema.Resolver].
func (s *Store) Resolve(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
	entry, err := s.FindMatch(ctx, doc.FilePath())
	if err != nil {
		return schema.Ref{}, err
	}

	// The catalog keeps only entries with an HTTP or HTTPS URL, so the Ref
	// names one the registry can fetch.
	return schema.URL(entry.URL), nil
}

// FindMatch finds the catalog entry that matches a file path.
//
// FindMatch resolves a relative path against the working directory before
// it matches, because yaml-language-server matches the absolute path of a
// document. A pattern such as ".github/workflows/*.yml" then matches
// "ci.yml" read from inside that directory. Patterns match at any depth,
// so the directories above the working directory never stop a match. For
// a path from [niceyaml.NewSourceFromFS], the root of the file system
// stands for the working directory.
//
// When several entries match, FindMatch returns the one whose matching
// pattern spells out the most characters of the names in the path.
// Separators and wildcards do not count. So "**/.moon/tasks/**/*.yml"
// wins over "**/tasks/*.yml" for ".moon/tasks/node.yml". Among entries
// that tie, the one listed first in the catalog wins.
//
// The returned entry owns its FileMatch patterns, so a write to them does
// not change the cached catalog.
//
// FindMatch returns [ErrNoCatalogMatch] for an empty file path and for a
// path that does not match any entry. Before any catalog has loaded, it
// returns [ErrFetchCatalog] when the fetch fails or when ctx ends before
// the fetch finishes.
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

	// A registry resolves each document of a multi-document file on its
	// own, and every one of those lookups brings the same path. The scan
	// depends only on the catalog load and the path, so FindMatch reuses
	// the last scan's outcome when both are unchanged.
	var (
		best  CatalogEntry
		found bool
	)

	if memo := s.lastMatch.Load(); memo.answers(entries, cleanPath) {
		best, found = memo.best, memo.found
	} else {
		best, found = bestMatch(entries, cleanPath)
		s.lastMatch.Store(&matchMemo{entries: entries, path: cleanPath, best: best, found: found})
	}

	if !found {
		return CatalogEntry{}, fmt.Errorf("%w: %q", ErrNoCatalogMatch, filePath)
	}

	return best.detach(), nil
}

// Entries returns the catalog entries that the store matches against, in
// catalog order. The store keeps only the entries that have an HTTP or
// HTTPS URL, pass the filter of [WithFilter], and keep at least one
// pattern. Each entry holds only the patterns that the store matches, as
// [CatalogEntry] describes for FileMatch.
//
// Entries loads the catalog as a lookup does. Before any catalog has
// loaded, it returns [ErrFetchCatalog] when the fetch fails or when ctx
// ends before the fetch finishes. After a failed refresh, it returns the
// previous entries. So a program can call Entries at startup to fetch the
// catalog before the first lookup, and to report a failed fetch there.
//
// Each returned entry owns its FileMatch patterns, so a write to them does
// not change the cached catalog. Entries copies the catalog on each call.
func (s *Store) Entries(ctx context.Context) ([]CatalogEntry, error) {
	entries, err := s.catalog(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]CatalogEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry.detach()
	}

	return out, nil
}

// Refresh fetches the catalog now, whatever the cache TTL and the retry
// interval. When a fetch is already in flight, Refresh waits for that
// fetch and does not start another. A fetch that succeeds replaces the
// catalog and restarts the TTL.
//
// Refresh returns the error of the fetch wrapped in [ErrFetchCatalog],
// even when the store keeps an earlier catalog in use. Lookups still
// match against that earlier catalog. So a long-running service can call
// Refresh on a ticker and log the error to learn that its catalog went
// stale. Refresh does not wait out the retry interval, so the ticker sets
// how often the store loads the catalog.
//
// When ctx ends before the fetch finishes, Refresh returns
// [ErrFetchCatalog] wrapping the error of ctx, and the fetch keeps
// running. When ctx has ended before the call, Refresh does not start a
// fetch.
//
// Refresh raises a panic or a call to [runtime.Goexit] from the fetch
// again in its caller, such as one in the function of [WithFilter]. A
// service that calls Refresh on a goroutine of its own must recover such
// a panic there to keep running.
func (s *Store) Refresh(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		err = s.start(ctx).wait(ctx)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrFetchCatalog, err)
	}

	return nil
}

// detach returns a copy of e for a caller. The cached entry shares its
// pattern slice with the catalog of the store. So the copy gets a slice of
// its own, which the caller can write to. The prepared globs serve lookups
// alone, so the copy gets none.
func (e CatalogEntry) detach() CatalogEntry {
	e.FileMatch = slices.Clone(e.FileMatch)
	e.globs = filepaths.AnyDepthPatterns{}

	return e
}

// bestMatch returns the entry whose pattern matches cleanPath most
// specifically, and the first such entry on a tie. It reports false when
// cleanPath does not match any entry.
func bestMatch(entries []CatalogEntry, cleanPath string) (CatalogEntry, bool) {
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

	return best, found
}

// matchMemo records the outcome of one catalog scan in [Store.FindMatch].
type matchMemo struct {
	entries []CatalogEntry // Catalog load that the scan read.
	path    string         // Cleaned path that the scan matched.
	best    CatalogEntry   // Matching entry, shared with entries.
	found   bool
}

// answers reports whether m holds the scan of path against entries. Each
// catalog load builds a new slice, and m keeps its own slice alive, so a
// later load cannot reuse its address. An empty catalog does not match
// any path, so any two empty loads give the same outcome.
func (m *matchMemo) answers(entries []CatalogEntry, path string) bool {
	return m != nil && m.path == path && len(m.entries) == len(entries) &&
		(len(entries) == 0 || &m.entries[0] == &entries[0])
}

// catalog returns the catalog entries. It fetches or refreshes them first
// when the cache is empty or expired.
//
// A lookup that waits for a fetch stops waiting when ctx ends, unless the
// fetch has finished by then. When the fetch fails or the wait ends early,
// the lookup uses the previous entries when they exist, and otherwise
// reports [ErrFetchCatalog].
func (s *Store) catalog(ctx context.Context) ([]CatalogEntry, error) {
	call, entries, err := s.join(ctx)
	if call == nil {
		return entries, err
	}

	err = call.wait(ctx)
	if err != nil {
		return s.stale(err)
	}

	return call.entries, nil
}

// wait waits for the fetch to finish or for ctx to end, and returns the
// error of the fetch or of ctx. The fetch hands back a panic or a call to
// [runtime.Goexit] as an error, and each caller that waited for it raises
// it again here.
func (call *fetchCall) wait(ctx context.Context) error {
	select {
	case <-call.done:
	case <-ctx.Done():
		// The fetch might have finished in the same instant the context
		// ended. Its outcome counts when it is ready, so the result does
		// not depend on which case the select picks.
		select {
		case <-call.done:
		default:
			return ctx.Err() //nolint:wrapcheck // Each caller wraps the error.
		}
	}

	capture.Reraise(call.err)

	return call.err
}

// join decides under the lock whether a lookup can answer without waiting
// for a fetch. When the lookup can, join returns the entries or the error
// to report. Otherwise it returns the fetch to wait on, and it starts that
// fetch first when the store is not fetching.
func (s *Store) join(ctx context.Context) (*fetchCall, []CatalogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := !s.lastFetch.IsZero()

	switch {
	case loaded && s.cacheTTL > 0 && time.Since(s.lastFetch) < s.cacheTTL:
		return nil, s.entries, nil

	case s.lastErr != nil && time.Since(s.lastAttempt) < s.retryAfter:
		// The last fetch failed within the retry interval, so leave the
		// catalog alone.
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

	return s.startLocked(ctx), nil, nil
}

// start returns the fetch in flight, and starts one first when the store
// is not fetching.
func (s *Store) start(ctx context.Context) *fetchCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.startLocked(ctx)
}

// startLocked is start for a caller that already holds mu.
func (s *Store) startLocked(ctx context.Context) *fetchCall {
	if s.inflight == nil {
		s.inflight = &fetchCall{done: make(chan struct{})}

		go s.refresh(ctx, s.inflight)
	}

	return s.inflight
}

// refresh fetches the catalog for call, records the outcome, and closes
// call.done. The ctx argument belongs to the call that started the fetch,
// which is a lookup, Entries, or Refresh. The fetch drops the cancellation
// and deadline of ctx, so a caller that stops waiting does not cancel a
// fetch that other callers share. The refresh timeout bounds the fetch
// instead.
//
// No caller can recover a panic on the refresh goroutine. So refresh
// records a panic or a call to [runtime.Goexit] as the error of the fetch,
// whether it comes from the filter, the HTTP transport, or the catalog
// parsing.
func (s *Store) refresh(ctx context.Context, call *fetchCall) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.refreshTimeout)
	defer cancel()

	var entries []CatalogEntry

	err := capture.Run(func() error {
		var err error

		entries, err = s.fetch(ctx)

		return err
	})

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

// stale returns the previous entries when a fetch has ever succeeded, and
// otherwise wraps cause in [ErrFetchCatalog].
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

// fetch loads the catalog that [WithCatalog] names and returns the
// filtered entries. It does not hold a lock, so a slow catalog server
// blocks only the lookups that wait on this fetch.
//
// The private registry of the store loads the Ref, so a Ref from
// [schema.URL] fails with a [*schema.FetchError] as it does in any
// registry. Only the catalog JSON parsing is specific to SchemaStore.
func (s *Store) fetch(ctx context.Context) ([]CatalogEntry, error) {
	data, err := s.loader.Load(ctx, s.catalogRef)
	if err != nil {
		return nil, s.loadError(err)
	}

	var catalog Catalog

	err = json.Unmarshal(data, &catalog)
	if err != nil {
		return nil, fmt.Errorf("parse catalog from %s: %w", s.catalogRef.Name(), err)
	}

	// A body with no schemas array, such as null or an error object,
	// decodes without error, so fetch rejects it here. The store then keeps
	// the previous catalog rather than replacing it with an empty one.
	if catalog.Schemas == nil {
		return nil, fmt.Errorf("parse catalog from %s: no schemas array", s.catalogRef.Name())
	}

	// Prefilter entries to those with supported patterns that pass the filter.
	return s.filterAndNormalizeEntries(catalog.Schemas), nil
}

// loadError returns the error of a catalog load that failed with err, an
// error of [schema.Registry.Load]. The [*schema.LoadError] of err calls
// the catalog a schema, so loadError drops it and keeps its cause. The
// registry reads a File Ref and fetches a URL Ref with errors that name
// the file or URL already. Each such error is a [*schema.FetchError] or
// an [*httpfetch.URLError] that the registry does not wrap. So loadError
// returns a cause of either type as it is. Any other cause gets "load
// catalog" and the name of the Ref. When the Ref does not have a name,
// the cause gets only "load catalog". A cause that wraps one of the two
// types gets the prefix too. So the error of a Loadable names the
// catalog even when it wraps the error of another registry.
func (s *Store) loadError(err error) error {
	if loadErr, ok := errors.AsType[*schema.LoadError](err); ok {
		err = loadErr.Err
	}

	// Only the errors that the registry returns itself count, and the
	// registry returns them unwrapped. A search of the whole chain lets
	// the error that a Loadable wraps decide the message.
	switch err.(type) { //nolint:errorlint // Only the direct cause counts.
	case *schema.FetchError, *httpfetch.URLError:
		return err
	}

	name := s.catalogRef.Name()
	if name == "" {
		return fmt.Errorf("load catalog: %w", err)
	}

	return fmt.Errorf("load catalog %q: %w", name, err)
}

// filterAndNormalizeEntries keeps only the catalog entries that have an
// HTTP or HTTPS URL and supported patterns, and that pass the configured
// filter. It also trims the FileMatch of each entry to the patterns that
// [filterSupportedPatterns] keeps.
func (s *Store) filterAndNormalizeEntries(schemas []CatalogEntry) []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(schemas))

	for _, entry := range schemas {
		// Skip entries without an HTTP or HTTPS scheme, which the
		// registry cannot fetch. A file:// URL would name a schema the
		// registry serves only when a File Ref cached it first. An entry
		// with an HTTP URL that does not parse stays, so its lookup fails
		// with the parse error rather than falling through.
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

		// Drop the patterns for non-YAML formats, extglob groups, and
		// braces past the limit.
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
// JSON file, because JSON is a valid subset of YAML. It drops the patterns
// that hold an extglob group. It also drops the patterns whose last
// segment carries the literal extension of a format that is not YAML,
// such as "*.toml" or ".eslintrc.jsonc". It drops the patterns for which
// [filepaths.ExpandBraces] returns [filepaths.ErrBraceLimit] too, because
// [filepaths.NewAnyDepthPatterns] drops them from matching anyway.
//
// It keeps every other pattern. A pattern with brace alternatives, such
// as "*.{toml,yaml}", counts when any of its alternatives can match a
// YAML file. It reads an exclusion without its leading "!", as
// [filepaths.NewAnyDepthPatterns] does, so the "!" cannot push the braces
// of an exclusion past the limit.
//
// The matcher reads an extglob group literally, so a pattern with one
// matches only a file named after the text of the group. With those
// patterns dropped, the entry count includes only the entries that can
// match a file that people write.
func filterSupportedPatterns(patterns []string) []string {
	var result []string

	for _, pattern := range patterns {
		if extglobRE.MatchString(pattern) {
			continue
		}

		alts, err := filepaths.ExpandBraces(strings.TrimPrefix(pattern, "!"))
		if err != nil {
			continue
		}

		if slices.ContainsFunc(alts, canMatchYAML) {
			result = append(result, pattern)
		}
	}

	return result
}

// canMatchYAML reports whether pattern can match a YAML or JSON file, in
// any letter case. It reports false only when the last segment ends in
// the literal extension of a format that is not YAML, such as ".toml". It
// reports true for a wildcard extension, as in "azure-pipelines*.y*ml",
// and for a name without an extension, such as ".clang-format". It also
// reports true for the extension of another YAML format, such as
// "CITATION.cff".
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
