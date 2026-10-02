package schema_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// countingClient returns a client that counts the requests it sends and
// sends them through the default transport.
func countingClient(requests *atomic.Int32) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)

			return http.DefaultTransport.RoundTrip(r) //nolint:wrapcheck // Test helper.
		}),
	}
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

		// The server counts only requests for a path that holds a random
		// nonce. A fetch of the schema URL counts whatever client or context
		// sends it, and a stray request that another process sends to a
		// reused port gets 404 and leaves the count alone. The server closes
		// each connection after one response, so a fetch from the bubble
		// below leaves no idle connection whose reader the bubble would wait
		// on forever.
		path := "/" + rand.Text() + "/schema.json"

		var requests atomic.Int32

		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			requests.Add(1)

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{}`))
		}))
		server.Config.SetKeepAlivesEnabled(false)
		server.Start()

		defer server.Close()

		schemaURL := server.URL + path

		// Resolve runs in a bubble. A goroutine that waits on the network is
		// not durably blocked, so Wait returns only after a fetch that
		// Resolve left running has finished, before the bubble cancels its
		// context. Test then waits for every goroutine in the bubble to exit
		// and panics when one stays blocked, such as one that sleeps before
		// it fetches.
		var ref schema.Ref

		synctest.Test(t, func(t *testing.T) {
			var err error

			ref, err = schema.URL(schemaURL).Resolve(t.Context(), document(t))
			require.NoError(t, err)

			synctest.Wait()
		})

		assert.Equal(t, schemaURL, ref.Key())
		assert.Equal(t, int32(0), requests.Load(), "Resolve should name the schema without fetching it")

		_, err := schema.NewRegistry().Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, int32(1), requests.Load())
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

		// The transport serves the 404 from memory. The default transport
		// pools the connection of a response with no body before it hands
		// the response over, and a sibling that closes its httptest server
		// closes the idle connections of the default transport. A close at
		// that moment would turn a 404 from a server into a broken
		// connection.
		client := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       http.NoBody,
					Request:    r,
				}, nil
			}),
		}

		err := lookup(t, client, schema.URL("http://example.com/schema.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "fetch http://example.com/schema.json: status 404")
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

		// The handler never blocks. Close waits for every active handler
		// to return, so a stray request that another process sends to a
		// reused port gets an answer, and Close never waits on a client
		// the test does not control. The handler counts only requests for
		// a path that holds a random nonce, and a stray request gets 404
		// and leaves the count alone. A Load that ignores its context
		// fetches a valid schema and returns no error, so the checks
		// below fail at once rather than hang.
		path := "/" + rand.Text() + "/schema.json"

		var requests atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				w.WriteHeader(http.StatusNotFound)

				return
			}

			requests.Add(1)

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"type": "object"}`))
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately.

		ref, err := schema.URL(server.URL+path).Resolve(ctx, document(t))
		require.NoError(t, err)

		_, err = schema.NewRegistry().Load(ctx, ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), requests.Load(), "a canceled Load should not fetch")
	})

	t.Run("invalid url", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, schema.URL("http://\x00")) // Control char makes URL invalid.
		require.ErrorContains(t, err, "parse URL")
	})

	t.Run("url that is not http or https", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			ref string
		}{
			"control character":     {ref: "\x00"},
			"no scheme":             {ref: "example.com/schema.json"},
			"s3 scheme":             {ref: "s3://bucket/schema.json"},
			"file scheme":           {ref: "file:///schemas/schema.json"},
			"uppercase file scheme": {ref: "FILE:///schemas/schema.json"},
			"scheme-relative url with a password starting with a slash": {
				ref: "//user:/secret@example.com/schema.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, _, err := load(t, schema.URL(tc.ref))
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorContains(t, err, "not an HTTP or HTTPS URL")
				assert.NotContains(t, err.Error(), "secret")

				_, err = schema.NewRegistry().Schema(t.Context(), schema.URL(tc.ref))
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorContains(t, err, "not an HTTP or HTTPS URL")
				assert.NotContains(t, err.Error(), "secret")
			})
		}
	})

	t.Run("file url after a File ref cached its key", func(t *testing.T) {
		t.Parallel()

		// A File Ref caches its schema under a file:// URL. A URL Ref with
		// the same key loads nothing, whatever the registry has cached, so
		// a warm registry reports what a cold one does.
		path := filepath.Join(t.TempDir(), "s.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"type": "object"}`), 0o600))

		reg := schema.NewRegistry()

		file := schema.File(path)
		_, err := reg.Schema(t.Context(), file)
		require.NoError(t, err)

		ref := schema.URL(file.Key())
		require.Equal(t, file.Key(), ref.Key())

		_, err = reg.Schema(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "not an HTTP or HTTPS URL")
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
