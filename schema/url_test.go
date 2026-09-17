package schema_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		assert.Equal(t, server.URL+"/schema.json", ref.URL)
		assert.Equal(t, 0, requests, "Resolve should name the schema without fetching it")

		_, err = ref.Load(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, requests)
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

	t.Run("with custom client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		customClient := &http.Client{}

		_, data, err := load(t, schema.URL(server.URL+"/schema.json", schema.WithHTTPClient(customClient)))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
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

		_, err = ref.Load(ctx)
		require.Error(t, err)
	})

	t.Run("invalid url", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, schema.URL("\x00")) // Control char makes URL invalid.
		require.ErrorContains(t, err, "create request for")
	})

	t.Run("client error", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, schema.URL("http://localhost:0/schema.json")) // Port 0 = connection refused.
		require.ErrorContains(t, err, "fetch http://localhost:0/schema.json")
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

		_, _, err := load(t, schema.URL("http://example.com/schema.json", schema.WithHTTPClient(client)))
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

		_, _, err := load(t, schema.URL("http://example.com/schema.json", schema.WithHTTPClient(client)))
		require.ErrorContains(t, err, "response exceeds")
	})
}

// infiniteReader provides an unlimited stream of zeros.
type infiniteReader struct{}

func (infiniteReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}
