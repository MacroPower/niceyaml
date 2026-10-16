package schema_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// A heldError is an error a test watches through a weak pointer, to see
// whether a registry still holds it.
type heldError struct {
	msg string
}

func (e *heldError) Error() string {
	return e.msg
}

// countedRefs returns n refs, each of which names its own key, serves a
// schema, and counts how many times its load runs.
func countedRefs(n int) ([]schema.Ref, []*atomic.Int32) {
	refs := make([]schema.Ref, n)
	loads := make([]*atomic.Int32, n)

	for i := range n {
		refs[i], loads[i] = countingLoader(fmt.Sprintf("schema-%d", i), []byte(`{"type": "object"}`))
	}

	return refs, loads
}

// loadAll asks reg for the schema of each ref, in order, and fails the
// test unless each loads.
func loadAll(t *testing.T, reg *schema.Registry, refs ...schema.Ref) {
	t.Helper()

	for _, ref := range refs {
		_, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
	}
}

// loadCounts returns the value of each count.
func loadCounts(counts []*atomic.Int32) []int32 {
	values := make([]int32, len(counts))

	for i, count := range counts {
		values[i] = count.Load()
	}

	return values
}

func TestWithCacheLimit(t *testing.T) {
	t.Parallel()

	t.Run("drops the schema used least recently", func(t *testing.T) {
		t.Parallel()

		refs, loads := countedRefs(3)
		a, b, c := refs[0], refs[1], refs[2]

		reg := schema.NewRegistry(schema.WithCacheLimit(2))

		// The second request for a makes b the schema used least
		// recently, so c takes the place of b.
		loadAll(t, reg, a, b, a, c)
		assert.Equal(t, []int32{1, 1, 1}, loadCounts(loads))

		loadAll(t, reg, a, c)
		assert.Equal(t, []int32{1, 1, 1}, loadCounts(loads))

		// B loads again and takes the place of a.
		loadAll(t, reg, b)
		assert.Equal(t, []int32{1, 2, 1}, loadCounts(loads))

		loadAll(t, reg, a)
		assert.Equal(t, []int32{2, 2, 1}, loadCounts(loads))
	})

	t.Run("limit", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []schema.RegistryOption
			// The number of schemas the registry keeps out of eight.
			want int
		}{
			"no option keeps every schema": {
				want: 8,
			},
			"zero keeps every schema": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(0)},
				want: 8,
			},
			"negative keeps every schema": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(-1)},
				want: 8,
			},
			"positive bounds the schemas": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(3)},
				want: 3,
			},
			"zero after a positive limit keeps the limit": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(3), schema.WithCacheLimit(0)},
				want: 3,
			},
			"last positive limit wins": {
				opts: []schema.RegistryOption{schema.WithCacheLimit(3), schema.WithCacheLimit(5)},
				want: 5,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				refs, loads := countedRefs(8)
				reg := schema.NewRegistry(tc.opts...)

				loadAll(t, reg, refs...)

				// Asking again in reverse order finds the schemas the
				// registry kept, the ones used last, before it loads one
				// that it dropped.
				for _, ref := range slices.Backward(refs) {
					loadAll(t, reg, ref)
				}

				var kept int

				for _, count := range loads {
					if count.Load() == 1 {
						kept++
					}
				}

				assert.Equal(t, tc.want, kept)
			})
		}
	})

	t.Run("a failure counts toward the limit", func(t *testing.T) {
		t.Parallel()

		refs, loads := countedRefs(1)
		gone := loading("gone", nil, errors.New("gone"))

		reg := schema.NewRegistry(schema.WithCacheLimit(1))

		loadAll(t, reg, refs[0])

		_, err := reg.Schema(t.Context(), gone)
		require.ErrorIs(t, err, schema.ErrLoad)

		loadAll(t, reg, refs[0])
		assert.Equal(t, []int32{2}, loadCounts(loads))
	})

	t.Run("a failure leaves room once its interval passes", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			refs, loads := countedRefs(2)
			a, b := refs[0], refs[1]
			gone := loading("gone", nil, errors.New("gone"))

			reg := schema.NewRegistry(schema.WithCacheLimit(2), schema.WithRetryAfter(time.Second))

			loadAll(t, reg, a)

			_, err := reg.Schema(t.Context(), gone)
			require.ErrorIs(t, err, schema.ErrLoad)

			// The failure has left the cache by the time b goes in, so a
			// stays, though the registry used the failure after a.
			time.Sleep(time.Second)

			loadAll(t, reg, b, a)
			assert.Equal(t, []int32{1, 1}, loadCounts(loads))
		})
	})

	t.Run("drops the documents $refs name", func(t *testing.T) {
		t.Parallel()

		handler, requests := countingHandler(schemaHandler(map[string]string{
			"/s1.json": `{"$ref": "d1.json"}`,
			"/s2.json": `{"$ref": "d2.json"}`,
			"/s3.json": `{"$ref": "d3.json"}`,
			"/s4.json": `{"$ref": "d1.json"}`,
			"/d1.json": `{"type": "object"}`,
			"/d2.json": `{"type": "object"}`,
			"/d3.json": `{"type": "object"}`,
		}))

		reg := schema.NewRegistry(
			schema.WithHTTPClient(handlerTransport(handler).Client()),
			schema.WithCacheLimit(2),
		)

		// D3 takes the place of d1, so the compile of s4 fetches d1 again.
		loadAll(t, reg,
			schema.URL("http://schemas.test/s1.json"),
			schema.URL("http://schemas.test/s2.json"),
			schema.URL("http://schemas.test/s3.json"),
			schema.URL("http://schemas.test/s4.json"),
		)

		assert.Equal(t, map[string]int{
			"/s1.json": 1, "/s2.json": 1, "/s3.json": 1, "/s4.json": 1,
			"/d1.json": 2, "/d2.json": 1, "/d3.json": 1,
		}, requests())
	})

	t.Run("stops holding the file systems of dropped entries", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			files map[string]string
			err   error
		}{
			"schema": {
				files: map[string]string{"s.json": `{"type": "object"}`},
			},
			"schema that does not load": {
				err: fs.ErrNotExist,
			},
			"document a $ref names": {
				files: map[string]string{
					"s.json":    `{"$ref": "defs.json"}`,
					"defs.json": `{"type": "object"}`,
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				reg := schema.NewRegistry(schema.WithCacheLimit(2))

				systems := make([]*heldFS, 4)
				for i := range systems {
					systems[i] = newHeldFS(tc.files, nil)

					_, err := reg.Schema(t.Context(), schema.FileFS(systems[i].fsys, "s.json"))
					if tc.err != nil {
						requireLoadError(t, err, tc.err)
					} else {
						require.NoError(t, err)
					}
				}

				// The registry keeps the entries of the last two file
				// systems, and stops holding the first two with their
				// entries.
				held := make([]bool, len(systems))
				for i, h := range systems {
					held[i] = h.held()
				}

				assert.Equal(t, []bool{false, false, true, true}, held)

				runtime.KeepAlive(reg)
			})
		}
	})

	t.Run("concurrent requests", func(t *testing.T) {
		t.Parallel()

		refs, _ := countedRefs(10)
		reg := schema.NewRegistry(schema.WithCacheLimit(3))

		var (
			wg   sync.WaitGroup
			errs [8]error
			nils atomic.Int32
		)

		for g := range errs {
			wg.Go(func() {
				for i := range 200 {
					s, err := reg.Schema(t.Context(), refs[i%len(refs)])
					if err != nil {
						errs[g] = err

						return
					}

					if s == nil {
						nils.Add(1)
					}
				}
			})
		}

		wg.Wait()

		require.NoError(t, errors.Join(errs[:]...))
		assert.Zero(t, nils.Load())
	})
}

func TestRegistry_ExpiredFailures(t *testing.T) {
	t.Parallel()

	const (
		// A schema whose $ref names a document that does not load.
		brokenURL = "http://schemas.test/broken.json"
		// A schema whose $ref names a document that loads.
		workingURL = "http://schemas.test/working.json"
	)

	tcs := map[string]struct {
		// The ref that fails with a new heldError on each load.
		failing func(fail func() error) schema.Ref
	}{
		"schema that does not load": {
			failing: func(fail func() error) schema.Ref {
				return schema.Loadable("gone", func(context.Context) ([]byte, error) {
					return nil, fail()
				})
			},
		},
		"document a $ref names": {
			failing: func(func() error) schema.Ref {
				return schema.URL(brokenURL)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var held weak.Pointer[heldError]

				// Fail returns a new error and watches it.
				fail := func() error {
					err := &heldError{msg: "gone"}
					held = weak.Make(err)

					return err
				}

				answer := handlerTransport(schemaHandler(map[string]string{
					"/broken.json":  `{"$ref": "gone.json"}`,
					"/working.json": `{"$ref": "defs.json"}`,
					"/defs.json":    `{"type": "object"}`,
				}))

				client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/gone.json" {
						return nil, fail()
					}

					return answer(r)
				}).Client()

				reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithRetryAfter(time.Second))

				requireLoadFails(t, reg, tc.failing(fail), "gone")

				// The registry remembers the failure inside the interval.
				runtime.GC()
				require.NotNil(t, held.Value())

				// Once the interval has passed, the next schema and
				// document the registry keeps drop the failure.
				time.Sleep(time.Second)
				loadAll(t, reg, schema.URL(workingURL))

				runtime.GC()
				assert.Nil(t, held.Value())

				// The registry must outlive the check, or the collector
				// frees the failure with the registry.
				runtime.KeepAlive(reg)
			})
		})
	}
}

// requireLoadFails fails the test unless reg fails to load the schema ref
// names with an error whose text holds want. It keeps no reference to the
// error once it returns.
func requireLoadFails(t *testing.T, reg *schema.Registry, ref schema.Ref, want string) {
	t.Helper()

	_, err := reg.Schema(t.Context(), ref)
	require.ErrorIs(t, err, schema.ErrLoad)
	require.ErrorContains(t, err, want)
}
