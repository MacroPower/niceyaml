package schema_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"path"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// bundleLoader serves each URL as the file in files that the last
// segment of its path names, and records each URL that it serves. A URL
// whose file files does not hold fails with [fs.ErrNotExist]. The
// function beside the loader returns the number of loads of each URL.
func bundleLoader(files map[string]string) (schema.LoaderFunc, func() map[string]int) {
	var (
		mu    sync.Mutex
		calls = map[string]int{}
	)

	load := func(_ context.Context, uri string) ([]byte, error) {
		mu.Lock()

		calls[uri]++

		mu.Unlock()

		u, err := url.Parse(uri)
		if err != nil {
			return nil, fs.ErrInvalid
		}

		data, ok := files[path.Base(u.Path)]
		if !ok {
			return nil, fs.ErrNotExist
		}

		return []byte(data), nil
	}

	return load, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()

		return maps.Clone(calls)
	}
}

// refusingClient returns a client that fails the test for every request,
// so a test proves that a loader served each URL.
func refusingClient(t *testing.T) *http.Client {
	t.Helper()

	return yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request for %s", r.URL)

		return nil, fmt.Errorf("unexpected request for %s", r.URL)
	}).Client()
}

func TestWithLoader(t *testing.T) {
	t.Parallel()

	const (
		prefix  = "https://schemas.example.com/v1/"
		defsURL = prefix + "defs.json"
		// A schema whose $id is the URL where its author publishes it.
		// Its relative $ref therefore names a URL under prefix.
		rootSchema = `{
			"$id": "https://schemas.example.com/v1/root.json",
			"properties": {"port": {"$ref": "defs.json#/$defs/port"}}
		}`
		defsSchema = `{"$defs": {"port": {"type": "integer"}}}`
	)

	t.Run("bundle with published ids", func(t *testing.T) {
		t.Parallel()

		bundle := yamltest.MapFS(map[string]string{
			"schemas/root.json": rootSchema,
			"schemas/defs.json": defsSchema,
			"other/a.json":      `{"properties": {"port": {"$ref": "b.json"}}}`,
			"other/b.json":      `{"type": "integer"}`,
		})

		reg := schema.NewRegistry(
			schema.WithAccess(schema.NoAccess),
			schema.WithHTTPClient(refusingClient(t)),
			schema.WithLoader(prefix, func(_ context.Context, uri string) ([]byte, error) {
				return fs.ReadFile(bundle, "schemas/"+path.Base(uri))
			}),
		)

		for _, name := range []string{"schemas/root.json", "other/a.json"} {
			s, err := reg.Schema(t.Context(), schema.FileFS(bundle, name))
			require.NoError(t, err, name)

			require.NoError(t, yamltest.FirstDocument(t, "port: 8080\n").Validate(t.Context(), s), name)
			require.ErrorContains(t, yamltest.FirstDocument(t, "port: x\n").Validate(t.Context(), s), "integer", name)
		}
	})

	t.Run("url ref outside the access", func(t *testing.T) {
		t.Parallel()

		load, calls := bundleLoader(map[string]string{"defs.json": defsSchema})

		reg := schema.NewRegistry(
			schema.WithAccess(schema.NoAccess),
			schema.WithHTTPClient(refusingClient(t)),
			schema.WithLoader(prefix, load),
		)

		// The loader gets the URL without the fragment.
		s, err := reg.Schema(t.Context(), schema.URL(defsURL).WithFragment("/$defs/port"))
		require.NoError(t, err)
		require.NoError(t, yamltest.FirstDocument(t, "8080\n").Validate(t.Context(), s))

		data, err := reg.Load(t.Context(), schema.URL(defsURL))
		require.NoError(t, err)
		assert.JSONEq(t, defsSchema, string(data))

		// Load reads the bytes again on every call.
		assert.Equal(t, map[string]int{defsURL: 2}, calls())
	})

	t.Run("context of the request", func(t *testing.T) {
		t.Parallel()

		type ctxKey struct{}

		var got any

		reg := schema.NewRegistry(schema.WithLoader(prefix, func(ctx context.Context, _ string) ([]byte, error) {
			got = ctx.Value(ctxKey{})

			return []byte(defsSchema), nil
		}))

		ctx := context.WithValue(t.Context(), ctxKey{}, "request")

		_, err := reg.Load(ctx, schema.URL(defsURL))
		require.NoError(t, err)
		assert.Equal(t, "request", got)
	})

	t.Run("url as written", func(t *testing.T) {
		t.Parallel()

		// The $id spells the host of the prefix in mixed case, names the
		// default port, and holds dot segments.
		bundle := yamltest.MapFS(map[string]string{
			"root.json": `{
				"$id": "HTTPS://Schemas.Example.COM:443/v1/sub/../root.json",
				"properties": {"port": {"$ref": "defs.json#/$defs/port"}}
			}`,
		})

		load, calls := bundleLoader(map[string]string{"defs.json": defsSchema})

		reg := schema.NewRegistry(
			schema.WithAccess(schema.NoAccess),
			schema.WithHTTPClient(refusingClient(t)),
			schema.WithLoader(prefix, load),
		)

		// The loader gets the URL of a Ref as the Ref spells it, without
		// its fragment.
		_, err := reg.Load(t.Context(), schema.URL("HTTPS://SCHEMAS.example.com:443/v1/x/../defs.json#/$defs/port"))
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"https://SCHEMAS.example.com:443/v1/x/../defs.json": 1}, calls())

		// A loader that reads the parts of the URL serves the $ref.
		s, err := reg.Schema(t.Context(), schema.FileFS(bundle, "root.json"))
		require.NoError(t, err)
		require.NoError(t, yamltest.FirstDocument(t, "port: 8080\n").Validate(t.Context(), s))
		require.ErrorContains(t, yamltest.FirstDocument(t, "port: x\n").Validate(t.Context(), s), "integer")
	})

	t.Run("matching", func(t *testing.T) {
		t.Parallel()

		// Each loader answers with a schema that names its prefix as a
		// const, so a validation tells which loader served the URL.
		serving := func(name string) schema.LoaderFunc {
			return func(context.Context, string) ([]byte, error) {
				return fmt.Appendf(nil, `{"const": %q}`, name), nil
			}
		}

		// A URL that no loader serves goes to the client of the
		// registry, under an access that allows no URL.
		reg := schema.NewRegistry(
			schema.WithAccess(schema.NoAccess),
			schema.WithHTTPClient(refusingClient(t)),
			schema.WithLoader("https://s.test/", serving("host")),
			schema.WithLoader("https://s.test/v1", serving("replaced")),
			schema.WithLoader("https://s.test/v1", serving("v1")),
			schema.WithLoader("https://s.test/v1/deep/", serving("deep")),
		)

		tcs := map[string]struct {
			url string
			// The loader that serves url, or empty for a url that no
			// loader serves.
			want string
		}{
			"longest prefix wins":                {url: "https://s.test/v1/deep/a.json", want: "deep"},
			"later loader for a prefix wins":     {url: "https://s.test/v1/a.json", want: "v1"},
			"prefix itself":                      {url: "https://s.test/v1", want: "v1"},
			"shorter prefix":                     {url: "https://s.test/v2/a.json", want: "host"},
			"segment boundary":                   {url: "https://s.test/v10/a.json", want: "host"},
			"upper-case host":                    {url: "https://S.TEST/v1/a.json", want: "v1"},
			"default port":                       {url: "https://s.test:443/v1/a.json", want: "v1"},
			"dot segments leave the prefix":      {url: "https://s.test/v1/../v2/a.json", want: "host"},
			"other host":                         {url: "https://s.test.evil.net/v1/a.json"},
			"host after userinfo":                {url: "https://s.test@evil.net/v1/a.json"},
			"other scheme":                       {url: "http://s.test/v1/a.json"},
			"other port":                         {url: "https://s.test:8443/v1/a.json"},
			"query does not change the matching": {url: "https://s.test/v1/a.json?x=1", want: "v1"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				s, err := reg.Schema(t.Context(), schema.URL(tc.url))
				if tc.want == "" {
					require.ErrorIs(t, err, schema.ErrURLNotAllowed)

					return
				}

				require.NoError(t, err)
				require.NoError(t, yamltest.FirstDocument(t, tc.want+"\n").Validate(t.Context(), s))
			})
		}
	})

	t.Run("load limit", func(t *testing.T) {
		t.Parallel()

		load, _ := bundleLoader(map[string]string{
			"root.json": rootSchema,
			"defs.json": defsSchema,
		})

		reg := schema.NewRegistry(
			schema.WithLoadLimit(len(defsSchema)-1),
			schema.WithLoader(prefix, load),
		)

		_, err := reg.Schema(t.Context(), schema.URL(defsURL))
		requireLoadError(t, err, niceyaml.ErrLimit)

		fetchErr, ok := errors.AsType[*schema.FetchError](err)
		require.True(t, ok)
		assert.Equal(t, defsURL, fetchErr.URL)
	})

	t.Run("missing ref document", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			rootURL := prefix + "root.json"
			load, calls := bundleLoader(map[string]string{"root.json": rootSchema})

			reg := schema.NewRegistry(schema.WithLoader(prefix, load))

			for range 3 {
				_, err := reg.Schema(t.Context(), schema.URL(rootURL))
				requireLoadError(t, err, fs.ErrNotExist)
				require.ErrorContains(t, err, "cannot resolve $ref: fetch "+defsURL)

				fetchErr, ok := errors.AsType[*schema.FetchError](err)
				require.True(t, ok)
				assert.Equal(t, defsURL, fetchErr.URL)
			}

			// The registry remembers the failure for the retry interval.
			assert.Equal(t, map[string]int{rootURL: 1, defsURL: 1}, calls())

			time.Sleep(time.Minute)

			_, err := reg.Schema(t.Context(), schema.URL(rootURL))
			requireLoadError(t, err, fs.ErrNotExist)
			assert.Equal(t, map[string]int{rootURL: 2, defsURL: 2}, calls())
		})
	})

	t.Run("url with userinfo", func(t *testing.T) {
		t.Parallel()

		const (
			rootURL = "https://user:secret@s.test/root.json"
			hidden  = "https://user:xxxxx@s.test/"
		)

		files := map[string]string{"root.json": `{"$ref": "defs.json"}`}

		requireHidden := func(t *testing.T, err error, want string) {
			t.Helper()

			fetchErr, ok := errors.AsType[*schema.FetchError](err)
			require.True(t, ok, err)
			assert.Equal(t, want, fetchErr.URL)
			assert.NotContains(t, err.Error(), "secret")
		}

		t.Run("ref document", func(t *testing.T) {
			t.Parallel()

			load, calls := bundleLoader(files)
			reg := schema.NewRegistry(schema.WithLoader("https://s.test/", load))

			// The loader gets the userinfo of the Ref for the Ref and for
			// its $ref, and the error hides the password.
			_, err := reg.Schema(t.Context(), schema.URL(rootURL))
			requireLoadError(t, err, fs.ErrNotExist)
			requireHidden(t, err, hidden+"defs.json")
			assert.Equal(t, map[string]int{
				rootURL:                                1,
				"https://user:secret@s.test/defs.json": 1,
			}, calls())
		})

		t.Run("missing ref", func(t *testing.T) {
			t.Parallel()

			load, _ := bundleLoader(files)
			reg := schema.NewRegistry(schema.WithLoader("https://s.test/", load))

			_, err := reg.Load(t.Context(), schema.URL("https://user:secret@s.test/gone.json"))
			require.ErrorIs(t, err, fs.ErrNotExist)
			requireHidden(t, err, hidden+"gone.json")
		})

		t.Run("load limit", func(t *testing.T) {
			t.Parallel()

			load, _ := bundleLoader(files)
			reg := schema.NewRegistry(
				schema.WithLoadLimit(len(files["root.json"])-1),
				schema.WithLoader("https://s.test/", load),
			)

			_, err := reg.Load(t.Context(), schema.URL(rootURL))
			require.ErrorIs(t, err, niceyaml.ErrLimit)
			requireHidden(t, err, hidden+"root.json")
		})
	})

	t.Run("embedded schema", func(t *testing.T) {
		t.Parallel()

		load, calls := bundleLoader(map[string]string{"defs.json": defsSchema})
		reg := schema.NewRegistry(schema.WithLoader(prefix, load))

		// The $id of the schema lies under the prefix. But a schema from
		// Embedded has no resolver of the registry, so the loader never
		// sees its $ref.
		_, err := reg.Schema(t.Context(), schema.Embedded([]byte(rootSchema)))
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.Empty(t, calls())
	})

	t.Run("refs not required", func(t *testing.T) {
		t.Parallel()

		rootURL := prefix + "root.json"
		load, _ := bundleLoader(map[string]string{"root.json": rootSchema})

		reg := schema.NewRegistry(requireRefs(false), schema.WithLoader(prefix, load))

		s, err := reg.Schema(t.Context(), schema.URL(rootURL))
		require.NoError(t, err)

		err = yamltest.FirstDocument(t, "port: 8080\n").Validate(t.Context(), s)
		require.ErrorIs(t, err, schema.ErrValidate)
		require.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("documents are cached and invalidated", func(t *testing.T) {
		t.Parallel()

		files := map[string]string{
			"a.json":    `{"properties": {"port": {"$ref": "defs.json#/$defs/port"}}}`,
			"b.json":    `{"items": {"$ref": "defs.json#/$defs/port"}}`,
			"defs.json": defsSchema,
		}
		load, calls := bundleLoader(files)

		reg := schema.NewRegistry(schema.WithLoader(prefix, load))

		for _, name := range []string{"a.json", "b.json"} {
			_, err := reg.Schema(t.Context(), schema.URL(prefix+name))
			require.NoError(t, err)
		}

		// The two schemas share one load of the document.
		assert.Equal(t, 1, calls()[defsURL])

		reg.Invalidate(schema.URL(defsURL))

		_, err := reg.Schema(t.Context(), schema.URL(prefix+"a.json"))
		require.NoError(t, err)
		assert.Equal(t, 2, calls()[defsURL])
	})

	t.Run("panics", func(t *testing.T) {
		t.Parallel()

		load, _ := bundleLoader(nil)

		tcs := map[string]struct {
			load   schema.LoaderFunc
			prefix string
			want   string
		}{
			"nil load": {
				prefix: prefix,
				want:   "schema.WithLoader: load is nil",
			},
			"query": {
				prefix: prefix + "?x=1",
				load:   load,
				want:   `schema.WithLoader: "` + prefix + `?x=1": prefix holds a query or a fragment`,
			},
			"not http": {
				prefix: "ftp://schemas.example.com/",
				load:   load,
				want: `schema.WithLoader: "ftp://schemas.example.com/": ` +
					"prefix is not an http or https URL with a host and a valid port",
			},
			"password": {
				prefix: "https://user:secret@schemas.example.com/",
				load:   load,
				want:   `schema.WithLoader: "https://user:xxxxx@schemas.example.com/": prefix holds userinfo`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, tc.want, func() {
					schema.WithLoader(tc.prefix, tc.load)
				})
			})
		}
	})
}
