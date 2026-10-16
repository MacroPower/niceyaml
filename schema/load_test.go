package schema_test

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// fetchErrors returns every [*schema.FetchError] on the tree of err, in
// the order a walk of its causes finds them. The walk visits each branch
// of a join.
func fetchErrors(err error) []*schema.FetchError {
	var found []*schema.FetchError

	var walk func(error)

	walk = func(err error) {
		if fetchErr, ok := err.(*schema.FetchError); ok { //nolint:errorlint // Walks the tree one node at a time.
			found = append(found, fetchErr)
		}

		switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
		case interface{ Unwrap() error }:
			next := x.Unwrap()
			if next != nil {
				walk(next)
			}

		case interface{ Unwrap() []error }:
			for _, branch := range x.Unwrap() {
				walk(branch)
			}
		}
	}

	walk(err)

	return found
}

// routes returns a [yamltest.RoundTripFunc] that answers each URL path
// in bodies with status 200 and its body, and each path in statuses with
// its status and no body. Any other path answers with status 404.
func routes(bodies map[string]string, statuses map[string]int) yamltest.RoundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		if body, ok := bodies[r.URL.Path]; ok {
			return yamltest.Respond(http.StatusOK, body)(r)
		}

		if status, ok := statuses[r.URL.Path]; ok {
			return yamltest.Respond(status, "")(r)
		}

		return yamltest.Respond(http.StatusNotFound, "")(r)
	}
}

func TestWithLoadLimit(t *testing.T) {
	t.Parallel()

	const limit = 64

	var (
		small   = `{"type": "object"}`
		bigJSON = small + strings.Repeat(" ", limit)
		bigYAML = "type: object\n" + strings.Repeat("#", limit) + "\n"
	)

	fsys := yamltest.MapFS(map[string]string{
		"small.json": small,
		"big.json":   bigJSON,
		"big.yaml":   bigYAML,
		"refs.json":  `{"properties": {"a": {"$ref": "big.json"}}}`,
	})

	client := routes(map[string]string{
		"/big.json":   bigJSON,
		"/small.json": small,
		"/refs.json":  `{"properties": {"a": {"$ref": "big.json"}}}`,
	}, nil).Client()

	tcs := map[string]struct {
		// The sentinel the error matches, or nil for a schema that loads.
		err  error
		ref  schema.Ref
		opts []schema.RegistryOption
		// The status of the FetchError, or -1 for an error with none.
		status int
	}{
		"file within the limit": {
			ref: schema.FileFS(fsys, "small.json"),
		},
		"file past the limit": {
			ref:    schema.FileFS(fsys, "big.json"),
			err:    schema.ErrLoad,
			status: 0,
		},
		"YAML file past the limit": {
			ref:    schema.FileFS(fsys, "big.yaml"),
			err:    schema.ErrLoad,
			status: 0,
		},
		"$ref document past the limit": {
			ref:    schema.FileFS(fsys, "refs.json"),
			err:    schema.ErrLoad,
			status: 0,
		},
		"URL within the limit": {
			ref: schema.URL("https://example.com/small.json"),
		},
		"URL past the limit": {
			ref:    schema.URL("https://example.com/big.json"),
			err:    schema.ErrLoad,
			status: http.StatusOK,
		},
		"$ref URL past the limit": {
			ref:    schema.URL("https://example.com/refs.json"),
			err:    schema.ErrLoad,
			status: http.StatusOK,
		},
		"embedded YAML past the limit": {
			ref:    schema.Embedded([]byte(bigYAML)),
			err:    schema.ErrCompile,
			status: -1,
		},
		"embedded JSON past the limit": {
			ref: schema.Embedded([]byte(bigJSON)),
		},
		"zero after a limit keeps that limit": {
			ref:    schema.FileFS(fsys, "big.json"),
			opts:   []schema.RegistryOption{schema.WithLoadLimit(0)},
			err:    schema.ErrLoad,
			status: 0,
		},
		"later limit replaces an earlier one": {
			ref:  schema.FileFS(fsys, "big.json"),
			opts: []schema.RegistryOption{schema.WithLoadLimit(1 << 20)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]schema.RegistryOption{
				schema.WithHTTPClient(client),
				schema.WithLoadLimit(limit),
			}, tc.opts...)

			_, err := schema.NewRegistry(opts...).Schema(t.Context(), tc.ref)
			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.err)
			require.ErrorIs(t, err, niceyaml.ErrLimit)
			assert.False(t, niceyaml.IsInvalid(err))

			fetched := fetchErrors(err)
			if tc.status < 0 {
				assert.Empty(t, fetched)

				return
			}

			require.Len(t, fetched, 1)
			assert.Equal(t, tc.status, fetched[0].StatusCode)
			require.ErrorIs(t, fetched[0], niceyaml.ErrLimit)
		})
	}
}

func TestWithLoadLimit_Default(t *testing.T) {
	t.Parallel()

	// A limit of zero or less keeps the default, and the largest int
	// reads a document of any size. Each reads a file and a URL far past
	// a limit of 64 bytes.
	body := `{"type": "object"}` + strings.Repeat(" ", 1<<10)
	fsys := yamltest.MapFS(map[string]string{"s.json": body})
	client := yamltest.Respond(http.StatusOK, body).Client()

	tcs := map[string]struct {
		limit int
	}{
		"zero":        {limit: 0},
		"negative":    {limit: -1},
		"largest int": {limit: math.MaxInt},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithLoadLimit(tc.limit))

			for _, ref := range []schema.Ref{
				schema.FileFS(fsys, "s.json"),
				schema.URL("https://example.com/s.json"),
				schema.Embedded([]byte("type: object\n" + strings.Repeat("#", 1<<10) + "\n")),
			} {
				_, err := reg.Schema(t.Context(), ref)
				require.NoError(t, err, ref.Name())
			}
		})
	}
}

func TestLoadError(t *testing.T) {
	t.Parallel()

	const secretURL = "https://user:secret@example.com/s.json"

	fsys := yamltest.MapFS(map[string]string{"bad.json": `{"type": 12}`})

	tcs := map[string]struct {
		phase error
		ref   schema.Ref
		want  string
	}{
		"fetch that fails": {
			ref:   schema.URL(secretURL),
			phase: schema.ErrLoad,
			want: `load schema: "https://user:xxxxx@example.com/s.json": ` +
				"fetch https://user:xxxxx@example.com/s.json: status 404",
		},
		"file that does not exist": {
			ref:   schema.FileFS(fsys, "missing.json"),
			phase: schema.ErrLoad,
			want:  `load schema: "missing.json": read missing.json: `,
		},
		"schema that does not compile": {
			ref:   schema.FileFS(fsys, "bad.json"),
			phase: schema.ErrCompile,
			want:  `compile schema: "bad.json": `,
		},
		"URL that is not HTTP": {
			ref:   schema.URL("ftp://example.com/s.json"),
			phase: schema.ErrLoad,
			want:  `load schema: "ftp://example.com/s.json": not an HTTP or HTTPS URL`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(
				schema.WithHTTPClient(yamltest.Respond(http.StatusNotFound, "").Client()),
				schema.WithResolvers(tc.ref),
			)

			// The second request answers from the failure the registry
			// remembers, and the Lookup binds the error to the document.
			for range 2 {
				_, err := reg.Schema(t.Context(), tc.ref)
				requireLoadErrorOf(t, err, tc.ref, tc.phase)
				assert.True(t, strings.HasPrefix(err.Error(), tc.want), err.Error())
				assert.NotContains(t, err.Error(), "secret")
			}

			_, err := reg.Lookup(t.Context(), document(t))
			requireLoadErrorOf(t, err, tc.ref, tc.phase)
			assert.False(t, niceyaml.IsInvalid(err))
		})
	}
}

// requireLoadErrorOf fails the test unless err holds a
// [*schema.LoadError] for ref in phase, and matches phase alone of the
// two phases.
func requireLoadErrorOf(t *testing.T, err error, ref schema.Ref, phase error) {
	t.Helper()

	loadErr, ok := errors.AsType[*schema.LoadError](err)
	require.True(t, ok, "want a LoadError, got %v", err)
	assert.Equal(t, ref.Key(), loadErr.Ref.Key())
	assert.Equal(t, phase, loadErr.Phase)

	require.ErrorIs(t, err, phase)

	other := schema.ErrCompile
	if errors.Is(phase, schema.ErrCompile) {
		other = schema.ErrLoad
	}

	require.NotErrorIs(t, err, other)
}

func TestLoadError_Fields(t *testing.T) {
	t.Parallel()

	// The Key keeps the password, and the message redacts it.
	ref := schema.URL("https://user:secret@example.com/s.json")

	err := &schema.LoadError{Ref: ref, Phase: schema.ErrLoad, Err: io.EOF}

	require.EqualError(t, err, `load schema: "https://user:xxxxx@example.com/s.json": EOF`)
	require.ErrorIs(t, err, schema.ErrLoad)
	require.ErrorIs(t, err, io.EOF)
	require.NotErrorIs(t, err, schema.ErrCompile)
	assert.Equal(t, "https://user:secret@example.com/s.json", err.Ref.Key())
	assert.Equal(t, "https://user:xxxxx@example.com/s.json", err.Ref.Name())
}

func TestFetchError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  *schema.FetchError
		want string
	}{
		"URL": {
			err:  &schema.FetchError{URL: "https://example.com/s.json", StatusCode: http.StatusNotFound, Err: io.EOF},
			want: "fetch https://example.com/s.json: EOF",
		},
		"upper-case scheme": {
			err:  &schema.FetchError{URL: "HTTP://example.com/s.json", Err: io.EOF},
			want: "fetch HTTP://example.com/s.json: EOF",
		},
		"path": {
			err:  &schema.FetchError{URL: "/srv/s.json", Err: fs.ErrNotExist},
			want: "read /srv/s.json: file does not exist",
		},
		"path with control characters": {
			err:  &schema.FetchError{URL: "a\x1b[2J\nb.json", Err: fs.ErrNotExist},
			want: "read a␛[2J␊b.json: file does not exist",
		},
		"no cause": {
			err:  &schema.FetchError{URL: "/srv/s.json"},
			want: "read /srv/s.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)

			if tc.err.Err != nil {
				require.ErrorIs(t, tc.err, tc.err.Err)
			}
		})
	}
}

func TestFetchError_Statuses(t *testing.T) {
	t.Parallel()

	// Two $refs of one schema fail with different statuses, and the error
	// of the schema holds the FetchError of each.
	client := routes(
		map[string]string{
			"/root.json": `{"properties": {"a": {"$ref": "a.json"}, "b": {"$ref": "b.json"}}}`,
		},
		map[string]int{
			"/a.json": http.StatusNotFound,
			"/b.json": http.StatusServiceUnavailable,
		},
	).Client()

	reg := schema.NewRegistry(schema.WithHTTPClient(client))

	_, err := reg.Schema(t.Context(), schema.URL("https://user:secret@example.com/root.json"))
	requireLoadError(t, err, schema.ErrLoad)

	var statuses []int

	for _, fetchErr := range fetchErrors(err) {
		statuses = append(statuses, fetchErr.StatusCode)

		assert.True(t, strings.HasPrefix(fetchErr.URL, "https://user:xxxxx@example.com/"), fetchErr.URL)
	}

	slices.Sort(statuses)
	assert.Equal(t, []int{http.StatusNotFound, http.StatusServiceUnavailable}, statuses)

	first, ok := errors.AsType[*schema.FetchError](err)
	require.True(t, ok)
	assert.Contains(t, []int{http.StatusNotFound, http.StatusServiceUnavailable}, first.StatusCode)
}

func TestFetchError_FileRead(t *testing.T) {
	t.Parallel()

	// A file read sends no request, so its FetchError holds no status and
	// names the path the file system reads.
	_, err := schema.NewRegistry().Load(t.Context(), schema.FileFS(yamltest.MapFS(nil), "schemas/s.json"))
	requireLoadError(t, err, fs.ErrNotExist)

	fetchErr, ok := errors.AsType[*schema.FetchError](err)
	require.True(t, ok)
	assert.Equal(t, "schemas/s.json", fetchErr.URL)
	assert.Zero(t, fetchErr.StatusCode)
}

func TestFetchError_RefusedURL(t *testing.T) {
	t.Parallel()

	// The registry refuses a URL outside its prefixes before any fetch, so
	// the error holds no FetchError.
	reg := schema.NewRegistry(schema.WithURLPrefixes("https://schemas.example.com/"))

	_, err := reg.Load(t.Context(), schema.URL("https://example.com/s.json"))
	requireLoadError(t, err, schema.ErrURLNotAllowed)
	assert.Empty(t, fetchErrors(err))
}

func TestFetchError_RefusedPath(t *testing.T) {
	t.Parallel()

	// The registry refuses an absolute path in a file system before any
	// read, and the error still holds a FetchError that names the path.
	_, err := schema.NewRegistry().Load(t.Context(), schema.FileFS(yamltest.MapFS(nil), "/schemas/s.json"))
	requireLoadError(t, err, fs.ErrInvalid)

	fetchErr, ok := errors.AsType[*schema.FetchError](err)
	require.True(t, ok)
	assert.Equal(t, "/schemas/s.json", fetchErr.URL)
	assert.Zero(t, fetchErr.StatusCode)
	require.ErrorIs(t, fetchErr, fs.ErrInvalid)
}
