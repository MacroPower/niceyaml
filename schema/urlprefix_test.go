package schema_test

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// objectSchema is the schema every handler of these tests serves where a
// test needs a schema that compiles.
const objectSchema = `{"type": "object", "$defs": {"a": {"type": "object"}}}`

// recordingClient returns a client that answers each request from memory
// through handler, as [handlerTransport] does, and a function that
// returns the URL of each request the client has sent, in order. No
// request leaves the process, so a test names any host.
func recordingClient(handler http.Handler) (*http.Client, func() []string) {
	var (
		mu   sync.Mutex
		urls []string
	)

	answer := handlerTransport(handler)

	client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()

		urls = append(urls, r.URL.String())
		mu.Unlock()

		return answer(r)
	}).Client()

	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(urls)
	}
}

// anySchema answers every request with [objectSchema].
var anySchema = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	//nolint:errcheck // Test helper.
	w.Write([]byte(objectSchema))
})

// nonceDir starts a server that answers each path of routes, under a
// directory named by a random nonce, through the handler routes holds for
// it. It returns the URL of that directory, with no trailing slash, and
// the count of the requests the routes answered. A stray request that
// another process sends to a reused port then gets 404 and leaves the
// count alone. The server closes when the test ends.
func nonceDir(t *testing.T, routes map[string]http.Handler) (string, *atomic.Int32) {
	t.Helper()

	dir := "/" + rand.Text()
	mux := http.NewServeMux()

	var requests atomic.Int32

	for path, handler := range routes {
		mux.Handle(dir+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			handler.ServeHTTP(w, r)
		}))
	}

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL + dir, &requests
}

func TestWithURLPrefixes(t *testing.T) {
	t.Parallel()

	t.Run("matching", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			prefixes []string
			url      string
			// Whether the registry fetches url.
			want bool
		}{
			"URL under the prefix": {
				prefixes: []string{"https://schemas.test/"},
				url:      "https://schemas.test/app.json",
				want:     true,
			},
			"host in another letter case": {
				prefixes: []string{"https://Schemas.Test/"},
				url:      "https://SCHEMAS.test/app.json",
				want:     true,
			},
			"prefix without a path": {
				prefixes: []string{"https://schemas.test"},
				url:      "https://schemas.test/v1/app.json",
				want:     true,
			},
			"URL without a path": {
				prefixes: []string{"https://schemas.test/"},
				url:      "https://schemas.test",
				want:     true,
			},
			"later prefix": {
				prefixes: []string{"https://other.test/", "https://schemas.test/v1/"},
				url:      "https://schemas.test/v1/app.json",
				want:     true,
			},
			"disallowed host": {
				prefixes: []string{"https://schemas.test/"},
				url:      "https://evil.test/app.json",
			},
			"host that extends the host of the prefix": {
				prefixes: []string{"https://schemas.test"},
				url:      "https://schemas.test.evil.test/app.json",
			},
			"host of the prefix as userinfo": {
				prefixes: []string{"https://schemas.test"},
				url:      "https://schemas.test@evil.test/app.json",
			},
			"host of the prefix as a subdomain": {
				prefixes: []string{"https://schemas.test"},
				url:      "https://evil.schemas.test/app.json",
			},
			"other scheme": {
				prefixes: []string{"https://schemas.test/"},
				url:      "http://schemas.test/app.json",
			},
			"default port written in the URL": {
				prefixes: []string{"http://schemas.test/"},
				url:      "http://schemas.test:80/app.json",
				want:     true,
			},
			"default port written in the prefix": {
				prefixes: []string{"https://schemas.test:443/"},
				url:      "https://schemas.test/app.json",
				want:     true,
			},
			"port with a leading zero": {
				prefixes: []string{"https://schemas.test:8443/"},
				url:      "https://schemas.test:08443/app.json",
				want:     true,
			},
			"port the prefix leaves out": {
				prefixes: []string{"https://schemas.test/"},
				url:      "https://schemas.test:8443/app.json",
			},
			"other port": {
				prefixes: []string{"https://schemas.test:8443/"},
				url:      "https://schemas.test:9443/app.json",
			},
			"path under a prefix without a trailing slash": {
				prefixes: []string{"https://schemas.test/v1"},
				url:      "https://schemas.test/v1/app.json",
				want:     true,
			},
			"path equal to a prefix without a trailing slash": {
				prefixes: []string{"https://schemas.test/v1"},
				url:      "https://schemas.test/v1",
				want:     true,
			},
			"path that extends the last segment of the prefix": {
				prefixes: []string{"https://schemas.test/v1"},
				url:      "https://schemas.test/v10/app.json",
			},
			"path without the trailing slash of the prefix": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/v1",
			},
			"path in another letter case": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/V1/app.json",
			},
			"dot segment that stays under the prefix": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/v1/./a/../app.json",
				want:     true,
			},
			"dot segment that leaves the prefix": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/v1/../admin",
			},
			"dot segment that enters the prefix": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/admin/../v1/app.json",
			},
			"escaped dot segment that leaves the prefix": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/v1/%2e%2E/admin",
			},
			"escaped slash after the prefix": {
				prefixes: []string{"https://schemas.test/v1"},
				url:      "https://schemas.test/v1%2F..%2Fadmin",
			},
			"escaped unreserved character": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/%761/app.json",
				want:     true,
			},
			"query and fragment": {
				prefixes: []string{"https://schemas.test/v1/"},
				url:      "https://schemas.test/v1/app.json?v=2#/$defs/a",
				want:     true,
			},
			"userinfo on the host of the prefix": {
				prefixes: []string{"https://schemas.test/"},
				url:      "https://user:secret@schemas.test/app.json",
				want:     true,
			},
			"no prefixes": {
				url: "https://schemas.test/app.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(anySchema)
				reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithURLPrefixes(tc.prefixes...))

				_, err := reg.Schema(t.Context(), schema.URL(tc.url))

				if tc.want {
					require.NoError(t, err)
					assert.Len(t, requests(), 1)

					return
				}

				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorIs(t, err, schema.ErrURLNotAllowed)
				assert.False(t, niceyaml.IsInvalid(err))
				assert.Empty(t, requests())
			})
		}
	})

	t.Run("without the option the registry fetches any URL", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(anySchema)
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		_, err := reg.Schema(t.Context(), schema.URL("https://evil.test/app.json"))
		require.NoError(t, err)
		assert.Equal(t, []string{"https://evil.test/app.json"}, requests())
	})

	t.Run("each call adds its prefixes", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(anySchema)
		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithURLPrefixes(),
			schema.WithURLPrefixes("https://a.test/"),
			schema.WithURLPrefixes("https://b.test/"),
		)

		for _, u := range []string{"https://a.test/s.json", "https://b.test/s.json"} {
			_, err := reg.Schema(t.Context(), schema.URL(u))
			require.NoError(t, err)
		}

		_, err := reg.Schema(t.Context(), schema.URL("https://c.test/s.json"))
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)
		assert.Equal(t, []string{"https://a.test/s.json", "https://b.test/s.json"}, requests())
	})

	t.Run("a server on another port is outside the prefix", func(t *testing.T) {
		t.Parallel()

		routes := map[string]http.Handler{"/s.json": anySchema}
		allowed, allowedRequests := nonceDir(t, routes)
		other, otherRequests := nonceDir(t, routes)

		reg := schema.NewRegistry(schema.WithURLPrefixes(allowed + "/"))

		_, err := reg.Schema(t.Context(), schema.URL(allowed+"/s.json"))
		require.NoError(t, err)

		_, err = reg.Schema(t.Context(), schema.URL(other+"/s.json"))
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)

		assert.Equal(t, int32(1), allowedRequests.Load())
		assert.Zero(t, otherRequests.Load())
	})

	t.Run("Load refuses a URL outside the prefixes", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(anySchema)
		reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithURLPrefixes("https://schemas.test/"))

		_, err := reg.Load(t.Context(), schema.URL("https://user:secret@evil.test/app.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)
		assert.Equal(t,
			`load schema: "https://user:xxxxx@evil.test/app.json": URL not under an allowed prefix`, err.Error())
		assert.Empty(t, requests())
	})

	t.Run("a directive names a URL outside the prefixes", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(anySchema)
		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithURLPrefixes("https://schemas.test/"),
			schema.WithResolvers(schema.Directive()),
		)

		requireAccepts(t, reg, withDirective("https://schemas.test/app.json", "a: 1\n"))

		err := reg.Check(t.Context(), yamltest.FirstDocument(t, withDirective("https://evil.test/app.json", "a: 1\n")))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)
		assert.False(t, niceyaml.IsInvalid(err))
		assert.Equal(t, []string{"https://schemas.test/app.json"}, requests())
	})

	t.Run("file reads stay outside the limit", func(t *testing.T) {
		t.Parallel()

		bundle := mapFS(map[string]string{"s.json": objectSchema})
		reg := schema.NewRegistry(schema.WithURLPrefixes())

		_, err := reg.Schema(t.Context(), schema.FileFS(bundle, "s.json"))
		require.NoError(t, err)
	})

	t.Run("$ref", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The $ref of the schema at https://schemas.test/v1/s.json.
			ref string
			// Whether the registry fetches the document ref names.
			want bool
		}{
			"under the prefix":                      {ref: "defs.json", want: true},
			"on a disallowed host":                  {ref: "https://evil.test/defs.json"},
			"on the host outside the path":          {ref: "/admin/defs.json"},
			"through the parent directory":          {ref: "../admin/defs.json"},
			"on the host of the prefix as userinfo": {ref: "https://schemas.test@evil.test/defs.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := objectSchema
					if r.URL.Path == "/v1/s.json" {
						body = `{"properties": {"a": {"$ref": "` + tc.ref + `"}}}`
					}

					//nolint:errcheck // Test helper.
					w.Write([]byte(body))
				}))

				reg := schema.NewRegistry(
					schema.WithHTTPClient(client),
					schema.WithURLPrefixes("https://schemas.test/v1/"),
				)

				_, err := reg.Schema(t.Context(), schema.URL("https://schemas.test/v1/s.json"))

				if tc.want {
					require.NoError(t, err)
					assert.Len(t, requests(), 2)

					return
				}

				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorIs(t, err, schema.ErrURLNotAllowed)
				assert.Equal(t, []string{"https://schemas.test/v1/s.json"}, requests())
			})
		}
	})

	t.Run("$ref in a schema that compiles without it", func(t *testing.T) {
		t.Parallel()

		client, requests := recordingClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"properties": {"a": {"$ref": "https://evil.test/defs.json"}}}`))
		}))

		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithURLPrefixes("https://schemas.test/"),
			requireRefs(false),
			schema.WithResolvers(schema.URL("https://schemas.test/s.json")),
		)

		requireAccepts(t, reg, "b: 1\n")

		err := reg.Check(t.Context(), yamltest.FirstDocument(t, "a: 1\n"))
		require.ErrorIs(t, err, schema.ErrValidate)
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)
		assert.Equal(t, []string{"https://schemas.test/s.json"}, requests())
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()

		elsewhere, elsewhereRequests := nonceDir(t, map[string]http.Handler{"/s.json": anySchema})

		tcs := map[string]struct {
			// The Location of the redirect from /v1/s.json.
			location string
			// Whether the registry follows the redirect.
			want bool
		}{
			"under the prefix":            {location: "/v1/other.json", want: true},
			"outside the path":            {location: "/admin/s.json"},
			"to another host":             {location: "https://evil.test/v1/s.json"},
			"to a server on another port": {location: elsewhere + "/s.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				client, requests := recordingClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/s.json" {
						http.Redirect(w, r, tc.location, http.StatusFound)

						return
					}

					anySchema.ServeHTTP(w, r)
				}))

				reg := schema.NewRegistry(
					schema.WithHTTPClient(client),
					schema.WithURLPrefixes("https://schemas.test/v1/"),
				)

				_, err := reg.Schema(t.Context(), schema.URL("https://schemas.test/v1/s.json"))

				if tc.want {
					require.NoError(t, err)
					assert.Equal(t,
						[]string{"https://schemas.test/v1/s.json", "https://schemas.test/v1/other.json"}, requests())

					return
				}

				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorIs(t, err, schema.ErrURLNotAllowed)
				assert.Contains(t, err.Error(), "redirect: URL not under an allowed prefix")
				assert.Equal(t, []string{"https://schemas.test/v1/s.json"}, requests())
			})
		}

		assert.Zero(t, elsewhereRequests.Load())
	})

	t.Run("redirect between servers", func(t *testing.T) {
		t.Parallel()

		elsewhere, elsewhereRequests := nonceDir(t, map[string]http.Handler{"/s.json": anySchema})
		allowed, _ := nonceDir(t, map[string]http.Handler{
			"/s.json": http.RedirectHandler(elsewhere+"/s.json", http.StatusFound),
		})

		reg := schema.NewRegistry(schema.WithURLPrefixes(allowed + "/"))

		_, err := reg.Schema(t.Context(), schema.URL(allowed+"/s.json"))
		require.ErrorIs(t, err, schema.ErrURLNotAllowed)
		assert.Zero(t, elsewhereRequests.Load())
	})

	t.Run("CheckRedirect of the client", func(t *testing.T) {
		t.Parallel()

		errStop := errors.New("redirect stopped by the client")

		redirecting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/in":
				http.Redirect(w, r, "/s.json", http.StatusFound)
			case "/out":
				http.Redirect(w, r, "https://evil.test/s.json", http.StatusFound)
			case "/loop":
				http.Redirect(w, r, "/loop", http.StatusFound)
			default:
				anySchema.ServeHTTP(w, r)
			}
		})

		t.Run("runs for a redirect under the prefix", func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			client, _ := recordingClient(redirecting)
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				calls.Add(1)

				return errStop
			}

			reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithURLPrefixes("https://schemas.test/"))

			_, err := reg.Schema(t.Context(), schema.URL("https://schemas.test/in"))
			require.ErrorIs(t, err, errStop)
			assert.Equal(t, int32(1), calls.Load())

			// The policy refuses a redirect before the client sees it.
			_, err = reg.Schema(t.Context(), schema.URL("https://schemas.test/out"))
			require.ErrorIs(t, err, schema.ErrURLNotAllowed)
			assert.Equal(t, int32(1), calls.Load())
		})

		t.Run("defaults to ten redirects", func(t *testing.T) {
			t.Parallel()

			client, requests := recordingClient(redirecting)
			reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithURLPrefixes("https://schemas.test/"))

			_, err := reg.Schema(t.Context(), schema.URL("https://schemas.test/loop"))
			require.ErrorIs(t, err, schema.ErrLoad)
			require.ErrorContains(t, err, "stopped after 10 redirects")
			assert.Len(t, requests(), 10)

			// The registry fetches with a copy and leaves the client as it
			// was.
			assert.Nil(t, client.CheckRedirect)
		})
	})

	t.Run("Timeout of the client bounds each fetch", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// The transport answers when the request's context ends, which
			// the Timeout of the client sets.
			client := &http.Client{
				Timeout: time.Second,
				Transport: yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
					<-r.Context().Done()

					return nil, r.Context().Err()
				}),
			}

			reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithURLPrefixes("https://schemas.test/"))

			start := time.Now()

			_, err := reg.Schema(t.Context(), schema.URL("https://schemas.test/s.json"))
			// The context of the lookup has no deadline, so the deadline
			// came from the Timeout.
			require.ErrorIs(t, err, schema.ErrLoad)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, time.Second, time.Since(start))
		})
	})

	t.Run("panics on an invalid prefix", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			prefix string
		}{
			"empty":                     {prefix: ""},
			"no scheme":                 {prefix: "schemas.test/v1/"},
			"other scheme":              {prefix: "ftp://schemas.test/"},
			"file URL":                  {prefix: "file:///schemas/"},
			"no host":                   {prefix: "https:///v1/"},
			"opaque":                    {prefix: "https:schemas.test"},
			"userinfo":                  {prefix: "https://user:secret@schemas.test/"},
			"query":                     {prefix: "https://schemas.test/v1/?a=1"},
			"fragment":                  {prefix: "https://schemas.test/v1/#a"},
			"empty fragment":            {prefix: "https://schemas.test/v1/#"},
			"port out of range":         {prefix: "https://schemas.test:65536/"},
			"port that is not a number": {prefix: "https://schemas.test:x/"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Panics(t, func() { schema.WithURLPrefixes("https://schemas.test/", tc.prefix) })
			})
		}

		// The message hides a password.
		assert.PanicsWithValue(t,
			`schema.WithURLPrefixes: "https://user:xxxxx@schemas.test/": prefix holds userinfo`,
			func() { schema.WithURLPrefixes("https://user:secret@schemas.test/") })
	})
}
