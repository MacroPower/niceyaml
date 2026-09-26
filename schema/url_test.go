package schema_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestURL(t *testing.T) {
	t.Parallel()

	t.Run("empty URL", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.URL: schema URL is empty", func() {
			schema.URL("")
		})
	})

	t.Run("successful fetch", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		url, data, err := load(t, schema.URL(server.URL+"/schema.json"))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, server.URL+"/schema.json", url)
	})

	t.Run("resolve does not fetch", func(t *testing.T) {
		t.Parallel()

		var requests int

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{}`))
		}))
		defer server.Close()

		ref, err := schema.URL(server.URL+"/schema.json").Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Equal(t, server.URL+"/schema.json", ref.Key())
		assert.Equal(t, 0, requests, "Resolve should name the schema without fetching it")

		_, err = schema.NewRegistry().Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, 1, requests)
	})

	t.Run("scheme is lowercased", func(t *testing.T) {
		t.Parallel()

		// Every entry point shares one cache key, so URL lowercases the
		// scheme at the start of a reference when "://" follows it and
		// leaves the rest of the URL alone. URL leaves any other reference
		// as written, such as one with no scheme and "://" in its query, or
		// an opaque mailto: URL.
		tcs := map[string]struct {
			ref  string
			want string
		}{
			"uppercase http scheme": {
				ref:  "HTTP://Example.COM/Schema.json",
				want: "http://Example.COM/Schema.json",
			},
			"mixed-case http scheme": {
				ref:  "HtTp://Example.COM/Schema.json",
				want: "http://Example.COM/Schema.json",
			},
			"uppercase https scheme": {
				ref:  "HTTPS://Example.COM/Schema.json",
				want: "https://Example.COM/Schema.json",
			},
			"custom scheme is lowercased": {
				ref:  "S3://Bucket/Key",
				want: "s3://Bucket/Key",
			},
			"no scheme with :// in query": {
				ref:  "Example.com/Path?u=http://x",
				want: "Example.com/Path?u=http://x",
			},
			"scheme-relative with :// in query": {
				ref:  "//Example.com/Path?u=http://x",
				want: "//Example.com/Path?u=http://x",
			},
			"opaque scheme with :// in query": {
				ref:  "Mailto:User@Example.com?Body=http://x",
				want: "Mailto:User@Example.com?Body=http://x",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ref, err := schema.URL(tc.ref).Resolve(t.Context(), document(t))
				require.NoError(t, err)
				assert.Equal(t, tc.want, ref.Key())
			})
		}
	})

	t.Run("fragment kept in key", func(t *testing.T) {
		t.Parallel()

		// Each fragment names a subschema of its own, so the key keeps it.
		// An empty fragment names the whole document, and the key drops
		// it, so both spellings share one cache entry.
		tcs := map[string]struct {
			ref  string
			want string
		}{
			"json pointer": {
				ref:  "https://example.com/s.json#/$defs/tasks",
				want: "https://example.com/s.json#/$defs/tasks",
			},
			"anchor": {
				ref:  "https://example.com/s.json#tasks",
				want: "https://example.com/s.json#tasks",
			},
			"empty fragment": {
				ref:  "https://example.com/s.json#",
				want: "https://example.com/s.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, schema.URL(tc.ref).Key())
			})
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		_, _, err := load(t, schema.URL(server.URL+"/schema.json"))
		require.ErrorContains(t, err, "fetch "+server.URL+"/schema.json: status 404")
	})

	t.Run("registry fetches with its client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		var requests atomic.Int32

		err := lookup(t, countingClient(&requests), schema.URL(server.URL+"/schema.json"))
		require.NoError(t, err)
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("registry with nil client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		// A nil client keeps the default rather than panicking on the fetch.
		err := lookup(t, nil, schema.URL(server.URL+"/schema.json"))
		require.NoError(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			// Block forever.
			select {}
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately.

		ref, err := schema.URL(server.URL+"/schema.json").Resolve(ctx, document(t))
		require.NoError(t, err)

		_, err = schema.NewRegistry().Load(ctx, ref)
		require.Error(t, err)
	})

	t.Run("invalid url", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, schema.URL("\x00")) // Control char makes URL invalid.
		require.ErrorContains(t, err, "parse URL")
	})

	t.Run("client error", func(t *testing.T) {
		t.Parallel()

		// The transport stands in for a refused connection.
		client := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			}),
		}

		err := lookup(t, client, schema.URL("http://example.com/schema.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "fetch http://example.com/schema.json: connection refused")
	})

	t.Run("body read error", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(errReader{}),
				}, nil
			}),
		}

		err := lookup(t, client, schema.URL("http://example.com/schema.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "read response from http://example.com/schema.json")
	})

	t.Run("schema exceeds size limit", func(t *testing.T) {
		t.Parallel()

		const maxSchemaSize = 10 * 1024 * 1024 // Must match httpfetch.MaxSize.

		// Create a reader that provides data beyond the limit.
		client := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				// Return a reader that provides exactly maxSchemaSize + 1 bytes.
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(io.LimitReader(infiniteReader{}, maxSchemaSize+1)),
				}, nil
			}),
		}

		err := lookup(t, client, schema.URL("http://example.com/schema.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "response exceeds")
	})
}

// infiniteReader provides an unlimited stream of zeros.
type infiniteReader struct{}

func (infiniteReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}
