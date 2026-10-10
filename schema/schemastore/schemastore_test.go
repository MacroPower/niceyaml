package schemastore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

// catalogURL is the URL the stores of these tests fetch their catalog from.
const catalogURL = "https://example.com/catalog.json"

// The URLs of the schemas that testCatalog and workflowCatalog name.
const (
	testSchemaURL     = "https://example.com/test.json"
	workflowSchemaURL = "https://example.com/github-workflow.json"
)

// The catalogs that tests share.
var (
	// A one-entry catalog whose pattern matches every YAML file.
	testCatalog = schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       testSchemaURL,
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	// A one-entry catalog whose pattern matches the YAML files of a
	// GitHub workflows directory.
	workflowCatalog = schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "GitHub Workflow",
				URL:       workflowSchemaURL,
				FileMatch: []string{".github/workflows/*.yaml"},
			},
		},
	}

	// A catalog whose first entry matches every YAML file and whose second
	// matches one file name.
	layeredCatalog = schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{Name: "Generic", URL: "https://example.com/generic.json", FileMatch: []string{"*.yaml"}},
			{Name: "Specific", URL: "https://example.com/specific.json", FileMatch: []string{"myapp.yaml"}},
		},
	}
)

func TestSchemaStore_FindMatch(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "GitHub Workflow",
				URL:       "https://json.schemastore.org/github-workflow.json",
				FileMatch: []string{".github/workflows/*.yml", ".github/workflows/*.yaml"},
			},
			{
				Name:      "Dependabot",
				URL:       "https://json.schemastore.org/dependabot-2.0.json",
				FileMatch: []string{".github/dependabot.yml", ".github/dependabot.yaml"},
			},
			{
				Name:      "JSON Schema Draft 7",
				URL:       "https://json.schemastore.org/schema.json",
				FileMatch: []string{"*.json"}, // JSON only, no YAML.
			},
			{
				Name:      "No file match",
				URL:       "https://json.schemastore.org/no-match.json",
				FileMatch: nil,
			},
			{
				Name:      "CircleCI",
				URL:       "https://json.schemastore.org/circleciconfig.json",
				FileMatch: []string{"**/.circleci/config.{yml,yaml}"},
			},
			{
				Name:      "Not YAML",
				URL:       "https://json.schemastore.org/not-yaml.json",
				FileMatch: []string{"*.{toml,ini}"},
			},
			{
				Name:      "Issue Template",
				URL:       "https://json.schemastore.org/github-issue-forms.json",
				FileMatch: []string{"**/.github/ISSUE_TEMPLATE/!(config).yml"}, // Extglob only.
			},
			{
				Name:      "Mixed Patterns",
				URL:       "https://json.schemastore.org/mixed.json",
				FileMatch: []string{"**/mixed/!(config).yml", "**/mixed/*.yaml"},
			},
			{
				Name:      "Compose Overrides",
				URL:       "https://json.schemastore.org/compose-overrides.json",
				FileMatch: []string{"**/compose/*.yml", "!docker-compose.yml"},
			},
			{
				Name:      "Local File",
				URL:       "file:///schemas/local.json", // The registry fetches only HTTP.
				FileMatch: []string{"**/local/*.yaml"},
			},
			{
				Name:      "Uppercase Scheme",
				URL:       "HTTPS://json.schemastore.org/upper.json",
				FileMatch: []string{"**/upper/*.yaml"},
			},
			{
				Name:      "No URL",
				URL:       "", // The registry has nothing to fetch.
				FileMatch: []string{"**/no-url/*.yaml"},
			},
		},
	}

	tcs := map[string]struct {
		filePath string
		want     string // Name of the matching entry, or "" for no match.
	}{
		"no match for an entry with a file URL": {filePath: "repo/local/config.yaml"},
		"no match for an entry without a URL":   {filePath: "repo/no-url/config.yaml"},
		"matches an entry with an uppercase HTTPS scheme": {
			filePath: "repo/upper/config.yaml",
			want:     "Uppercase Scheme",
		},
		"matches brace alternative yml": {
			filePath: ".circleci/config.yml",
			want:     "CircleCI",
		},
		"matches brace alternative yaml": {
			filePath: "repo/.circleci/config.yaml",
			want:     "CircleCI",
		},
		"no match for brace alternatives without a yaml extension": {filePath: "settings.toml"},
		"matches github workflow yaml": {
			filePath: ".github/workflows/ci.yaml",
			want:     "GitHub Workflow",
		},
		"matches github workflow yml": {
			filePath: ".github/workflows/build.yml",
			want:     "GitHub Workflow",
		},
		"matches github workflow under an absolute repo path": {
			filePath: "/home/user/repo/.github/workflows/ci.yaml",
			want:     "GitHub Workflow",
		},
		"matches dependabot yaml": {
			filePath: ".github/dependabot.yaml",
			want:     "Dependabot",
		},
		"matches dependabot yml": {
			filePath: ".github/dependabot.yml",
			want:     "Dependabot",
		},
		"matches json file": {
			filePath: "schema.json",
			want:     "JSON Schema Draft 7",
		},
		"no match for an extglob pattern": {
			// The matcher implements no extglob, so the store drops the
			// pattern rather than keeping it as something only a literally
			// named file could match.
			filePath: ".github/ISSUE_TEMPLATE/bug.yml",
		},
		"no match for the literal spelling of an extglob pattern": {
			filePath: ".github/ISSUE_TEMPLATE/!(config).yml",
		},
		"matches the supported pattern beside an extglob one": {
			filePath: "repo/mixed/config.yaml",
			want:     "Mixed Patterns",
		},
		"matches a pattern beside an exclusion": {
			filePath: "repo/compose/override.yml",
			want:     "Compose Overrides",
		},
		"no match for an excluded file": {filePath: "repo/compose/docker-compose.yml"},
		"no match for unrelated file":   {filePath: "config.yaml"},
		"empty file path":               {filePath: ""},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newCatalogStore(t, catalog)

			entry, err := store.FindMatch(t.Context(), tc.filePath)
			if tc.want == "" {
				require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
				require.ErrorIs(t, err, schema.ErrNoMatch)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, entry.Name)
		})
	}
}

func TestSchemaStore_FindMatchPrefersSpecificPattern(t *testing.T) {
	t.Parallel()

	// Each case lists a broad entry before a specific one that matches the
	// same path, as the SchemaStore catalog does.
	tcs := map[string]struct {
		path    string
		entries []schemastore.CatalogEntry
		want    string
	}{
		"winget installer manifest": {
			path: "/repo/manifests/m/Microsoft/Foo/1.0/Microsoft.Foo.installer.yaml",
			entries: []schemastore.CatalogEntry{
				{Name: "Singleton", FileMatch: []string{"**/manifests/?/*/*/*/*.*.yaml"}},
				{Name: "Installer", FileMatch: []string{"**/manifests/?/*/*/*/*.*.installer.yaml"}},
			},
			want: "Installer",
		},
		"moon tasks": {
			path: "/repo/.moon/tasks/node.yml",
			entries: []schemastore.CatalogEntry{
				{Name: "Ansible", FileMatch: []string{"**/tasks/*.yml", "**/handlers/*.yml"}},
				{Name: "moon", FileMatch: []string{"**/.moon/tasks/**/*.yml"}},
			},
			want: "moon",
		},
		"mason package": {
			path: "/repo/packages/foo/package.yaml",
			entries: []schemastore.CatalogEntry{
				{Name: "hpack", FileMatch: []string{"package.yaml"}},
				{Name: "Mason", FileMatch: []string{"**/packages/*/package.yaml"}},
			},
			want: "Mason",
		},
		"vespertide migration": {
			path: "/repo/migrations/x.vespertide.yml",
			entries: []schemastore.CatalogEntry{
				{Name: "Drupal", FileMatch: []string{"*.migration.*.yml", "**/migrations/*.yml"}},
				{Name: "Vespertide", FileMatch: []string{"**/migrations/**/*.vespertide.yml"}},
			},
			want: "Vespertide",
		},
		"non-ASCII pattern counts characters": {
			path: "/repo/abcd/日本語/x.yml",
			entries: []schemastore.CatalogEntry{
				{Name: "Japanese", FileMatch: []string{"日本語/*.yml"}},
				{Name: "ASCII", FileMatch: []string{"abcd/*/*.yml"}},
			},
			want: "ASCII",
		},
		"broad entry when the specific one does not match": {
			path: "/repo/tasks/main.yml",
			entries: []schemastore.CatalogEntry{
				{Name: "Ansible", FileMatch: []string{"**/tasks/*.yml"}},
				{Name: "moon", FileMatch: []string{"**/.moon/tasks/**/*.yml"}},
			},
			want: "Ansible",
		},
		"catalog order breaks a tie": {
			path: "/repo/config.yaml",
			entries: []schemastore.CatalogEntry{
				{Name: "First", FileMatch: []string{"*.yaml"}},
				{Name: "Second", FileMatch: []string{"**/*.yaml"}},
			},
			want: "First",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := schemastore.Catalog{}
			for i, entry := range tc.entries {
				entry.URL = fmt.Sprintf("https://example.com/schema-%d.json", i)
				catalog.Schemas = append(catalog.Schemas, entry)
			}

			store := newCatalogStore(t, catalog)

			entry, err := store.FindMatch(t.Context(), tc.path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, entry.Name)
		})
	}
}

func TestSchemaStore_FindMatchDoesNotAliasCatalog(t *testing.T) {
	t.Parallel()

	// The returned entry must not share its pattern slice with the cached
	// catalog, or a caller writing to it changes which entry matches next.
	store := newCatalogStore(t, layeredCatalog)

	entry, err := store.FindMatch(t.Context(), "/x/other.yaml")
	require.NoError(t, err)
	// The entry equals the catalog's, so no state the store keeps for
	// matching reaches the caller.
	require.Equal(t, layeredCatalog.Schemas[0], entry)

	entry.FileMatch[0] = "*.json"

	entry, err = store.FindMatch(t.Context(), "/x/other.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Generic", entry.Name)
	assert.Equal(t, []string{"*.yaml"}, entry.FileMatch)
}

func TestSchemaStore_FindMatchRepeatedPath(t *testing.T) {
	t.Parallel()

	// A registry looks up a file's path once per document, so each lookup
	// must give what a first lookup of its path gives, whatever lookups
	// came before it.
	wd, err := os.Getwd()
	require.NoError(t, err)

	tcs := map[string]struct {
		paths []string
		want  []string // Name of the matching entry, or "" for no match.
	}{
		"same path": {
			paths: []string{"/x/myapp.yaml", "/x/myapp.yaml", "/x/myapp.yaml"},
			want:  []string{"Specific", "Specific", "Specific"},
		},
		"alternating paths": {
			paths: []string{"/x/myapp.yaml", "/x/other.yaml", "/x/myapp.yaml", "/x/other.yaml"},
			want:  []string{"Specific", "Generic", "Specific", "Generic"},
		},
		"unmatched path": {
			paths: []string{"/x/other.json", "/x/other.json", "/x/myapp.yaml"},
			want:  []string{"", "", "Specific"},
		},
		"relative and absolute spellings of one path": {
			// Each error names the path as its caller spelled it.
			paths: []string{"other.json", filepath.Join(wd, "other.json"), "other.json"},
			want:  []string{"", "", ""},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newCatalogStore(t, layeredCatalog)

			for i, path := range tc.paths {
				entry, err := store.FindMatch(t.Context(), path)

				if tc.want[i] == "" {
					require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch, "lookup %d", i)
					require.ErrorContains(t, err, fmt.Sprintf("%q", path), "lookup %d", i)

					continue
				}

				require.NoError(t, err, "lookup %d", i)
				assert.Equal(t, tc.want[i], entry.Name, "lookup %d", i)
			}
		})
	}
}

func TestSchemaStore_FindMatchAfterRefresh(t *testing.T) {
	t.Parallel()

	// A TTL of 0 turns caching off, so each lookup fetches a new catalog,
	// whose entry takes its name from the request count. A lookup matches
	// against the catalog it fetched, not the one an earlier lookup of the
	// same path read.
	client, requests, _ := newHeldCatalogClient(t, 3)

	store := newStore(client, schemastore.WithCacheTTL(0))

	for _, want := range []string{"Catalog 1", "Catalog 2", "Catalog 3"} {
		entry, err := store.FindMatch(t.Context(), "/x/config.yaml")
		require.NoError(t, err)
		assert.Equal(t, want, entry.Name)
	}

	assert.Equal(t, int32(3), requests.Load())
}

func BenchmarkStore_FindMatch(b *testing.B) {
	// A path no entry matches makes a scan try every pattern, as a document
	// outside the catalog does.
	catalog := schemastore.Catalog{}
	for i := range 1000 {
		catalog.Schemas = append(catalog.Schemas, schemastore.CatalogEntry{
			Name: fmt.Sprintf("Schema %d", i),
			URL:  fmt.Sprintf("https://example.com/schema-%d.json", i),
			FileMatch: []string{
				fmt.Sprintf("schema-%d.yaml", i),
				fmt.Sprintf("/.config/schema-%d/*.{yml,yaml}", i),
			},
		})
	}

	store := newCatalogStore(b, catalog)

	_, err := store.FindMatch(b.Context(), "/repo/unmatched.yaml")
	require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)

	bms := map[string]struct {
		paths []string
	}{
		// Each lookup names a different path from the one before, so each
		// one scans the catalog.
		"new path": {
			paths: []string{"/repo/unmatched.yaml", "/repo/other.yaml"},
		},
		// Every lookup names the same path, as the documents of one file
		// do, so each one reuses the last scan.
		"repeated path": {
			paths: []string{"/repo/unmatched.yaml"},
		},
	}

	for name, bm := range bms {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			var i int

			for b.Loop() {
				_, err = store.FindMatch(b.Context(), bm.paths[i%len(bm.paths)])
				i++
			}

			require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)
		})
	}
}

func TestSchemaStore_LazyLoading(t *testing.T) {
	t.Parallel()

	// A fetch the store starts on a goroutine of its own can reach the
	// transport after the call that started it returns. Inside the bubble,
	// synctest.Wait lets every such fetch reach the transport before the
	// test reads the count.
	synctest.Test(t, func(t *testing.T) {
		client, fetchCount := yamltest.CountingClient(respondCatalog(t, testCatalog))

		// New performs no I/O.
		store := newStore(client)

		synctest.Wait()
		assert.Equal(t, int32(0), fetchCount.Load())

		// First lookup fetches.
		entry, err := store.FindMatch(t.Context(), "config.yaml")
		require.NoError(t, err)
		assert.NotEmpty(t, entry.Name)

		synctest.Wait()
		assert.Equal(t, int32(1), fetchCount.Load())

		// Second lookup uses cache.
		entry, err = store.FindMatch(t.Context(), "other.yaml")
		require.NoError(t, err)
		assert.NotEmpty(t, entry.Name)

		synctest.Wait()
		assert.Equal(t, int32(1), fetchCount.Load())
	})
}

func TestSchemaStore_CacheTTL(t *testing.T) {
	t.Parallel()

	client, fetchCount := yamltest.CountingClient(respondCatalog(t, testCatalog))

	// Use a very short TTL for testing.
	store := newStore(client, schemastore.WithCacheTTL(10*time.Millisecond))

	// First lookup fetches.
	requireEntry(t, store, "Test")
	assert.Equal(t, int32(1), fetchCount.Load())

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// Lookup triggers refetch due to expired cache.
	requireEntry(t, store, "Test")
	assert.Equal(t, int32(2), fetchCount.Load())
}

func TestSchemaStore_ConcurrentFirstFetch(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Concurrent lookups before the catalog has loaded share one fetch.
		const goroutines = 10

		// The transport holds the fetch open until every caller waits on
		// it.
		client, fetchCount, release := newHeldCatalogClient(t, 0)

		store := newStore(client)

		entries := make([]schemastore.CatalogEntry, goroutines)
		errs := make([]error, goroutines)

		var wg sync.WaitGroup

		for i := range goroutines {
			wg.Go(func() {
				entries[i], errs[i] = store.FindMatch(t.Context(), "config.yaml")
			})
		}

		// Wait until every caller is waiting on the one fetch in flight.
		synctest.Wait()
		release()
		wg.Wait()

		assert.Equal(t, int32(1), fetchCount.Load(), "concurrent lookups should share one fetch")

		for i := range goroutines {
			require.NoError(t, errs[i])
			assert.Equal(t, "Catalog 1", entries[i].Name)
		}
	})
}

func TestSchemaStore_SlowFetch(t *testing.T) {
	t.Parallel()

	// Each subtest runs in a synctest bubble, whose clock stands still until
	// every goroutine in it waits. A lookup therefore reaches the store before
	// its own deadline, and synctest.Wait marks the moment a held fetch has
	// reached the transport.

	t.Run("serves the previous catalog during a refresh", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, requests, release := newHeldCatalogClient(t, 1)

			store := newStore(client, schemastore.WithCacheTTL(10*time.Millisecond))

			requireEntry(t, store, "Catalog 1")

			// Let the cache expire.
			time.Sleep(20 * time.Millisecond)

			// This lookup starts the refresh, which the transport holds.
			refreshing := findMatchAsync(t.Context(), store)

			synctest.Wait()
			require.Equal(t, int32(2), requests.Load())

			// A lookup during the refresh gets the previous catalog at once.
			result := receive(t, findMatchAsync(t.Context(), store))
			require.NoError(t, result.err)
			assert.Equal(t, "Catalog 1", result.entry.Name)

			// The lookup that started the refresh waits for it.
			release()

			result = receive(t, refreshing)
			require.NoError(t, result.err)
			assert.Equal(t, "Catalog 2", result.entry.Name)
			assert.Equal(t, int32(2), requests.Load())
		})
	})

	t.Run("stops waiting for the first fetch when the context ends", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, requests, release := newHeldCatalogClient(t, 0)

			store := newStore(client)

			// This lookup starts the first fetch, which the transport holds.
			first := findMatchAsync(t.Context(), store)

			synctest.Wait()
			require.Equal(t, int32(1), requests.Load())

			// With no catalog to fall back on, a second lookup waits for the
			// fetch until its own deadline and no longer.
			const timeout = 50 * time.Millisecond

			start := time.Now()

			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()

			result := receive(t, findMatchAsync(ctx, store))
			require.ErrorIs(t, result.err, schemastore.ErrFetchCatalog)
			require.ErrorIs(t, result.err, context.DeadlineExceeded)
			assert.Equal(t, timeout, time.Since(start), "lookup should wait for the fetch until its deadline")

			release()

			result = receive(t, first)
			require.NoError(t, result.err)
			assert.Equal(t, "Catalog 1", result.entry.Name)
			assert.Equal(t, int32(1), requests.Load())
		})
	})

	t.Run("finishes a fetch after its lookup stops waiting", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, requests, release := newHeldCatalogClient(t, 0)

			store := newStore(client)

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			abandoned := findMatchAsync(ctx, store)

			// The lookup starts the fetch before its deadline, and the
			// transport holds it.
			synctest.Wait()
			require.Equal(t, int32(1), requests.Load())

			result := receive(t, abandoned)
			require.ErrorIs(t, result.err, schemastore.ErrFetchCatalog)
			require.ErrorIs(t, result.err, context.DeadlineExceeded)

			// The fetch outlives the lookup that started it and records its
			// catalog, so the next lookup gets that catalog rather than
			// sending a second request.
			release()
			synctest.Wait()

			requireEntry(t, store, "Catalog 1")
			assert.Equal(t, int32(1), requests.Load())
		})
	})

	t.Run("ends a held fetch at the refresh timeout", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, requests, _ := newHeldCatalogClient(t, 0)

			// The timeout stays below the five seconds receive waits, which
			// the default would exceed.
			const timeout = 50 * time.Millisecond

			store := newStore(client, schemastore.WithRefreshTimeout(timeout))

			start := time.Now()

			// The lookup context has no deadline, so only the refresh timeout
			// can end the fetch the transport holds.
			result := receive(t, findMatchAsync(t.Context(), store))
			require.ErrorIs(t, result.err, schemastore.ErrFetchCatalog)
			require.ErrorIs(t, result.err, context.DeadlineExceeded)
			assert.Equal(t, timeout, time.Since(start), "fetch should end at the refresh timeout")
			assert.Equal(t, int32(1), requests.Load())
		})
	})
}

func TestSchemaStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "GitHub Workflow",
				URL:       "https://json.schemastore.org/github-workflow.json",
				FileMatch: []string{".github/workflows/*.yaml"},
			},
			{
				Name:      "Docker Compose",
				URL:       "https://json.schemastore.org/docker-compose.json",
				FileMatch: []string{"docker-compose.yaml", "docker-compose.yml"},
			},
			{
				Name:      "Generic",
				URL:       "https://example.com/generic.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	// Use short TTL to trigger concurrent cache refreshes.
	store := newCatalogStore(t, catalog, schemastore.WithCacheTTL(1*time.Millisecond))

	// Run multiple goroutines calling FindMatch concurrently.
	const numGoroutines = 10

	const numIterations = 100

	filePaths := []string{
		".github/workflows/ci.yaml",
		"docker-compose.yaml",
		"config.yaml",
		"random.yaml",
		"",
	}

	var wg sync.WaitGroup

	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()

			for range numIterations {
				for _, path := range filePaths {
					_, _ = store.FindMatch(t.Context(), path) //nolint:errcheck // Only the race matters here.
				}
			}
		}()
	}

	wg.Wait()
}

func TestSchemaStore_Filter(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "GitHub Workflow",
				URL:       "https://json.schemastore.org/github-workflow.json",
				FileMatch: []string{".github/workflows/*.yaml"},
			},
			{
				Name:      "Docker Compose",
				URL:       "https://json.schemastore.org/docker-compose.json",
				FileMatch: []string{"docker-compose.yaml", "docker-compose.yml"},
			},
		},
	}

	// Filter to only GitHub schemas.
	store := newCatalogStore(t, catalog, schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
		return strings.Contains(strings.ToLower(e.Name), "github")
	}))

	// GitHub workflow should match.
	entry, err := store.FindMatch(t.Context(), ".github/workflows/ci.yaml")
	require.NoError(t, err)
	assert.Equal(t, "GitHub Workflow", entry.Name)

	// Docker Compose should not match due to filter.
	_, err = store.FindMatch(t.Context(), "docker-compose.yaml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
}

func TestSchemaStore_FetchPanicReachesCaller(t *testing.T) {
	t.Parallel()

	// The fetch runs on a goroutine the store starts, so a panic during it
	// must reach the lookup that waits for the fetch rather than end the
	// process.
	tcs := map[string]struct {
		transport yamltest.RoundTripFunc
		filter    func(schemastore.CatalogEntry) bool
		want      any
	}{
		"filter": {
			transport: respondCatalog(t, testCatalog),
			filter: func(schemastore.CatalogEntry) bool {
				panic("filter bug")
			},
			want: "filter bug",
		},
		"transport": {
			transport: panicTransport,
			want:      "transport bug",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, fetchCount := yamltest.CountingClient(tc.transport)
			store := newStore(client, schemastore.WithFilter(tc.filter))

			assert.PanicsWithValue(t, tc.want, func() {
				_, err := store.FindMatch(t.Context(), "config.yaml")
				require.NoError(t, err)
			})

			// The panicked fetch counts as a failed one, so the next lookup
			// reports it within the retry interval and does not fetch again.
			res := receive(t, findMatchAsync(t.Context(), store))
			require.ErrorIs(t, res.err, schemastore.ErrFetchCatalog)
			assert.Equal(t, int32(1), fetchCount.Load())
		})
	}
}

func TestSchemaStore_FetchGoexitReachesCaller(t *testing.T) {
	t.Parallel()

	// A fetch that calls runtime.Goexit, as t.FailNow does, must end the
	// goroutine of the lookup that waits for it the same way, rather than
	// leave every lookup waiting on a fetch that never finishes.
	tcs := map[string]struct {
		transport yamltest.RoundTripFunc
		filter    func(schemastore.CatalogEntry) bool
	}{
		"filter": {
			transport: respondCatalog(t, testCatalog),
			filter: func(schemastore.CatalogEntry) bool {
				runtime.Goexit()

				return true
			},
		},
		"transport": {
			transport: goexitTransport,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, fetchCount := yamltest.CountingClient(tc.transport)
			store := newStore(client, schemastore.WithFilter(tc.filter))

			var returned bool

			done := make(chan struct{})

			go func() {
				defer close(done)

				//nolint:errcheck // The call exits through runtime.Goexit.
				_, _ = store.FindMatch(t.Context(), "config.yaml")
				returned = true
			}()

			// A lookup that hangs on the fetch never closes done.
			receive(t, done)
			assert.False(t, returned)

			// The fetch that called runtime.Goexit counts as a failed one, so
			// the next lookup reports it within the retry interval and does
			// not fetch again.
			res := receive(t, findMatchAsync(t.Context(), store))
			require.ErrorIs(t, res.err, schemastore.ErrFetchCatalog)
			require.NotErrorIs(t, res.err, schema.ErrNoMatch)
			assert.Equal(t, int32(1), fetchCount.Load())
		})
	}
}

func TestSchemaStore_RecordedFetchPanicLoadsAsError(t *testing.T) {
	t.Parallel()

	// After a fetch panics or calls runtime.Goexit, lookups within the
	// retry interval report ErrFetchCatalog. A registry load that returns
	// that error must fail with it rather than raise the fetch's panic or
	// Goexit again in a caller that never waited on the fetch.
	tcs := map[string]struct {
		transport yamltest.RoundTripFunc
	}{
		"panic":  {transport: panicTransport},
		"goexit": {transport: goexitTransport},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newStore(tc.transport.Client())

			// The first lookup waits on the fetch and raises its panic or
			// Goexit, so it runs on a goroutine of its own.
			first := make(chan struct{})

			go func() {
				defer close(first)
				//nolint:errcheck // The panic only signals the end of the fetch.
				defer func() { _ = recover() }()

				//nolint:errcheck // The call panics or exits through runtime.Goexit.
				_, _ = store.FindMatch(t.Context(), "config.yaml")
			}()

			receive(t, first)

			reg := schema.NewRegistry()
			ref := schema.Loadable("catalog.json", func(ctx context.Context) ([]byte, error) {
				_, err := store.FindMatch(ctx, "config.yaml")

				return nil, fmt.Errorf("find catalog match: %w", err)
			})

			// A Schema that raises the failure again must not take the test
			// goroutine with it, so it runs on a goroutine of its own too.
			type outcome struct {
				err       error
				recovered any
				returned  bool
			}

			results := make(chan outcome, 1)

			go func() {
				var out outcome

				defer func() {
					out.recovered = recover()
					results <- out
				}()

				_, out.err = reg.Schema(t.Context(), ref)
				out.returned = true
			}()

			out := receive(t, results)
			require.Nil(t, out.recovered)
			require.True(t, out.returned, "Schema exited through runtime.Goexit")
			require.ErrorIs(t, out.err, schema.ErrLoad)
			require.ErrorIs(t, out.err, schemastore.ErrFetchCatalog)
		})
	}
}

func TestSchemaStore_ContextEndsAfterFetch(t *testing.T) {
	t.Parallel()

	// The lookup's context ends only after the fetch has finished, so both
	// cases of the lookup's wait are ready at once. The fetch's outcome must
	// count every time.
	tcs := map[string]struct {
		client    *http.Client
		wantPanic any
		opts      []schemastore.Option
		wantErr   bool
	}{
		"fetch succeeds": {
			client: respondCatalog(t, testCatalog).Client(),
		},
		"fetch fails": {
			client:  yamltest.Respond(http.StatusInternalServerError, "").Client(),
			wantErr: true,
		},
		"filter panics": {
			client: respondCatalog(t, testCatalog).Client(),
			opts: []schemastore.Option{
				schemastore.WithFilter(func(schemastore.CatalogEntry) bool {
					panic("filter bug")
				}),
			},
			wantPanic: "filter bug",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Go picks among ready select cases at random, so repeat the
			// lookup to catch an outcome that depends on the pick.
			for range 32 {
				store := newStore(tc.client, tc.opts...)

				ctx := &endsAfterFetchContext{
					Context: t.Context(),
					wait: func() {
						// A second lookup waits on the same fetch, so the fetch
						// has finished once the lookup returns or panics.
						//nolint:errcheck // The panic only signals the end of the fetch.
						defer func() { _ = recover() }()

						//nolint:errcheck // Only the wait matters here.
						_, _ = store.FindMatch(t.Context(), "config.yaml")
					},
				}

				if tc.wantPanic != nil {
					assert.PanicsWithValue(t, tc.wantPanic, func() {
						//nolint:errcheck // The call panics.
						_, _ = store.FindMatch(ctx, "config.yaml")
					})

					continue
				}

				entry, err := store.FindMatch(ctx, "config.yaml")
				if tc.wantErr {
					require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
					require.NotErrorIs(t, err, context.Canceled)

					continue
				}

				require.NoError(t, err)
				assert.Equal(t, "Test", entry.Name)
			}
		})
	}
}

// endsAfterFetchContext is a context that ends once a lookup waits on it.
// Err reports nil until then, so the lookup starts a fetch. Done calls wait,
// which returns once that fetch has finished, and then reports the context
// as canceled.
type endsAfterFetchContext struct {
	context.Context

	wait  func()
	ended atomic.Bool
}

func (c *endsAfterFetchContext) Done() <-chan struct{} {
	c.wait()
	c.ended.Store(true)

	done := make(chan struct{})
	close(done)

	return done
}

func (c *endsAfterFetchContext) Err() error {
	if c.ended.Load() {
		return context.Canceled
	}

	return nil
}

func TestSchemaStore_HTTPClient(t *testing.T) {
	t.Parallel()

	// The catalog URL names a host that never resolves, so only the
	// configured client can answer it.
	client, fetchCount := yamltest.CountingClient(respondCatalog(t, testCatalog))

	store := schemastore.New(
		schemastore.WithCatalogURL("https://catalog.invalid/catalog.json"),
		schemastore.WithHTTPClient(client),
	)

	requireEntry(t, store, "Test")
	assert.Equal(t, int32(1), fetchCount.Load())
}

func TestSchemaStore_ZeroRefreshTimeout(t *testing.T) {
	t.Parallel()

	// A zero timeout keeps the default rather than expiring every fetch
	// before it starts.
	store := newCatalogStore(t, testCatalog, schemastore.WithRefreshTimeout(0))

	requireEntry(t, store, "Test")
}

func TestSchemaStore_NilHTTPClient(t *testing.T) {
	t.Parallel()

	// A nil client keeps the default rather than panicking on the first
	// fetch. The default client needs a real server, so this is the one
	// test in the package that starts one. See respondCatalog for why the
	// other tests fetch from memory.
	data := marshalCatalog(t, testCatalog)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		//nolint:errcheck // Test helper.
		w.Write([]byte(data))
	}))
	t.Cleanup(server.Close)

	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithHTTPClient(nil),
	)

	requireEntry(t, store, "Test")
}

func TestSchemaStore_FetchError(t *testing.T) {
	t.Parallel()

	// With no catalog ever loaded, a failed fetch surfaces from the lookup.
	tcs := map[string]*schemastore.Store{
		"server error": newStore(yamltest.Respond(http.StatusInternalServerError, "").Client()),
		"invalid json": newStore(yamltest.Respond(http.StatusOK, "not json").Client()),

		// A JSON body with no schemas array is not a catalog, so the store
		// treats it as a failed fetch rather than as a catalog with no
		// entries.
		"null":         newStore(yamltest.Respond(http.StatusOK, `null`).Client()),
		"empty object": newStore(yamltest.Respond(http.StatusOK, `{}`).Client()),
		"null schemas": newStore(yamltest.Respond(http.StatusOK, `{"schemas": null}`).Client()),
		"error object": newStore(yamltest.Respond(http.StatusOK, `{"message": "rate limited"}`).Client()),

		// The control character makes url.Parse reject the URL, so the
		// fetch sends no request.
		"invalid URL": schemastore.New(schemastore.WithCatalogURL("http://\x00invalid")),

		// Port 1 is a privileged port that won't be listening.
		"connection refused": schemastore.New(schemastore.WithCatalogURL("http://localhost:1")),

		"read body error": newStore(yamltest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(iotest.ErrReader(errors.New("read error"))),
			}, nil
		}).Client()),
	}

	for name, store := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := store.FindMatch(t.Context(), "config.yaml")
			require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			require.NotErrorIs(t, err, schema.ErrNoMatch)

			doc := yamltest.FirstDocumentWithPath(t, `key: value`, "config.yaml")
			_, err = store.Resolve(t.Context(), doc)
			require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			require.NotErrorIs(t, err, schema.ErrNoMatch)
		})
	}
}

func TestSchemaStore_RetryAfter(t *testing.T) {
	t.Parallel()

	// The bubble's clock stands still while goroutines run, so the second
	// lookup lands inside the retry interval however slowly the first one
	// returns.
	synctest.Test(t, func(t *testing.T) {
		client, fetchCount := yamltest.CountingClient(yamltest.Respond(http.StatusInternalServerError, ""))

		store := newStore(client, schemastore.WithRetryAfter(20*time.Millisecond))

		// The first lookup fetches and fails.
		_, err := store.FindMatch(t.Context(), "config.yaml")
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		assert.Equal(t, int32(1), fetchCount.Load())

		// A lookup inside the retry interval reports the same failure
		// without contacting the server again.
		_, err = store.FindMatch(t.Context(), "config.yaml")
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		assert.Equal(t, int32(1), fetchCount.Load())

		// Once the interval passes, the next lookup tries again.
		time.Sleep(30 * time.Millisecond)

		_, err = store.FindMatch(t.Context(), "config.yaml")
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		assert.Equal(t, int32(2), fetchCount.Load())
	})
}

func TestSchemaStore_NonPositiveRetryAfter(t *testing.T) {
	t.Parallel()

	tcs := map[string]time.Duration{
		"zero":     0,
		"negative": -time.Second,
	}

	for name, interval := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, fetchCount := yamltest.CountingClient(yamltest.Respond(http.StatusInternalServerError, ""))

			// A non-positive interval keeps the default rather than
			// retrying on every lookup, which costs one refresh timeout
			// each.
			store := newStore(client, schemastore.WithRetryAfter(interval))

			for range 5 {
				_, err := store.FindMatch(t.Context(), "config.yaml")
				require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			}

			assert.Equal(t, int32(1), fetchCount.Load())
		})
	}
}

func TestSchemaStore_EmptyCatalog(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{},
	}

	store := newCatalogStore(t, catalog)

	// No schemas in catalog means no matches.
	_, err := store.FindMatch(t.Context(), "config.yaml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
}

func TestSchemaStore_CanceledContext(t *testing.T) {
	t.Parallel()

	// Each subtest runs in a synctest bubble. A lookup with a canceled
	// context returns at once, so a fetch it wrongly started could still
	// be on its way to the transport. Calling synctest.Wait lets that
	// fetch reach the transport before the test reads the count.

	t.Run("uses cached data without refreshing", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, fetchCount := yamltest.CountingClient(respondCatalog(t, testCatalog))

			// Use short TTL so cache expires quickly.
			store := newStore(client, schemastore.WithCacheTTL(10*time.Millisecond))

			requireEntry(t, store, "Test")

			// Wait for cache to expire.
			time.Sleep(20 * time.Millisecond)

			// Create a canceled context.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			// FindMatch skips the refresh with a canceled context but still
			// returns cached data.
			entry, err := store.FindMatch(ctx, "config.yaml")
			require.NoError(t, err)
			assert.Equal(t, "Test", entry.Name)

			synctest.Wait()
			assert.Equal(t, int32(1), fetchCount.Load())
		})
	})

	t.Run("reports the cancellation without cached data", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			client, fetchCount := yamltest.CountingClient(respondCatalog(t, testCatalog))

			store := newStore(client)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := store.FindMatch(ctx, "config.yaml")
			require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			require.ErrorIs(t, err, context.Canceled)

			synctest.Wait()
			assert.Equal(t, int32(0), fetchCount.Load())

			// A cancellation is not a failed attempt, so the next lookup
			// fetches.
			requireEntry(t, store, "Test")

			synctest.Wait()
			assert.Equal(t, int32(1), fetchCount.Load())
		})
	})
}

func TestSchemaStore_RefetchFails(t *testing.T) {
	t.Parallel()

	// A refresh that fails leaves the previous catalog in use.
	tcs := map[string]struct {
		body   string
		status int
	}{
		"server error": {status: http.StatusInternalServerError},

		// A JSON body with no schemas array is not a catalog, so the store
		// keeps the previous catalog rather than replacing it with one
		// that has no entries.
		"null":         {status: http.StatusOK, body: `null`},
		"empty object": {status: http.StatusOK, body: `{}`},
		"null schemas": {status: http.StatusOK, body: `{"schemas": null}`},
		"error object": {status: http.StatusOK, body: `{"message": "rate limited"}`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Inside the bubble, the sleep below moves the fake clock past
			// the cache TTL without waiting in real time.
			synctest.Test(t, func(t *testing.T) {
				first, later := respondCatalog(t, testCatalog), yamltest.Respond(tc.status, tc.body)

				var requestCount atomic.Int32

				// The transport serves the catalog first and the response
				// of the case after. It opens no socket, so it counts only
				// the store's own requests.
				client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if requestCount.Add(1) > 1 {
						return later(r)
					}

					return first(r)
				}).Client()

				// Use short TTL so cache expires quickly.
				store := newStore(client, schemastore.WithCacheTTL(10*time.Millisecond))

				// First fetch succeeds.
				requireEntry(t, store, "Test")
				assert.Equal(t, int32(1), requestCount.Load())

				// Wait for cache to expire.
				time.Sleep(20 * time.Millisecond)

				// FindMatch triggers a refetch which fails, but still
				// returns cached data.
				requireEntry(t, store, "Test")

				// Verify the store attempted the refetch.
				assert.Equal(t, int32(2), requestCount.Load())

				// Inside the retry interval the store serves stale data
				// without a request.
				requireEntry(t, store, "Test")
				assert.Equal(t, int32(2), requestCount.Load())
			})
		})
	}
}

func TestSchemaStore_Resolve(t *testing.T) {
	t.Parallel()

	schemaData := `{"type": "object"}`

	t.Run("matches known pattern", func(t *testing.T) {
		t.Parallel()

		store := newCatalogStore(t, workflowCatalog)

		doc := yamltest.FirstDocumentWithPath(t, `on: push`, ".github/workflows/ci.yaml")
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, workflowSchemaURL, ref.Key())
	})

	t.Run("no match for unknown file", func(t *testing.T) {
		t.Parallel()

		store := newCatalogStore(t, workflowCatalog)

		doc := yamltest.FirstDocumentWithPath(t, `key: value`, "random.yaml")
		_, err := store.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorContains(t, err, "random.yaml")
	})

	t.Run("matches a relative path against the working directory", func(t *testing.T) {
		t.Parallel()

		// A pattern that names a directory matches a file read from inside
		// it, as yaml-language-server matches the absolute document path.
		// Go runs a test with the package directory as its working
		// directory, so the pattern names that directory.
		cwd, err := os.Getwd()
		require.NoError(t, err)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Local Workflow",
					URL:       "https://example.com/local.json",
					FileMatch: []string{filepath.Base(cwd) + "/*.yml"},
				},
			},
		}

		store := newCatalogStore(t, catalog)

		doc := yamltest.FirstDocumentWithPath(t, `on: push`, "ci.yml")
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/local.json", ref.Key())

		entry, err := store.FindMatch(t.Context(), "./ci.yml")
		require.NoError(t, err)
		assert.Equal(t, "Local Workflow", entry.Name)

		// The error names the path as the caller wrote it.
		_, err = store.FindMatch(t.Context(), "ci.yaml")
		require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
		require.ErrorContains(t, err, `"ci.yaml"`)
		assert.NotContains(t, err.Error(), cwd)
	})

	t.Run("loads matching schema", func(t *testing.T) {
		t.Parallel()

		client := newRoutingClient(map[string]string{
			catalogURL:    marshalCatalog(t, testCatalog),
			testSchemaURL: schemaData,
		})

		store := newStore(client)

		doc := yamltest.FirstDocumentWithPath(t, `key: value`, "config.yaml")

		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, testSchemaURL, ref.Key())

		data, err := schema.NewRegistry(schema.WithHTTPClient(client)).Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, schemaData, string(data))
	})

	t.Run("error when schema URL not found", func(t *testing.T) {
		t.Parallel()

		// The client serves the catalog alone, so the schema URL gets
		// status 404.
		client := newRoutingClient(map[string]string{
			catalogURL: marshalCatalog(t, testCatalog),
		})

		store := newStore(client)

		doc := yamltest.FirstDocumentWithPath(t, `key: value`, "config.yaml")

		// The path matches, so Resolve names the schema. Only the load fails,
		// and a failed load is not a no-match.
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, testSchemaURL, ref.Key())

		_, err = schema.NewRegistry(schema.WithHTTPClient(client)).Load(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "fetch "+testSchemaURL+": status 404")
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestIntegration(t *testing.T) {
	t.Parallel()

	schemaData := stringtest.Input(`
		{
			"type": "object",
			"properties": {
				"on": {"type": ["string", "object"]}
			},
			"required": ["on"]
		}
	`)

	// The check function validates a document through a registry whose
	// store serves workflowCatalog.
	check := func(t *testing.T, input, filePath string) error {
		t.Helper()

		client := newRoutingClient(map[string]string{
			catalogURL:        marshalCatalog(t, workflowCatalog),
			workflowSchemaURL: schemaData,
		})

		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithResolvers(newStore(client)),
		)

		return reg.Check(t.Context(), yamltest.FirstDocumentWithPath(t, input, filePath))
	}

	t.Run("validates matching document", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, check(t, `on: push`, ".github/workflows/ci.yaml"))
	})

	t.Run("rejects invalid document", func(t *testing.T) {
		t.Parallel()

		require.Error(t, check(t, `name: test`, ".github/workflows/ci.yaml"))
	})

	t.Run("returns ErrNoMatch for unmatched document", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, check(t, `key: value`, "random.yaml"), schema.ErrNoMatch)
	})

	t.Run("fetches each schema once", func(t *testing.T) {
		t.Parallel()

		catalogClient, catalogFetches := yamltest.CountingClient(respondCatalog(t, workflowCatalog))
		schemaClient, schemaFetches := yamltest.CountingClient(yamltest.Respond(http.StatusOK, schemaData))

		reg := schema.NewRegistry(
			schema.WithHTTPClient(schemaClient),
			schema.WithResolvers(newStore(catalogClient)),
		)

		for _, name := range []string{"ci.yaml", "release.yaml", "lint.yaml"} {
			doc := yamltest.FirstDocumentWithPath(t, `on: push`, ".github/workflows/"+name)

			err := reg.Check(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), catalogFetches.Load())
		assert.Equal(t, int32(1), schemaFetches.Load(), "the registry should fetch the schema once")
	})

	t.Run("reports an unreachable catalog and stops at the store", func(t *testing.T) {
		t.Parallel()

		var fallbackCalls atomic.Int32

		fallback := schema.ResolverFunc(func(context.Context, *niceyaml.Node) (schema.Ref, error) {
			fallbackCalls.Add(1)

			return schema.Ref{}, schema.ErrNoMatch
		})

		reg := schema.NewRegistry(
			schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL("http://localhost:1")), fallback),
		)

		doc := yamltest.FirstDocumentWithPath(t, `key: value`, "config.yaml")

		err := reg.Check(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
		assert.Zero(t, fallbackCalls.Load(), "the registry should not try resolvers after the store")
	})
}

// Helper functions.

// newStore creates a [*schemastore.Store] that fetches its catalog from
// catalogURL through client, with opts applied after.
func newStore(client *http.Client, opts ...schemastore.Option) *schemastore.Store {
	return schemastore.New(append([]schemastore.Option{
		schemastore.WithCatalogURL(catalogURL),
		schemastore.WithHTTPClient(client),
	}, opts...)...)
}

// newCatalogStore creates a [*schemastore.Store] whose client answers every
// URL with catalog as JSON, with opts applied after.
func newCatalogStore(tb testing.TB, catalog schemastore.Catalog, opts ...schemastore.Option) *schemastore.Store {
	tb.Helper()

	return newStore(respondCatalog(tb, catalog).Client(), opts...)
}

// respondCatalog returns a transport function that answers every URL
// with catalog as JSON.
//
// Tests in this package fetch through transport functions like this one
// rather than from test servers on http.DefaultTransport. Closing a test
// server closes the idle connections of http.DefaultTransport. That
// transport returns a connection to its idle pool just before it hands
// over a response with no body, so a request that another test has in
// flight can fail with "transport connection broken" instead of getting
// its response.
func respondCatalog(tb testing.TB, catalog schemastore.Catalog) yamltest.RoundTripFunc {
	tb.Helper()

	return yamltest.Respond(http.StatusOK, marshalCatalog(tb, catalog))
}

// panicTransport is a transport function that panics.
func panicTransport(*http.Request) (*http.Response, error) {
	panic("transport bug")
}

// goexitTransport is a transport function that calls runtime.Goexit, as
// t.FailNow does.
func goexitTransport(*http.Request) (*http.Response, error) {
	runtime.Goexit()

	panic("unreachable")
}

// newRoutingClient returns a client whose transport answers each URL in
// routes with its body and every other URL with status 404.
func newRoutingClient(routes map[string]string) *http.Client {
	return yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := routes[r.URL.String()]
		if !ok {
			return yamltest.Respond(http.StatusNotFound, "")(r)
		}

		return yamltest.Respond(http.StatusOK, body)(r)
	}).Client()
}

// marshalCatalog returns catalog as JSON.
func marshalCatalog(tb testing.TB, catalog schemastore.Catalog) string {
	tb.Helper()

	data, err := json.Marshal(catalog)
	require.NoError(tb, err)

	return string(data)
}

// newHeldCatalogClient returns a client whose transport answers every URL
// with a one-entry catalog whose pattern matches every YAML file. The entry
// takes its name from the request count, so the first reads "Catalog 1".
// The transport answers the first immediate requests at once and holds
// each later one until the caller calls release or the request's context
// ends. The returned counter reports the requests the transport has
// received. Cleanup releases held requests.
//
// The transport stands in for a test server so the client works inside a
// synctest bubble, where a goroutine waiting on a socket does not count as
// durably blocked.
func newHeldCatalogClient(t *testing.T, immediate int32) (*http.Client, *atomic.Int32, func()) {
	t.Helper()

	var requests atomic.Int32

	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	t.Cleanup(release)

	client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := requests.Add(1)
		if n > immediate {
			select {
			case <-unblock:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}

		data, err := json.Marshal(schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{{
				Name:      fmt.Sprintf("Catalog %d", n),
				URL:       testSchemaURL,
				FileMatch: []string{"*.yaml"},
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal catalog: %w", err)
		}

		return yamltest.Respond(http.StatusOK, string(data))(r)
	}).Client()

	return client, &requests, release
}

// requireEntry looks config.yaml up in store and requires the entry
// named want.
func requireEntry(t *testing.T, store *schemastore.Store, want string) {
	t.Helper()

	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, want, entry.Name)
}

// findMatchResult holds what a FindMatch call started by findMatchAsync
// returned.
type findMatchResult struct {
	err   error
	entry schemastore.CatalogEntry
}

// findMatchAsync looks up config.yaml with FindMatch in a new goroutine and
// sends the result on the returned channel.
func findMatchAsync(ctx context.Context, store *schemastore.Store) <-chan findMatchResult {
	results := make(chan findMatchResult, 1)

	go func() {
		entry, err := store.FindMatch(ctx, "config.yaml")
		results <- findMatchResult{err: err, entry: entry}
	}()

	return results
}

// receive returns the next value from ch and fails the test if none arrives
// within five seconds. Inside a synctest bubble the five seconds pass on the
// bubble's clock, which moves only once every goroutine waits, so a failure
// there means the value could never arrive.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case v := <-ch:
		return v

	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a value")

		var zero T

		return zero
	}
}

func TestStore_ParseErrorRedactsCredentials(t *testing.T) {
	t.Parallel()

	store := schemastore.New(
		schemastore.WithCatalogURL("http://svc:hunter2@example.com/catalog.json"),
		schemastore.WithHTTPClient(yamltest.Respond(http.StatusOK, "<html>not json</html>").Client()),
	)

	_, err := store.FindMatch(t.Context(), "a.yaml")
	require.Error(t, err)

	// The catalog URL in the message carries no password, as it does not
	// when the fetch itself fails.
	assert.Contains(t, err.Error(), "svc:xxxxx@")
	assert.NotContains(t, err.Error(), "hunter2")
}

func TestStore_FindMatch_SupportedPatterns(t *testing.T) {
	t.Parallel()

	// A pattern whose braces expand past the limit matches no file.
	braces := strings.Repeat("{a,b}", 11) + ".yaml"

	// Expanding this exclusion takes all the work the limit allows. The
	// "!" would add a byte to each of the 512 patterns the expansion
	// builds, so the store must check the exclusion without it, as
	// matching does, to keep the exclusion.
	excludeName := strings.Repeat("a", 512) + strings.Repeat("b", 1008) + ".yaml"
	exclusion := "!**/exclude/" + strings.Repeat("{a}", 512) + strings.Repeat("b", 1008) + ".yaml"

	_, err := filepaths.ExpandBraces(exclusion[1:])
	require.NoError(t, err)

	_, err = filepaths.ExpandBraces(exclusion)
	require.ErrorIs(t, err, filepaths.ErrBraceLimit)

	// The store keeps the patterns of an entry that can match a YAML file
	// and drops the rest, and FindMatch returns the patterns it kept.
	catalog := schemastore.Catalog{Schemas: []schemastore.CatalogEntry{
		// A pattern whose extension holds a wildcard can match a YAML
		// file, so the store keeps it. The store drops a pattern whose
		// literal extension it does not support.
		{
			Name:      "Azure Pipelines",
			URL:       "https://example.com/azure.json",
			FileMatch: []string{"**/azure-pipelines*.y*ml", "*.toml"},
		},

		// A file name without an extension can belong to a YAML file, as
		// .clang-format and .yamllint do, so the store keeps such a
		// pattern beside the patterns with a YAML extension. It still
		// drops a pattern with an extension it does not support.
		{
			Name:      "clang-format",
			URL:       "https://example.com/clang-format.json",
			FileMatch: []string{".clang-format"},
		},
		{
			Name:      "yamllint",
			URL:       "https://example.com/yamllint.json",
			FileMatch: []string{"**/.yamllint", "**/.yamllint.yaml", "**/.yamllint.yml", "**/.yamllint.toml"},
		},
		{
			Name:      "BOSH job spec",
			URL:       "https://example.com/bosh.json",
			FileMatch: []string{"**/jobs/*/spec"},
		},

		// Many YAML formats use an extension of their own, so the store
		// keeps their patterns. It drops only the patterns for formats
		// known not to be YAML, such as TOML and JSON with comments.
		{
			Name:      "Citation File Format",
			URL:       "https://example.com/cff.json",
			FileMatch: []string{"CITATION.cff"},
		},
		{
			Name:      "Helm Chart.lock",
			URL:       "https://example.com/chart-lock.json",
			FileMatch: []string{"Chart.lock"},
		},
		{
			Name:      "Sublime Syntax",
			URL:       "https://example.com/sublime-syntax.json",
			FileMatch: []string{"*.sublime-syntax"},
		},
		{
			Name:      "Biome",
			URL:       "https://example.com/biome.json",
			FileMatch: []string{"biome.json", "biome.jsonc"},
		},

		// The store drops a pattern whose braces expand past the limit
		// from the entry, as it drops a pattern for a format that is not
		// YAML. An entry left with no pattern matches nothing.
		{
			Name:      "Mixed",
			URL:       "https://example.com/mixed.json",
			FileMatch: []string{"**/mixed/*.yaml", "**/mixed/" + braces},
		},
		{
			Name:      "Braces",
			URL:       "https://example.com/braces.json",
			FileMatch: []string{"**/braces/" + braces},
		},
		{
			Name:      "Exclusion",
			URL:       "https://example.com/exclusion.json",
			FileMatch: []string{"**/exclude/*.yaml", exclusion},
		},
	}}

	store := newCatalogStore(t, catalog)

	tcs := map[string]struct {
		file string
		// The name and the kept patterns of the matching entry, or empty
		// for no match.
		want          string
		wantFileMatch []string
	}{
		"wildcard extension": {
			file:          "ci/azure-pipelines.yaml",
			want:          "Azure Pipelines",
			wantFileMatch: []string{"**/azure-pipelines*.y*ml"},
		},
		"unsupported extension beside a wildcard one": {file: "ci/azure.toml"},
		"hidden file without an extension": {
			file:          "/repo/.clang-format",
			want:          "clang-format",
			wantFileMatch: []string{".clang-format"},
		},
		"extensionless pattern beside YAML patterns": {
			file:          "/repo/.yamllint",
			want:          "yamllint",
			wantFileMatch: []string{"**/.yamllint", "**/.yamllint.yaml", "**/.yamllint.yml"},
		},
		"YAML pattern of the same entry": {
			file:          "/repo/.yamllint.yml",
			want:          "yamllint",
			wantFileMatch: []string{"**/.yamllint", "**/.yamllint.yaml", "**/.yamllint.yml"},
		},
		"plain name without an extension": {
			file:          "/repo/jobs/web/spec",
			want:          "BOSH job spec",
			wantFileMatch: []string{"**/jobs/*/spec"},
		},
		"unsupported extension": {file: "/repo/.yamllint.toml"},
		"cff extension": {
			file:          "/repo/CITATION.cff",
			want:          "Citation File Format",
			wantFileMatch: []string{"CITATION.cff"},
		},
		"lock extension": {
			file:          "/repo/charts/app/Chart.lock",
			want:          "Helm Chart.lock",
			wantFileMatch: []string{"Chart.lock"},
		},
		"hyphenated extension": {
			file:          "/repo/x.sublime-syntax",
			want:          "Sublime Syntax",
			wantFileMatch: []string{"*.sublime-syntax"},
		},
		"JSON pattern beside a JSONC pattern": {
			file:          "/repo/biome.json",
			want:          "Biome",
			wantFileMatch: []string{"biome.json"},
		},
		"jsonc extension": {file: "/repo/biome.jsonc"},
		"pattern beside one past the limit": {
			file:          "/repo/mixed/x.yaml",
			want:          "Mixed",
			wantFileMatch: []string{"**/mixed/*.yaml"},
		},
		"pattern past the limit": {file: "/repo/braces/" + strings.Repeat("a", 11) + ".yaml"},
		"pattern beside an exclusion at the limit": {
			file:          "/repo/exclude/x.yaml",
			want:          "Exclusion",
			wantFileMatch: []string{"**/exclude/*.yaml", exclusion},
		},
		"file an exclusion at the limit excludes": {file: "/repo/exclude/" + excludeName},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entry, err := store.FindMatch(t.Context(), tc.file)
			if tc.want == "" {
				require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, entry.Name)
			assert.Equal(t, tc.wantFileMatch, entry.FileMatch)
		})
	}
}
