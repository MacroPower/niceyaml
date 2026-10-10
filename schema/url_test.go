package schema_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// schemaHandler answers each request with the body files holds for the
// path of its URL, and with 404 for any other path.
func schemaHandler(files map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		//nolint:errcheck // Test helper.
		w.Write([]byte(data))
	})
}

// serve starts a server that answers through [schemaHandler] and returns
// its URL. The server closes when the test ends.
func serve(t *testing.T, files map[string]string) string {
	t.Helper()

	server := httptest.NewServer(schemaHandler(files))
	t.Cleanup(server.Close)

	return server.URL
}

// memoryClient returns a client that answers through [schemaHandler]
// from memory, and the count of the requests it answered. No server
// listens on a port, so a connection that another process opens cannot
// add to the count.
func memoryClient(files map[string]string) (*http.Client, *atomic.Int32) {
	handler := schemaHandler(files)

	return yamltest.CountingClient(yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)

		return rec.Result(), nil
	}))
}

// nonceServer starts a server that answers one path with a schema and
// returns the URL of that path and the count of the requests for it. The
// path holds a random nonce, so a fetch of the URL counts whatever client
// or context sends it, and a stray request that another process sends to
// a reused port gets 404 and leaves the count alone. The handler never
// blocks, and the server closes each connection after one response and
// closes itself when the test ends.
func nonceServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()

	path := "/" + rand.Text() + "/schema.json"
	handler := schemaHandler(map[string]string{path: `{"type": "object"}`})

	var requests atomic.Int32

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			requests.Add(1)
		}

		handler.ServeHTTP(w, r)
	}))
	server.Config.SetKeepAlivesEnabled(false)
	server.Start()
	t.Cleanup(server.Close)

	return server.URL + path, &requests
}

// respondBody returns a transport function that answers a request with
// status 200 and the bytes body reads.
func respondBody(body io.Reader) yamltest.RoundTripFunc {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body)}, nil
	}
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
		schemaURL := serve(t, map[string]string{"/schema.json": schemaData}) + "/schema.json"

		url, data, err := load(t, schema.URL(schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("resolve does not fetch", func(t *testing.T) {
		t.Parallel()

		// The server closes each connection after one response, so a fetch
		// from the bubble below leaves no idle connection whose reader the
		// bubble would wait on forever.
		schemaURL, requests := nonceServer(t)

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

	t.Run("key", func(t *testing.T) {
		t.Parallel()

		// Every entry point shares one cache key, so URL lowercases the
		// scheme at the start of a reference when "://" follows it and
		// leaves the rest of the URL alone. URL leaves any other reference
		// as written, such as one with no scheme and "://" in its query, or
		// an opaque mailto: URL.
		//
		// Each fragment names a subschema of its own, so the key keeps it.
		// An empty fragment names the whole document, and the key drops
		// it, so both spellings share one cache entry.
		tcs := map[string]struct {
			ref string
			// The key, or empty for a key that is the reference as written.
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
			"no scheme with :// in query":       {ref: "Example.com/Path?u=http://x"},
			"scheme-relative with :// in query": {ref: "//Example.com/Path?u=http://x"},
			"opaque scheme with :// in query":   {ref: "Mailto:User@Example.com?Body=http://x"},
			"json pointer fragment":             {ref: "https://example.com/s.json#/$defs/tasks"},
			"anchor fragment":                   {ref: "https://example.com/s.json#tasks"},
			"empty fragment": {
				ref:  "https://example.com/s.json#",
				want: "https://example.com/s.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, cmp.Or(tc.want, tc.ref), schema.URL(tc.ref).Key())
			})
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		// A Load that ignores its context fetches a valid schema and returns
		// no error, so the checks below fail at once rather than hang.
		schemaURL, requests := nonceServer(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately.

		ref, err := schema.URL(schemaURL).Resolve(ctx, document(t))
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
		reg := schema.NewRegistry()

		file := schema.File(writeSchema(t, "s.json", []byte(`{"type": "object"}`)))
		_, err := reg.Schema(t.Context(), file)
		require.NoError(t, err)

		ref := schema.URL(file.Key())
		require.Equal(t, file.Key(), ref.Key())

		_, err = reg.Schema(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "not an HTTP or HTTPS URL")
	})

	t.Run("failed fetch", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			transport yamltest.RoundTripFunc
			want      string
		}{
			// The transport serves the 404 from memory. The default
			// transport pools the connection of a response with no body
			// before it hands the response over, and a sibling that closes
			// its httptest server closes the idle connections of the default
			// transport. A close at that moment would turn a 404 from a
			// server into a broken connection.
			"not found": {
				transport: yamltest.Respond(http.StatusNotFound, ""),
				want:      "fetch http://example.com/schema.json: status 404",
			},
			// The transport stands in for a refused connection.
			"client error": {
				transport: func(*http.Request) (*http.Response, error) {
					return nil, errors.New("connection refused")
				},
				want: "fetch http://example.com/schema.json: connection refused",
			},
			"body read error": {
				transport: respondBody(iotest.ErrReader(errors.New("read error"))),
				want:      "read response from http://example.com/schema.json",
			},
			"schema exceeds size limit": {
				transport: respondBody(bytes.NewReader(make([]byte, httpfetch.MaxSize+1))),
				want:      "response exceeds",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := lookup(t, tc.transport.Client(), schema.URL("http://example.com/schema.json"))
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorContains(t, err, tc.want)
			})
		}
	})
}
