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
		},
	}

	tcs := map[string]struct {
		filePath string
		wantName string
		err      error
	}{
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

			// Each parallel test gets its own server.
			server := newCatalogServer(t, catalog)
			t.Cleanup(server.Close)

			store := schemastore.New(schemastore.WithCatalogURL(server.URL))

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

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	entry, err := store.FindMatch(t.Context(), "/x/myapp.yaml")
	require.NoError(t, err)
	// The entry equals the catalog's, so no state the store keeps for
	// matching reaches the caller.
	require.Equal(t, catalog.Schemas[0], entry)

	entry.FileMatch[0] = "*.json"

	entry, err = store.FindMatch(t.Context(), "/x/myapp.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Generic", entry.Name)
	assert.Equal(t, []string{"*.yaml"}, entry.FileMatch)
}

func BenchmarkStore_FindMatch(b *testing.B) {
	// A path no entry matches makes each lookup try every pattern, as a
	// document outside the catalog does.
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

	server := newCatalogServer(b, catalog)
	b.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	_, err := store.FindMatch(b.Context(), "/repo/unmatched.yaml")
	require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)

	b.ReportAllocs()

	for b.Loop() {
		_, err = store.FindMatch(b.Context(), "/repo/unmatched.yaml")
	}

	require.ErrorIs(b, err, schemastore.ErrNoCatalogMatch)
}

func TestSchemaStore_LazyLoading(t *testing.T) {
	t.Parallel()

	server, fetchCount := newCountingCatalogServer(t, testCatalog)

	// New performs no I/O.
	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	assert.Equal(t, int32(0), fetchCount.Load())

	// First lookup fetches.
	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.NotEmpty(t, entry.Name)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Second lookup uses cache.
	entry, err = store.FindMatch(t.Context(), "other.yaml")
	require.NoError(t, err)
	assert.NotEmpty(t, entry.Name)
	assert.Equal(t, int32(1), fetchCount.Load())
}

func TestSchemaStore_CacheTTL(t *testing.T) {
	t.Parallel()

	server, fetchCount := newCountingCatalogServer(t, testCatalog)

	// Use a very short TTL for testing.
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
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

	server, fetchCount := newCountingCatalogServer(t, testCatalog)

	// TTL of 0 should disable caching (fetch on every lookup).
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
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

	t.Run("serves the previous catalog during a refresh", func(t *testing.T) {
		t.Parallel()

		server, held, release := newHeldCatalogServer(t, 1)

		store := schemastore.New(
			schemastore.WithCatalogURL(server.URL),
			schemastore.WithCacheTTL(10*time.Millisecond),
		)

		entry, err := store.FindMatch(t.Context(), "config.yaml")
		require.NoError(t, err)
		require.Equal(t, "Catalog 1", entry.Name)

		// Wait for cache to expire.
		time.Sleep(20 * time.Millisecond)

		// This lookup starts the refresh, which the server holds.
		refreshing := findMatchAsync(t.Context(), store)
		receive(t, held)

		// A lookup during the refresh gets the previous catalog at once.
		result := receive(t, findMatchAsync(t.Context(), store))
		require.NoError(t, result.err)
		assert.Equal(t, "Catalog 1", result.entry.Name)

		// The lookup that started the refresh waits for it.
		release()

		result = receive(t, refreshing)
		require.NoError(t, result.err)
		assert.Equal(t, "Catalog 2", result.entry.Name)
	})

	t.Run("stops waiting for the first fetch when the context ends", func(t *testing.T) {
		t.Parallel()

		server, held, release := newHeldCatalogServer(t, 0)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

		// This lookup starts the first fetch, which the server holds.
		first := findMatchAsync(t.Context(), store)
		receive(t, held)

		// With no catalog to fall back on, a second lookup waits for the fetch
		// only until its own deadline.
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		result := receive(t, findMatchAsync(ctx, store))
		require.ErrorIs(t, result.err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, result.err, context.DeadlineExceeded)

		release()

		result = receive(t, first)
		require.NoError(t, result.err)
		assert.Equal(t, "Catalog 1", result.entry.Name)
	})

	t.Run("finishes a fetch after its lookup stops waiting", func(t *testing.T) {
		t.Parallel()

		server, held, release := newHeldCatalogServer(t, 0)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		abandoned := findMatchAsync(ctx, store)

		receive(t, held)

		result := receive(t, abandoned)
		require.ErrorIs(t, result.err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, result.err, context.DeadlineExceeded)

		// The fetch outlives the lookup that started it, so the next lookup
		// gets its catalog rather than sending a second request.
		release()

		entry, err := store.FindMatch(t.Context(), "config.yaml")
		require.NoError(t, err)
		assert.Equal(t, "Catalog 1", entry.Name)
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

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	// Use short TTL to trigger concurrent cache refreshes.
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
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

	server := newCatalogServer(t, catalog)
	defer server.Close()

	// Filter to only GitHub schemas.
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
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

				server, fetchCount := newCountingCatalogServer(t, testCatalog)

				return []schemastore.Option{
					schemastore.WithCatalogURL(server.URL),
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

				server, fetchCount := newCountingCatalogServer(t, testCatalog)

				return []schemastore.Option{
					schemastore.WithCatalogURL(server.URL),
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

func TestSchemaStore_HTTPClient(t *testing.T) {
	t.Parallel()

	var headerReceived string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerReceived = r.Header.Get("X-Custom-Header")

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(testCatalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &roundTripperFunc{fn: func(r *http.Request) (*http.Response, error) {
			r.Header.Set("X-Custom-Header", "test-value")

			return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // Test helper.
		}},
	}

	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithHTTPClient(client),
	)

	_, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "test-value", headerReceived)
}

func TestSchemaStore_ZeroRefreshTimeout(t *testing.T) {
	t.Parallel()

	server := newCatalogServer(t, testCatalog)
	t.Cleanup(server.Close)

	// A zero timeout keeps the default rather than expiring every fetch
	// before it starts.
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithRefreshTimeout(0),
	)

	entry, err := store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
}

func TestSchemaStore_NilHTTPClient(t *testing.T) {
	t.Parallel()

	server := newCatalogServer(t, testCatalog)
	t.Cleanup(server.Close)

	// A nil client keeps the default rather than panicking on the first
	// fetch.
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

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(server.Close)

				return []schemastore.Option{schemastore.WithCatalogURL(server.URL)}
			},
		},
		"invalid json": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					//nolint:errcheck // Test helper.
					w.Write([]byte("not json"))
				}))
				t.Cleanup(server.Close)

				return []schemastore.Option{schemastore.WithCatalogURL(server.URL)}
			},
		},
		"invalid URL": {
			setup: func(t *testing.T) []schemastore.Option {
				t.Helper()

				// URL with control character triggers http.NewRequestWithContext error.
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

	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithRetryAfter(20*time.Millisecond),
	)

	// The first lookup fetches and fails.
	_, err := store.FindMatch(t.Context(), "config.yaml")
	require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	assert.Equal(t, int32(1), fetchCount.Load())

	// A lookup inside the retry interval reports the same failure without
	// contacting the server again.
	_, err = store.FindMatch(t.Context(), "config.yaml")
	require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Once the interval passes, the next lookup tries again.
	time.Sleep(30 * time.Millisecond)

	_, err = store.FindMatch(t.Context(), "config.yaml")
	require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	assert.Equal(t, int32(2), fetchCount.Load())
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

			var fetchCount atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fetchCount.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)

			// A non-positive interval keeps the default rather than
			// retrying on every lookup, which costs one refresh timeout
			// each.
			store := schemastore.New(
				schemastore.WithCatalogURL(server.URL),
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

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	// No schemas in catalog means no matches.
	_, err := store.FindMatch(t.Context(), "config.yaml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
}

func TestSchemaStore_CanceledContext(t *testing.T) {
	t.Parallel()

	t.Run("uses cached data without refreshing", func(t *testing.T) {
		t.Parallel()

		server, fetchCount := newCountingCatalogServer(t, testCatalog)

		// Use short TTL so cache expires quickly.
		store := schemastore.New(
			schemastore.WithCatalogURL(server.URL),
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
		assert.Equal(t, int32(1), fetchCount.Load())
	})

	t.Run("reports the cancellation without cached data", func(t *testing.T) {
		t.Parallel()

		server, fetchCount := newCountingCatalogServer(t, testCatalog)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := store.FindMatch(ctx, "config.yaml")
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), fetchCount.Load())

		// A cancellation is not a failed attempt, so the next lookup fetches.
		entry, err := store.FindMatch(t.Context(), "config.yaml")
		require.NoError(t, err)
		assert.Equal(t, "Test", entry.Name)
		assert.Equal(t, int32(1), fetchCount.Load())
	})
}

func TestSchemaStore_RefetchFails(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32

	// Server succeeds first time, fails on subsequent requests.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := requestCount.Add(1)
		if count > 1 {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(testCatalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	// Use short TTL so cache expires quickly.
	store := schemastore.New(
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)

	// First fetch succeeds.
	_, err := store.FindMatch(t.Context(), "config.yaml")
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

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					//nolint:errcheck // Test helper.
					w.Write([]byte(tc.body))
				}))
				t.Cleanup(server.Close)

				store := schemastore.New(schemastore.WithCatalogURL(server.URL))

				_, err := store.FindMatch(t.Context(), "config.yaml")
				require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
				require.NotErrorIs(t, err, schema.ErrNoMatch)
			})

			t.Run("with a cached catalog", func(t *testing.T) {
				t.Parallel()

				catalog, err := json.Marshal(testCatalog)
				require.NoError(t, err)

				var requestCount atomic.Int32

				// The server serves the catalog first and the body after.
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					data := catalog
					if requestCount.Add(1) > 1 {
						data = []byte(tc.body)
					}

					//nolint:errcheck // Test helper.
					w.Write(data)
				}))
				t.Cleanup(server.Close)

				store := schemastore.New(
					schemastore.WithCatalogURL(server.URL),
					schemastore.WithCacheTTL(10*time.Millisecond),
				)

				_, err = store.FindMatch(t.Context(), "config.yaml")
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

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

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

		server := newCatalogServer(t, catalog)
		t.Cleanup(server.Close)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

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

		server := newCatalogServer(t, catalog)
		t.Cleanup(server.Close)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

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

		server := newCatalogServer(t, catalog)
		t.Cleanup(server.Close)

		store := schemastore.New(schemastore.WithCatalogURL(server.URL))

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

		schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		t.Cleanup(schemaServer.Close)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Test Schema",
					URL:       schemaServer.URL + "/schema.json",
					FileMatch: []string{"*.yaml"},
				},
			},
		}

		catalogServer := newCatalogServer(t, catalog)
		t.Cleanup(catalogServer.Close)

		store := schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaServer.URL+"/schema.json", ref.Key())

		data, err := schema.NewRegistry().Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("error when schema URL not found", func(t *testing.T) {
		t.Parallel()

		schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(schemaServer.Close)

		schemaURL := schemaServer.URL + "/schema.json"

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Test Schema",
					URL:       schemaURL,
					FileMatch: []string{"*.yaml"},
				},
			},
		}

		catalogServer := newCatalogServer(t, catalog)
		t.Cleanup(catalogServer.Close)

		store := schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		// The path matches, so Resolve names the schema; only loading it fails,
		// which is not a no-match.
		ref, err := store.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaURL, ref.Key())

		_, err = schema.NewRegistry().Load(t.Context(), ref)
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

		schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		t.Cleanup(schemaServer.Close)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       schemaServer.URL + "/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml", ".github/workflows/*.yml"},
				},
			},
		}

		catalogServer := newCatalogServer(t, catalog)
		t.Cleanup(catalogServer.Close)

		reg := schema.NewRegistry(schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`on: push`), ".github/workflows/ci.yaml")

		err := reg.Validate(t.Context(), doc)
		require.NoError(t, err)
	})

	t.Run("fetches each schema once", func(t *testing.T) {
		t.Parallel()

		var schemaFetches atomic.Int32

		schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			schemaFetches.Add(1)

			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		t.Cleanup(schemaServer.Close)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       schemaServer.URL + "/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml"},
				},
			},
		}

		catalogServer, catalogFetches := newCountingCatalogServer(t, catalog)

		reg := schema.NewRegistry(schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))))

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

		schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		t.Cleanup(schemaServer.Close)

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "GitHub Workflow",
					URL:       schemaServer.URL + "/github-workflow.json",
					FileMatch: []string{".github/workflows/*.yaml", ".github/workflows/*.yml"},
				},
			},
		}

		catalogServer := newCatalogServer(t, catalog)
		t.Cleanup(catalogServer.Close)

		reg := schema.NewRegistry(schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))))

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

		catalogServer := newCatalogServer(t, catalog)
		t.Cleanup(catalogServer.Close)

		reg := schema.NewRegistry(schema.WithResolvers(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL))))

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

func newCatalogServer(tb testing.TB, catalog schemastore.Catalog) *httptest.Server {
	tb.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := json.Marshal(catalog)
		if err != nil {
			tb.Errorf("marshal catalog: %v", err)
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
}

// newCountingCatalogServer serves catalog and counts the requests it
// receives. The server closes with the test.
func newCountingCatalogServer(t *testing.T, catalog schemastore.Catalog) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)

		data, err := json.Marshal(catalog)
		if err != nil {
			t.Errorf("marshal catalog: %v", err)
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	return server, &fetchCount
}

// newHeldCatalogServer serves a one-entry catalog whose pattern matches every
// YAML file and names its entry for the request count, starting with
// "Catalog 1". It answers the first immediate requests at once and holds each
// later one until the caller calls release, sending on held as the request
// arrives. Cleanup releases held requests before the server closes.
func newHeldCatalogServer(t *testing.T, immediate int32) (*httptest.Server, <-chan struct{}, func()) {
	t.Helper()

	var requests atomic.Int32

	held := make(chan struct{}, 10)
	unblock := make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		if n > immediate {
			held <- struct{}{}

			<-unblock
		}

		data, err := json.Marshal(schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{{
				Name:      fmt.Sprintf("Catalog %d", n),
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			}},
		})
		if err != nil {
			t.Errorf("marshal catalog: %v", err)
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))

	// Cleanups run in reverse order, so held requests finish before Close
	// waits for them.
	t.Cleanup(server.Close)
	t.Cleanup(release)

	return server, held, release
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
// within five seconds.
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		//nolint:errcheck // Test helper.
		w.Write([]byte("<html>not json</html>"))
	}))
	t.Cleanup(srv.Close)

	catalogURL := strings.Replace(srv.URL, "http://", "http://svc:hunter2@", 1) + "/catalog.json"
	store := schemastore.New(schemastore.WithCatalogURL(catalogURL), schemastore.WithHTTPClient(srv.Client()))

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

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	entry, err := store.FindMatch(t.Context(), "ci/azure-pipelines.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Azure Pipelines", entry.Name)
	assert.Equal(t, []string{"**/azure-pipelines*.y*ml"}, entry.FileMatch)

	_, err = store.FindMatch(t.Context(), "ci/azure.toml")
	require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
}
