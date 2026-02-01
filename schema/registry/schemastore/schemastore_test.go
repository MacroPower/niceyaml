package schemastore_test

import (
	"context"
	"encoding/json"
	"errors"
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

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/registry"
	"go.jacobcolvin.com/niceyaml/schema/registry/schemastore"
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
		},
	}

	tcs := map[string]struct {
		filePath string
		wantName string
		wantNil  bool
	}{
		"matches github workflow yaml": {
			filePath: ".github/workflows/ci.yaml",
			wantName: "GitHub Workflow",
		},
		"matches github workflow yml": {
			filePath: ".github/workflows/build.yml",
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
			wantNil:  true,
		},
		"empty file path": {
			filePath: "",
			wantNil:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Each parallel test gets its own server.
			server := newCatalogServer(t, catalog)
			t.Cleanup(server.Close)

			store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
			require.NoError(t, err)

			entry, ok := store.FindMatch(t.Context(), tc.filePath)

			if tc.wantNil {
				assert.False(t, ok)
			} else {
				require.True(t, ok)
				assert.Equal(t, tc.wantName, entry.Name)
			}
		})
	}
}

func TestSchemaStore_EagerLoading(t *testing.T) {
	t.Parallel()

	var fetchCount atomic.Int32

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(catalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	defer server.Close()

	// Fetch happens during construction.
	store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
	require.NoError(t, err)
	assert.Equal(t, int32(1), fetchCount.Load())

	// First lookup uses cache.
	entry, ok := store.FindMatch(t.Context(), "config.yaml")
	require.True(t, ok)
	assert.NotEmpty(t, entry.Name)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Second lookup uses cache.
	entry, ok = store.FindMatch(t.Context(), "other.yaml")
	require.True(t, ok)
	assert.NotEmpty(t, entry.Name)
	assert.Equal(t, int32(1), fetchCount.Load())
}

func TestSchemaStore_CacheTTL(t *testing.T) {
	t.Parallel()

	var fetchCount atomic.Int32

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(catalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	defer server.Close()

	// Use a very short TTL for testing.
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)
	require.NoError(t, err)

	// Construction triggers fetch.
	assert.Equal(t, int32(1), fetchCount.Load())

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// Lookup triggers refetch due to expired cache.
	_, _ = store.FindMatch(t.Context(), "config.yaml")
	assert.Equal(t, int32(2), fetchCount.Load())
}

func TestSchemaStore_NoCaching(t *testing.T) {
	t.Parallel()

	var fetchCount atomic.Int32

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount.Add(1)

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(catalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	defer server.Close()

	// TTL of 0 should disable caching (fetch on every lookup).
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(0),
	)
	require.NoError(t, err)

	// Construction triggers initial fetch.
	assert.Equal(t, int32(1), fetchCount.Load())

	// First lookup should refetch (no caching).
	_, _ = store.FindMatch(t.Context(), "config.yaml")
	assert.Equal(t, int32(2), fetchCount.Load())

	// Second lookup should also refetch.
	_, _ = store.FindMatch(t.Context(), "other.yaml")
	assert.Equal(t, int32(3), fetchCount.Load())

	// Third lookup should also refetch.
	_, _ = store.FindMatch(t.Context(), "another.yaml")
	assert.Equal(t, int32(4), fetchCount.Load())
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
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(1*time.Millisecond),
	)
	require.NoError(t, err)

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
					_, _ = store.FindMatch(t.Context(), path)
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
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithFilter(func(e schemastore.CatalogEntry) bool {
			return strings.Contains(strings.ToLower(e.Name), "github")
		}),
	)
	require.NoError(t, err)

	// GitHub workflow should match.
	entry, ok := store.FindMatch(t.Context(), ".github/workflows/ci.yaml")
	require.True(t, ok)
	assert.Equal(t, "GitHub Workflow", entry.Name)

	// Docker Compose should not match due to filter.
	_, ok = store.FindMatch(t.Context(), "docker-compose.yaml")
	assert.False(t, ok)
}

func TestSchemaStore_HTTPClient(t *testing.T) {
	t.Parallel()

	var headerReceived string

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerReceived = r.Header.Get("X-Custom-Header")

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(catalog)

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

	_, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithHTTPClient(client),
	)
	require.NoError(t, err)
	assert.Equal(t, "test-value", headerReceived)
}

func TestSchemaStore_FetchError(t *testing.T) {
	t.Parallel()

	t.Run("server error", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		_, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte("not json"))
		}))
		defer server.Close()

		_, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	})

	t.Run("invalid URL", func(t *testing.T) {
		t.Parallel()

		// URL with control character triggers http.NewRequestWithContext error.
		_, err := schemastore.New(t.Context(), schemastore.WithCatalogURL("http://\x00invalid"))
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	})

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()

		// Port 1 is a privileged port that won't be listening.
		_, err := schemastore.New(t.Context(), schemastore.WithCatalogURL("http://localhost:1"))
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	})

	t.Run("read body error", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{
			Transport: &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(&errorReader{}),
				}, nil
			}},
		}

		_, err := schemastore.New(
			t.Context(),
			schemastore.WithCatalogURL("http://example.com/catalog.json"),
			schemastore.WithHTTPClient(client),
		)
		require.ErrorIs(t, err, schemastore.ErrFetchCatalog)
	})
}

func TestSchemaStore_EmptyCatalog(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{},
	}

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
	require.NoError(t, err)

	// No schemas in catalog means no matches.
	_, ok := store.FindMatch(t.Context(), "config.yaml")
	assert.False(t, ok)
}

func TestSchemaStore_CanceledContext(t *testing.T) {
	t.Parallel()

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	server := newCatalogServer(t, catalog)
	t.Cleanup(server.Close)

	// Use short TTL so cache expires quickly.
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)
	require.NoError(t, err)

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// Create a canceled context.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// FindMatch should skip refresh with canceled context but still return cached data.
	entry, ok := store.FindMatch(ctx, "config.yaml")
	require.True(t, ok)
	assert.Equal(t, "Test", entry.Name)
}

func TestSchemaStore_RefetchFails(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32

	catalog := schemastore.Catalog{
		Schemas: []schemastore.CatalogEntry{
			{
				Name:      "Test",
				URL:       "https://example.com/test.json",
				FileMatch: []string{"*.yaml"},
			},
		},
	}

	// Server succeeds first time, fails on subsequent requests.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := requestCount.Add(1)
		if count > 1 {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		//nolint:errcheck // Test data is static and valid.
		data, _ := json.Marshal(catalog)

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	// Use short TTL so cache expires quickly.
	store, err := schemastore.New(
		t.Context(),
		schemastore.WithCatalogURL(server.URL),
		schemastore.WithCacheTTL(10*time.Millisecond),
	)
	require.NoError(t, err)

	// First fetch succeeded.
	assert.Equal(t, int32(1), requestCount.Load())

	// Wait for cache to expire.
	time.Sleep(20 * time.Millisecond)

	// FindMatch triggers refetch which fails, but still returns cached data.
	entry, ok := store.FindMatch(t.Context(), "config.yaml")
	require.True(t, ok)
	assert.Equal(t, "Test", entry.Name)

	// Verify refetch was attempted.
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

	store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
	require.NoError(t, err)

	// Entry without URL should be skipped, so random.yaml won't match.
	_, ok := store.FindMatch(t.Context(), "random.yaml")
	assert.False(t, ok)

	// Entry with URL should still match.
	entry, ok := store.FindMatch(t.Context(), "config.yaml")
	require.True(t, ok)
	assert.Equal(t, "Has URL", entry.Name)
}

func TestSchemaStore_MatchLoader(t *testing.T) {
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`on: push`), ".github/workflows/ci.yaml")
		assert.True(t, store.Match(t.Context(), doc))
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "random.yaml")
		assert.False(t, store.Match(t.Context(), doc))
	})

	t.Run("loads matching schema after match", func(t *testing.T) {
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "config.yaml")

		// Match first, then Load.
		require.True(t, store.Match(t.Context(), doc))

		result, err := store.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
		assert.Equal(t, schemaServer.URL+"/schema.json", result.URL)
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "config.yaml")

		// Match succeeds but Load fails because schema URL is unreachable.
		require.True(t, store.Match(t.Context(), doc))

		_, err = store.Load(t.Context(), doc)
		require.ErrorContains(t, err, "fetch https://example.com/schema.json: status 404")
	})

	t.Run("load without prior match", func(t *testing.T) {
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "config.yaml")

		// Load can be called without prior Match.
		result, err := store.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
	})

	t.Run("error when no matching schema", func(t *testing.T) {
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(server.URL))
		require.NoError(t, err)

		// Document path doesn't match any schema pattern.
		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "random.yaml")

		_, err = store.Load(t.Context(), doc)
		require.ErrorIs(t, err, schemastore.ErrNoCatalogMatch)
		require.ErrorContains(t, err, "random.yaml")
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		reg := registry.New()
		reg.Register(store)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`on: push`), ".github/workflows/ci.yaml")

		err = reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		reg := registry.New()
		reg.Register(store)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`name: test`), ".github/workflows/ci.yaml")

		err = reg.ValidateDocument(t.Context(), doc)
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

		store, err := schemastore.New(t.Context(), schemastore.WithCatalogURL(catalogServer.URL))
		require.NoError(t, err)

		reg := registry.New()
		reg.Register(store)

		doc := yamltest.FirstDocumentWithPath(t, yamltest.Input(`key: value`), "random.yaml")

		err = reg.ValidateDocument(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoMatch)
	})
}

// Helper functions.

func newCatalogServer(t *testing.T, catalog schemastore.Catalog) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := json.Marshal(catalog)
		if err != nil {
			t.Fatalf("marshal catalog: %v", err)
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
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
