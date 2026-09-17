package schemastore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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
	require.Equal(t, "Generic", entry.Name)

	entry.FileMatch[0] = "*.json"

	entry, err = store.FindMatch(t.Context(), "/x/myapp.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Generic", entry.Name)
	assert.Equal(t, []string{"*.yaml"}, entry.FileMatch)
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

	// Concurrent lookups before the catalog has loaded share one fetch.
	const goroutines = 10

	var fetchCount atomic.Int32

	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)
		<-release // Hold the fetch open until every goroutine has looked up.

		data, err := json.Marshal(testCatalog)
		if err != nil {
			t.Errorf("marshal catalog: %v", err)
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	store := schemastore.New(schemastore.WithCatalogURL(server.URL))

	var (
		started sync.WaitGroup
		done    sync.WaitGroup
	)

	started.Add(goroutines)
	done.Add(goroutines)

	for range goroutines {
		go func() {
			defer done.Done()

			started.Done()

			entry, err := store.FindMatch(t.Context(), "config.yaml")
			assert.NoError(t, err)
			assert.Equal(t, "Test", entry.Name)
		}()
	}

	started.Wait()
	close(release)
	done.Wait()

	assert.Equal(t, int32(1), fetchCount.Load(), "concurrent lookups should share one fetch")
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

	// Verify refetch was attempted.
	assert.Equal(t, int32(2), requestCount.Load())

	// Inside the retry interval the stale data serves without a request.
	entry, err = store.FindMatch(t.Context(), "config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "Test", entry.Name)
	assert.Equal(t, int32(2), requestCount.Load())
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

	// Entry without URL should be skipped, so random.yaml won't match.
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
		assert.Equal(t, "https://json.schemastore.org/github-workflow.json", ref.URL)
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
		assert.Equal(t, schemaServer.URL+"/schema.json", ref.URL)

		data, err := ref.Load(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("error when schema URL unreachable", func(t *testing.T) {
		t.Parallel()

		catalog := schemastore.Catalog{
			Schemas: []schemastore.CatalogEntry{
				{
					Name:      "Test Schema",
					URL:       "https://example.com/schema.json",
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
		assert.Equal(t, "https://example.com/schema.json", ref.URL)

		_, err = ref.Load(t.Context())
		require.ErrorContains(t, err, "fetch https://example.com/schema.json: status 404")
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestSchemaStore_SchemaFetchTimeout(t *testing.T) {
	t.Parallel()

	// A schema host that accepts the connection and never answers must not
	// outlast the refresh timeout, whatever deadline the caller has.
	blocked := make(chan struct{})
	schemaServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-blocked
	}))

	t.Cleanup(func() {
		close(blocked)
		schemaServer.Close()
	})

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

	store := schemastore.New(
		schemastore.WithCatalogURL(catalogServer.URL),
		schemastore.WithRefreshTimeout(100*time.Millisecond),
	)

	doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

	ref, err := store.Resolve(t.Context(), doc)
	require.NoError(t, err)

	done := make(chan error, 1)

	go func() {
		_, loadErr := ref.Load(context.Background()) //nolint:usetesting // A context with no deadline is the point.
		done <- loadErr
	}()

	select {
	case loadErr := <-done:
		require.ErrorIs(t, loadErr, context.DeadlineExceeded)

	case <-time.After(time.Second):
		t.Fatal("schema fetch outlasted the refresh timeout")
	}
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

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL)))

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

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL)))

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

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL)))

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

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL(catalogServer.URL)))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "random.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("reports an unreachable catalog through the registry", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL("http://localhost:1")))

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("stops before resolvers registered after an unreachable catalog", func(t *testing.T) {
		t.Parallel()

		var fallbackCalls atomic.Int32

		fallback := schema.ResolverFunc(func(context.Context, *niceyaml.Document) (schema.Ref, error) {
			fallbackCalls.Add(1)

			return schema.Ref{}, schema.ErrNoMatch
		})

		reg := schema.NewRegistry()
		reg.Register(schemastore.New(schemastore.WithCatalogURL("http://localhost:1")), fallback)

		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`key: value`), "config.yaml")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
		assert.Zero(t, fallbackCalls.Load(), "the registry should not try resolvers after the store")
	})
}

// Helper functions.

func newCatalogServer(t *testing.T, catalog schemastore.Catalog) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := json.Marshal(catalog)
		if err != nil {
			t.Errorf("marshal catalog: %v", err)
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
// YAML file and whose entry is named for the request count, starting with
// "Catalog 1". It answers the first immediate requests at once and holds each
// later one until release is called, sending on held as the request arrives.
// Cleanup releases held requests before the server closes.
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
func findMatchAsync(ctx context.Context, store *schemastore.SchemaStore) <-chan findMatchResult {
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
