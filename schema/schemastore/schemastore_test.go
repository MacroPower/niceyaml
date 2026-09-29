package schemastore_test

import (
	"bytes"
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
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

// testCatalog is a one-entry catalog whose pattern matches every YAML file.
var testCatalog = schemastore.Catalog{
	Schemas: []schemastore.CatalogEntry{
		{
			Name:      "Test",
			URL:       "https://example.com/test.json",
			FileMatch: []string{"*.yaml"},
		},
	},
}

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
		},
	}

	tcs := map[string]struct {
		filePath string
		wantName string
		err      error
	}{
		"no match for an entry with a file URL": {
			filePath: "repo/local/config.yaml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"matches an entry with an uppercase HTTPS scheme": {
			filePath: "repo/upper/config.yaml",
			wantName: "Uppercase Scheme",
		},
		"matches brace alternative yml": {
			filePath: ".circleci/config.yml",
			wantName: "CircleCI",
		},
		"matches brace alternative yaml": {
			filePath: "repo/.circleci/config.yaml",
			wantName: "CircleCI",
		},
		"no match for brace alternatives without a yaml extension": {
			filePath: "settings.toml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"matches github workflow yaml": {
			filePath: ".github/workflows/ci.yaml",
			wantName: "GitHub Workflow",
		},
		"matches github workflow yml": {
			filePath: ".github/workflows/build.yml",
			wantName: "GitHub Workflow",
		},
		"matches github workflow under an absolute repo path": {
			filePath: "/home/user/repo/.github/workflows/ci.yaml",
			wantName: "GitHub Workflow",
		},
		"matches dependabot yaml": {
			filePath: ".github/dependabot.yaml",
			wantName: "Dependabot",
		},
		"matches dependabot yml": {
			filePath: ".github/dependabot.yml",
			wantName: "Dependabot",
		},
		"matches json file": {
			filePath: "schema.json",
			wantName: "JSON Schema Draft 7",
		},
		"no match for an extglob pattern": {
			// The matcher implements no extglob, so the store drops the
			// pattern rather than keeping it as something only a literally
			// named file could match.
			filePath: ".github/ISSUE_TEMPLATE/bug.yml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"no match for the literal spelling of an extglob pattern": {
			filePath: ".github/ISSUE_TEMPLATE/!(config).yml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"matches the supported pattern beside an extglob one": {
			filePath: "repo/mixed/config.yaml",
			wantName: "Mixed Patterns",
		},
		"matches a pattern beside an exclusion": {
			filePath: "repo/compose/override.yml",
			wantName: "Compose Overrides",
		},
		"no match for an excluded file": {
			filePath: "repo/compose/docker-compose.yml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"no match for unrelated file": {
			filePath: "config.yaml",
			err:      schemastore.ErrNoCatalogMatch,
		},
		"empty file path": {
			filePath: "",
			err:      schemastore.ErrNoCatalogMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
			)

			entry, err := store.FindMatch(t.Context(), tc.filePath)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, err, schema.ErrNoMatch)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantName, entry.Name)
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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
			)

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
	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Generic",
				URL:       "https://example.com/generic.json",
				FileMatch: []string{"*.yaml"},
			},
			{
				Name:      "Specific",
				URL:       "https://example.com/specific.json",
				FileMatch: []string{"myapp.yaml"},
			},
		},
	}

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

	entry, err := store.FindMatch(t.Context(), "/x/other.yaml")
	require.NoError(t, err)
	// The entry equals the catalog's, so no state the store keeps for
	// matching reaches the caller.
	require.Equal(t, catalog.Schemas[0], entry)

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
	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{Name: "Generic", URL: "https://example.com/generic.json", FileMatch: []string{"*.yaml"}},
			{Name: "Specific", URL: "https://example.com/specific.json", FileMatch: []string{"myapp.yaml"}},
		},
	}

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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
			)

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

	// With caching off, each lookup fetches a new catalog whose entry takes
	// its name from the request count. A lookup matches against the catalog
	// it fetched, not the one an earlier lookup of the same path read.
	client, requests, _ := newHeldCatalogClient(t, 3)

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(client),
		schemastore.WithCacheTTL(0),
	)

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

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(b, catalog)),
	)

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
		client, fetchCount := newCountingCatalogClient(t, testCatalog)

		// New performs no I/O.
		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(client),
		)

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

	client, fetchCount := newCountingCatalogClient(t, testCatalog)

	// Use a very short TTL for testing.
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(client),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)

	// First lookup fetches.
	_, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// Lookup triggers refetch due to expired cache.
	_, err = store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, int32(2), fetchCount.Load())
}

func TestSchemaStore_NoCaching(t *testing.T) {
	t.Parallel()

	client, fetchCount := newCountingCatalogClient(t, testCatalog)

	// TTL of 0 should disable caching (fetch on every lookup).
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(client),
		schemastore.WithCacheTTL(0),
	)

	for i := range 3 {
		_, err := store.FindMatch(t.Context(), "config.yaml")
		require.NoError(t, err)
		assert.Equal(t, int32(i+1), fetchCount.Load())
	}
}

func TestSchemaStore_ConcurrentFirstFetch(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Concurrent lookups before the catalog has loaded share one fetch.
		const goroutines = 10

		data, err := json.Marshal(testCatalog)
		require.NoError(t, err)

		var fetchCount atomic.Int32

		release := make(chan struct{})

		// A goroutine waiting on a test server's socket does not count as
		// durably blocked inside the bubble, so a stub transport stands in
		// for the server.
		client := &http.Client{
			Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
				fetchCount.Add(1)
				<-release // Hold the fetch open until every caller waits on it.

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(data)),
					Request:    r,
				}, nil
			}},
		}

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(client),
		)

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
		close(release)
		wg.Wait()

		assert.Equal(t, int32(1), fetchCount.Load(), "concurrent lookups should share one fetch")

		for i := range goroutines {
			require.NoError(t, errs[i])
			assert.Equal(t, "Test", entries[i].Name)
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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
				schemastore.WithCacheTTL(10*time.Millisecond),
			)

			entry, err := store.FindMatch(t.Context(), "config.yaml")
			require.NoError(t, err)
			require.Equal(t, "Catalog 1", entry.Name)

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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
			)

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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
			)

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

			entry, err := store.FindMatch(t.Context(), "config.yaml")
			require.NoError(t, err)
			assert.Equal(t, "Catalog 1", entry.Name)
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

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
				schemastore.WithRefreshTimeout(timeout),
			)

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
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		schemastore.WithCacheTTL(1*time.Millisecond),
	)

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
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
			return strings.Contains(strings.ToLower(e.Name), "github")
		}),
	)

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
		setup func(t *testing.T) ([]schemastore.Option, *atomic.Int32)
		want  any
	}{
		"filter": {
			setup: func(t *testing.T) ([]schemastore.Option, *atomic.Int32) {
				t.Helper()

				client, fetchCount := newCountingCatalogClient(t, testCatalog)

				return []schemastore.Option{
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
					schemastore.WithHTTPClient(client),
					schemastore.WithFilter(func(schemastore.CatalogEntry) bool {
						panic("filter bug")
					}),
				}, fetchCount
			},
			want: "filter bug",
		},
		"transport": {
			setup: func(t *testing.T) ([]schemastore.Option, *atomic.Int32) {
				t.Helper()

				var fetchCount atomic.Int32

				client := &http.Client{
					Transport: &roundTripperFunc{fn: func(*http.Request) (*http.Response, error) {
						fetchCount.Add(1)

						panic("transport bug")
					}},
				}

				return []schemastore.Option{
					schemastore.WithCatalogURL("http://example.com/catalog.json"),
					schemastore.WithHTTPClient(client),
				}, &fetchCount
			},
			want: "transport bug",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts, fetchCount := tc.setup(t)
			store := schemastore.New(opts...)

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
		setup func(t *testing.T) ([]schemastore.Option, *atomic.Int32)
	}{
		"filter": {
			setup: func(t *testing.T) ([]schemastore.Option, *atomic.Int32) {
				t.Helper()

				client, fetchCount := newCountingCatalogClient(t, testCatalog)

				return []schemastore.Option{
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
					schemastore.WithHTTPClient(client),
					schemastore.WithFilter(func(schemastore.CatalogEntry) bool {
						runtime.Goexit()

						return true
					}),
				}, fetchCount
			},
		},
		"transport": {
			setup: func(t *testing.T) ([]schemastore.Option, *atomic.Int32) {
				t.Helper()

				var fetchCount atomic.Int32

				client := &http.Client{
					Transport: &roundTripperFunc{fn: func(*http.Request) (*http.Response, error) {
						fetchCount.Add(1)
						runtime.Goexit()

						panic("unreachable")
					}},
				}

				return []schemastore.Option{
					schemastore.WithCatalogURL("http://example.com/catalog.json"),
					schemastore.WithHTTPClient(client),
				}, &fetchCount
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts, fetchCount := tc.setup(t)
			store := schemastore.New(opts...)

			var returned bool

			done := make(chan struct{})

			go func() {
				defer close(done)

				//nolint:errcheck // The call exits through runtime.Goexit.
				_, _ = store.FindMatch(t.Context(), "config.yaml")
				returned = true
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "FindMatch hung after the fetch called runtime.Goexit")
			}

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
		fn func(*http.Request) (*http.Response, error)
	}{
		"panic": {
			fn: func(*http.Request) (*http.Response, error) {
				panic("transport bug")
			},
		},
		"goexit": {
			fn: func(*http.Request) (*http.Response, error) {
				runtime.Goexit()

				panic("unreachable")
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := schemastore.New(
				schemastore.WithCatalogURL("http://example.com/catalog.json"),
				schemastore.WithHTTPClient(&http.Client{Transport: &roundTripperFunc{fn: tc.fn}}),
			)

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
		opts      []schemastore.Option
		wantPanic any
		wantErr   bool
	}{
		"fetch succeeds": {
			opts: []schemastore.Option{
				schemastore.WithHTTPClient(newCatalogClient(t, testCatalog)),
			},
		},
		"fetch fails": {
			opts: []schemastore.Option{
				schemastore.WithHTTPClient(newClient(http.StatusInternalServerError, nil)),
			},
			wantErr: true,
		},
		"filter panics": {
			opts: []schemastore.Option{
				schemastore.WithHTTPClient(newCatalogClient(t, testCatalog)),
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
				opts := append([]schemastore.Option{
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
				}, tc.opts...)
				store := schemastore.New(opts...)

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
	client, fetchCount := newCountingCatalogClient(t, testCatalog)

	store := schemastore.New(
		schemastore.WithCatalogURL("https://catalog.invalid/catalog.json"),
		schemastore.WithHTTPClient(client),
	)

	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
	assert.Equal(t, int32(1), fetchCount.Load())
}

func TestSchemaStore_ZeroRefreshTimeout(t *testing.T) {
	t.Parallel()

	// A zero timeout keeps the default rather than expiring every fetch
	// before it starts.
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, testCatalog)),
		schemastore.WithRefreshTimeout(0),
	)

	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
}

func TestSchemaStore_NilHTTPClient(t *testing.T) {
	t.Parallel()

	// A nil client keeps the default rather than panicking on the first
	// fetch. The default client needs a real server, so this is the one
	// test in the package that starts one. See newCountingClient for why
	// the other tests fetch from memory.
	data, err := json.Marshal(testCatalog)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithHTTPClient(nil),
	)

	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
}

func TestSchemaStore_FetchError(t *testing.T) {
	t.Parallel()

	// With no catalog ever loaded, a failed fetch surfaces from the lookup.
	tcs := map[string]struct {
		setup func(t *testing.T) []schemastore.Option
	}{
		"server error": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				return []schemastore.Option{
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
					schemastore.WithHTTPClient(newClient(http.StatusInternalServerError, nil)),
				}
			},
		},
		"invalid json": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				return []schemastore.Option{
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
					schemastore.WithHTTPClient(newClient(http.StatusOK, []byte("not json"))),
				}
			},
		},
		"invalid URL": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				// The control character makes url.Parse reject the URL, so the
				// fetch sends no request.
				return []schemastore.Option{schemastore.WithCatalogURL("http://\x00invalid")}
			},
		},
		"connection refused": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				// Port 1 is a privileged port that won't be listening.
				return []schemastore.Option{schemastore.WithCatalogURL("http://localhost:1")}
			},
		},
		"read body error": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				client := &http.Client{
					Transport: &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(&errorReader{}),
						}, nil
					}},
				}

				return []schemastore.Option{
					schemastore.WithCatalogURL("http://example.com/catalog.json"),
					schemastore.WithHTTPClient(client),
				}
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := schemastore.New(tc.setup(t)...)

			_, err := store.FindMatch(t.Context(), "config.yaml")
			require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			require.NotErrorIs(t, err, schema.ErrNoMatch)

			doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")
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
		client, fetchCount := newCountingClient(http.StatusInternalServerError, nil)

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(client),
			schemastore.WithRetryAfter(20*time.Millisecond),
		)

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

			client, fetchCount := newCountingClient(http.StatusInternalServerError, nil)

			// A non-positive interval keeps the default rather than
			// retrying on every lookup, which costs one refresh timeout
			// each.
			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
				schemastore.WithRetryAfter(interval),
			)

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

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

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
			client, fetchCount := newCountingCatalogClient(t, testCatalog)

			// Use short TTL so cache expires quickly.
			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
				schemastore.WithCacheTTL(10*time.Millisecond),
			)

			_, err := store.FindMatch(t.Context(), "config.yaml")
			require.NoError(t, err)

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
			client, fetchCount := newCountingCatalogClient(t, testCatalog)

			store := schemastore.New(
				schemastore.WithCatalogURL("https://example.com/catalog.json"),
				schemastore.WithHTTPClient(client),
			)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := store.FindMatch(ctx, "config.yaml")
			require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
			require.ErrorIs(t, err, context.Canceled)

			synctest.Wait()
			assert.Equal(t, int32(0), fetchCount.Load())

			// A cancellation is not a failed attempt, so the next lookup
			// fetches.
			entry, err := store.FindMatch(t.Context(), "config.yaml")
			require.NoError(t, err)
			assert.Equal(t, "Test", entry.Name)

			synctest.Wait()
			assert.Equal(t, int32(1), fetchCount.Load())
		})
	})
}

func TestSchemaStore_RefetchFails(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(testCatalog)
	require.NoError(t, err)

	var requestCount atomic.Int32

	// The transport succeeds the first time and fails on later requests.
	client := &http.Client{
		Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
			if requestCount.Add(1) > 1 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       http.NoBody,
					Request:    r,
				}, nil
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Request:    r,
			}, nil
		}},
	}

	// Use short TTL so cache expires quickly.
	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(client),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)

	// First fetch succeeds.
	_, err = store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, int32(1), requestCount.Load())

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// FindMatch triggers a refetch which fails, but still returns cached data.
	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)

	// Verify the store attempted the refetch.
	assert.Equal(t, int32(2), requestCount.Load())

	// Inside the retry interval the store serves stale data without a request.
	entry, err = store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
	assert.Equal(t, int32(2), requestCount.Load())
}

func TestSchemaStore_CatalogWithoutSchemas(t *testing.T) {
	t.Parallel()

	// A JSON body with no schemas array is not a catalog, so the store
	// treats it as a failed fetch rather than as a catalog with no entries.
	tcs := map[string]struct {
		body string
	}{
		"null": {
			body: `null`,
		},
		"empty object": {
			body: `{}`,
		},
		"null schemas": {
			body: `{"schemas": null}`,
		},
		"error object": {
			body: `{"message": "rate limited"}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("without a cached catalog", func(t *testing.T) {
				t.Parallel()

				store := schemastore.New(
					schemastore.WithCatalogURL("https://example.com/catalog.json"),
					schemastore.WithHTTPClient(newClient(http.StatusOK, []byte(tc.body))),
				)

				_, err := store.FindMatch(t.Context(), "config.yaml")
				require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
				require.NotErrorIs(t, err, schema.ErrNoMatch)
			})

			t.Run("with a cached catalog", func(t *testing.T) {
				t.Parallel()

				// Inside the bubble, the sleep below moves the fake clock
				// past the cache TTL without waiting in real time.
				synctest.Test(t, func(t *testing.T) {
					catalog := marshalCatalog(t, testCatalog)

					var requestCount atomic.Int32

					// The transport serves the catalog first and the body
					// after. It opens no socket, so it counts only the
					// store's own requests.
					client := &http.Client{
						Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
							data := catalog
							if requestCount.Add(1) > 1 {
								data = []byte(tc.body)
							}

							return &http.Response{
								StatusCode: http.StatusOK,
								Body:       io.NopCloser(bytes.NewReader(data)),
								Request:    r,
							}, nil
						}},
					}

					store := schemastore.New(
						schemastore.WithCatalogURL("https://example.com/catalog.json"),
						schemastore.WithHTTPClient(client),
						schemastore.WithCacheTTL(10*time.Millisecond),
					)

					_, err := store.FindMatch(t.Context(), "config.yaml")
					require.NoError(t, err)

					// Wait for cache to expire.
					time.Sleep(20 * time.Millisecond)

					// The refresh fails, so the store keeps the cached catalog.
					entry, err := store.FindMatch(t.Context(), "config.yaml")
					require.NoError(t, err)
					assert.Equal(t, "Test", entry.Name)
					assert.Equal(t, int32(2), requestCount.Load())
				})
			})
		})
	}
}

func TestSchemaStore_SkipsEntriesWithoutURL(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "No URL",
				URL:       "",
				FileMatch: []string{"*.yaml"},
			},
			{
				Name:      "Has URL",
				URL:       "https://example.com/schema.json",
				FileMatch: []string{"config.yaml"},
			},
		},
	}

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

	// The store skips the entry without a URL, so random.yaml won't match.
	_, err := store.FindMatch(t.Context(), "random.yaml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)

	// Entry with URL should still match.
	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Has URL", entry.Name)
}

func TestSchemaStore_Resolve(t *testing.T) {
	t.Parallel()

	schemaData := `{"type": "object"}`

	t.Run("matches known pattern", func(t *testing.T) {
		t.Parallel()

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       "https://json.schemastore.org/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml"},
				},
			},
		}

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`on: push`), ".github/workflows/ci.yaml")
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, "https://json.schemastore.org/github-workflow.json", ref.Key())
	})

	t.Run("no match for unknown file", func(t *testing.T) {
		t.Parallel()

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       "https://json.schemastore.org/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml"},
				},
			},
		}

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "random.yaml")
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

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`on: push`), "ci.yml")
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

		const (
			catalogURL = "https://example.com/catalog.json"
			schemaURL  = "https://example.com/schema.json"
		)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Test Schema",
					URL:       schemaURL,
					FileMatch: []string{"*.yaml"},
				},
			},
		}

		client := newRoutingClient(map[string][]byte{
			catalogURL: marshalCatalog(t, catalog),
			schemaURL:  []byte(schemaData),
		})

		store := schemastore.New(
			schemastore.WithCatalogURL(catalogURL),
			schemastore.WithHTTPClient(client),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaURL, ref.Key())

		data, err := schema.NewRegistry(schema.WithHTTPClient(client)).Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("error when schema URL not found", func(t *testing.T) {
		t.Parallel()

		const (
			catalogURL = "https://example.com/catalog.json"
			schemaURL  = "https://example.com/schema.json"
		)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Test Schema",
					URL:       schemaURL,
					FileMatch: []string{"*.yaml"},
				},
			},
		}

		// The client serves the catalog alone, so the schema URL gets
		// status 404.
		client := newRoutingClient(map[string][]byte{
			catalogURL: marshalCatalog(t, catalog),
		})

		store := schemastore.New(
			schemastore.WithCatalogURL(catalogURL),
			schemastore.WithHTTPClient(client),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		// The path matches, so Resolve names the schema. Only the load fails,
		// and a failed load is not a no-match.
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaURL, ref.Key())

		_, err = schema.NewRegistry(schema.WithHTTPClient(client)).Load(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "fetch "+schemaURL+": status 404")
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestIntegration(t *testing.T) {
	t.Parallel()

	schemaData := `{
		"type": "object",
		"properties": {
			"on": {"type": ["string", "object"]}
		},
		"required": ["on"]
	}`

	t.Run("validates matching document", func(t *testing.T) {
		t.Parallel()

		const (
			catalogURL = "https://example.com/catalog.json"
			schemaURL  = "https://example.com/github-workflow.json"
		)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       schemaURL,
					FileMatch: []string{".github/workflows/*.yaml", ".github/workflows/*.yml"},
				},
			},
		}

		client := newRoutingClient(map[string][]byte{
			catalogURL: marshalCatalog(t, catalog),
			schemaURL:  []byte(schemaData),
		})

		store := schemastore.New(
			schemastore.WithCatalogURL(catalogURL),
			schemastore.WithHTTPClient(client),
		)

		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithResolvers(store),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`on: push`), ".github/workflows/ci.yaml")

		err := reg.Validate(t.Context(), doc)
		require.NoError(t, err)
	})

	t.Run("fetches each schema once", func(t *testing.T) {
		t.Parallel()

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       "https://example.com/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml"},
				},
			},
		}

		catalogClient, catalogFetches := newCountingCatalogClient(t, catalog)
		schemaClient, schemaFetches := newCountingClient(http.StatusOK, []byte(schemaData))

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(catalogClient),
		)

		reg := schema.NewRegistry(
			schema.WithHTTPClient(schemaClient),
			schema.WithResolvers(store),
		)

		for _, name := range []string{"ci.yaml", "release.yaml", "lint.yaml"} {
			doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`on: push`), ".github/workflows/"+name)

			err := reg.Validate(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), catalogFetches.Load())
		assert.Equal(t, int32(1), schemaFetches.Load(), "the registry should fetch the schema once")
	})

	t.Run("rejects invalid document", func(t *testing.T) {
		t.Parallel()

		const (
			catalogURL = "https://example.com/catalog.json"
			schemaURL  = "https://example.com/github-workflow.json"
		)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       schemaURL,
					FileMatch: []string{".github/workflows/*.yaml", ".github/workflows/*.yml"},
				},
			},
		}

		client := newRoutingClient(map[string][]byte{
			catalogURL: marshalCatalog(t, catalog),
			schemaURL:  []byte(schemaData),
		})

		store := schemastore.New(
			schemastore.WithCatalogURL(catalogURL),
			schemastore.WithHTTPClient(client),
		)

		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithResolvers(store),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`name: test`), ".github/workflows/ci.yaml")

		err := reg.Validate(t.Context(), doc)
		require.Error(t, err)
	})

	t.Run("returns ErrNoMatch for unmatched document", func(t *testing.T) {
		t.Parallel()

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       "https://example.com/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml", ".github/workflows/*.yml"},
				},
			},
		}

		store := schemastore.New(
			schemastore.WithCatalogURL("https://example.com/catalog.json"),
			schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
		)

		reg := schema.NewRegistry(schema.WithResolvers(store))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "random.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("reports an unreachable catalog through the registry", func(t *testing.T) {
		t.Parallel()

		store := schemastore.New(schemastore.WithCatalogURL("http://localhost:1"))
		reg := schema.NewRegistry(schema.WithResolvers(store))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("stops before resolvers registered after an unreachable catalog", func(t *testing.T) {
		t.Parallel()

		var fallbackCalls atomic.Int32

		fallback := schema.ResolverFunc(func(context.Context, *niceyaml.Node) (schema.Ref, error) {
			fallbackCalls.Add(1)

			return schema.Ref{}, schema.ErrNoMatch
		})

		reg := schema.NewRegistry(
			schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL("http://localhost:1")), fallback),
		)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		assert.Zero(t, fallbackCalls.Load(), "the registry should not try resolvers after the store")
	})
}

// Helper functions.

// newCountingClient returns a client whose transport answers every URL
// with status and body, along with a counter of the requests it has
// answered. The transport opens no socket, so no other test or process
// can reach it and change the count.
//
// Tests in this package fetch through in-memory clients like this one
// rather than from test servers on http.DefaultTransport. Closing a test
// server closes the idle connections of http.DefaultTransport. That
// transport returns a connection to its idle pool just before it hands
// over a response with no body, so a request that another test has in
// flight can fail with "transport connection broken" instead of getting
// its response.
func newCountingClient(status int, body []byte) (*http.Client, *atomic.Int32) {
	var requests atomic.Int32

	client := &http.Client{
		Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
			requests.Add(1)

			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Request:    r,
			}, nil
		}},
	}

	return client, &requests
}

// newClient is newCountingClient without the counter.
func newClient(status int, body []byte) *http.Client {
	client, _ := newCountingClient(status, body)

	return client
}

// newCountingCatalogClient is newCountingClient serving catalog.
func newCountingCatalogClient(tb testing.TB, catalog schemastore.Catalog) (*http.Client, *atomic.Int32) {
	tb.Helper()

	return newCountingClient(http.StatusOK, marshalCatalog(tb, catalog))
}

// newCatalogClient is newClient serving catalog.
func newCatalogClient(tb testing.TB, catalog schemastore.Catalog) *http.Client {
	tb.Helper()

	return newClient(http.StatusOK, marshalCatalog(tb, catalog))
}

// newRoutingClient returns a client whose transport answers each URL in
// routes with its body and every other URL with status 404. Like
// newCountingClient, it opens no socket.
func newRoutingClient(routes map[string][]byte) *http.Client {
	return &http.Client{
		Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
			body, ok := routes[r.URL.String()]
			if !ok {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       http.NoBody,
					Request:    r,
				}, nil
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
				Request:    r,
			}, nil
		}},
	}
}

// marshalCatalog returns catalog as JSON.
func marshalCatalog(tb testing.TB, catalog schemastore.Catalog) []byte {
	tb.Helper()

	data, err := json.Marshal(catalog)
	require.NoError(tb, err)

	return data
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

	client := &http.Client{
		Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
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
					URL:       "https://example.com/test.json",
					FileMatch: []string{"*.yaml"},
				}},
			})
			if err != nil {
				return nil, fmt.Errorf("marshal catalog: %w", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Request:    r,
			}, nil
		}},
	}

	return client, &requests, release
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

type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f.fn(r)
}

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestStore_ParseErrorRedactsCredentials(t *testing.T) {
	t.Parallel()

	store := schemastore.New(
		schemastore.WithCatalogURL("http://svc:hunter2@example.com/catalog.json"),
		schemastore.WithHTTPClient(newClient(http.StatusOK, []byte("<html>not json</html>"))),
	)

	_, err := store.FindMatch(t.Context(), "a.yaml")
	require.Error(t, err)

	// The catalog URL in the message carries no password, as it does not
	// when the fetch itself fails.
	assert.Contains(t, err.Error(), "svc:xxxxx@")
	assert.NotContains(t, err.Error(), "hunter2")
}

func TestStore_FindMatch_WildcardExtension(t *testing.T) {
	t.Parallel()

	// A pattern whose extension holds a wildcard can match a YAML file, so
	// the store keeps it. The store drops a pattern whose literal extension
	// it does not support.
	catalog := schemastore.Catalog{Schemas: []schemastore.CatalogEntry{{
		Name:      "Azure Pipelines",
		URL:       "https://example.com/azure.json",
		FileMatch: []string{"**/azure-pipelines*.y*ml", "*.toml"},
	}}}

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

	entry, err := store.FindMatch(t.Context(), "ci/azure-pipelines.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Azure Pipelines", entry.Name)
	assert.Equal(t, []string{"**/azure-pipelines*.y*ml"}, entry.FileMatch)

	_, err = store.FindMatch(t.Context(), "ci/azure.toml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
}

func TestStore_FindMatch_NoExtension(t *testing.T) {
	t.Parallel()

	// A file name without an extension can belong to a YAML file, as
	// .clang-format and .yamllint do, so the store keeps such a pattern
	// beside the patterns with a YAML extension. It still drops a pattern
	// with an extension it does not support.
	catalog := schemastore.Catalog{Schemas: []schemastore.CatalogEntry{
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
	}}

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

	tcs := map[string]struct {
		file      string
		name      string
		fileMatch []string
		err       error
	}{
		"hidden file without an extension": {
			file:      "/repo/.clang-format",
			name:      "clang-format",
			fileMatch: []string{".clang-format"},
		},
		"extensionless pattern beside YAML patterns": {
			file:      "/repo/.yamllint",
			name:      "yamllint",
			fileMatch: []string{"**/.yamllint", "**/.yamllint.yaml", "**/.yamllint.yml"},
		},
		"YAML pattern of the same entry": {
			file:      "/repo/.yamllint.yml",
			name:      "yamllint",
			fileMatch: []string{"**/.yamllint", "**/.yamllint.yaml", "**/.yamllint.yml"},
		},
		"plain name without an extension": {
			file:      "/repo/jobs/web/spec",
			name:      "BOSH job spec",
			fileMatch: []string{"**/jobs/*/spec"},
		},
		"unsupported extension": {
			file: "/repo/.yamllint.toml",
			err:  schemastore.ErrNoCatalogMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entry, err := store.FindMatch(t.Context(), tc.file)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.name, entry.Name)
			assert.Equal(t, tc.fileMatch, entry.FileMatch)
		})
	}
}

func TestStore_FindMatch_OtherYAMLExtension(t *testing.T) {
	t.Parallel()

	// Many YAML formats use an extension of their own, so the store keeps
	// their patterns. It drops only the patterns for formats known not to
	// be YAML, such as TOML and JSON with comments.
	catalog := schemastore.Catalog{Schemas: []schemastore.CatalogEntry{
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
	}}

	store := schemastore.New(
		schemastore.WithCatalogURL("https://example.com/catalog.json"),
		schemastore.WithHTTPClient(newCatalogClient(t, catalog)),
	)

	tcs := map[string]struct {
		file      string
		name      string
		fileMatch []string
		err       error
	}{
		"cff extension": {
			file:      "/repo/CITATION.cff",
			name:      "Citation File Format",
			fileMatch: []string{"CITATION.cff"},
		},
		"lock extension": {
			file:      "/repo/charts/app/Chart.lock",
			name:      "Helm Chart.lock",
			fileMatch: []string{"Chart.lock"},
		},
		"hyphenated extension": {
			file:      "/repo/x.sublime-syntax",
			name:      "Sublime Syntax",
			fileMatch: []string{"*.sublime-syntax"},
		},
		"JSON pattern beside a JSONC pattern": {
			file:      "/repo/biome.json",
			name:      "Biome",
			fileMatch: []string{"biome.json"},
		},
		"jsonc extension": {
			file: "/repo/biome.jsonc",
			err:  schemastore.ErrNoCatalogMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entry, err := store.FindMatch(t.Context(), tc.file)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.name, entry.Name)
			assert.Equal(t, tc.fileMatch, entry.FileMatch)
		})
	}
}
