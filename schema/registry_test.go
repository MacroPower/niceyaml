package schema_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Path helpers for tests.
var kindPath = paths.Root().Child("kind")

// countingLoader returns a resolver that names key, serves data, and counts
// how many times its Load runs.
func countingLoader(key string, data []byte) (schema.Resolver, *atomic.Int32) {
	var loads atomic.Int32

	r := schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
		return schema.Loadable(key, func(_ context.Context) ([]byte, error) {
			loads.Add(1)

			return data, nil
		}), nil
	})

	return r, &loads
}

// A gatedContext is a context whose Done method waits for gate to close.
// A test uses it to hold a caller of [schema.Registry.Schema] after the
// caller joins a load and before it waits on the result.
type gatedContext struct {
	context.Context

	gate <-chan struct{}
}

// withGate returns a copy of ctx whose Done method waits for gate to
// close.
func withGate(ctx context.Context, gate <-chan struct{}) context.Context {
	return gatedContext{Context: ctx, gate: gate}
}

// Done waits for gate to close and returns the done channel of the
// wrapped context.
func (c gatedContext) Done() <-chan struct{} {
	<-c.gate

	return c.Context.Done()
}

// pastDeadline returns a context whose deadline has already passed, so
// its error is [context.DeadlineExceeded].
func pastDeadline(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now())
	t.Cleanup(cancel)

	return ctx
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)

	t.Run("first match wins", func(t *testing.T) {
		t.Parallel()

		// The second resolver matches everything, but the lookup never
		// reaches it.
		fallback, fallbackLoads := countingLoader("fallback.json", schemaData)

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded(schemaData),
			),
			fallback,
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, int32(0), fallbackLoads.Load())
	})

	t.Run("loader alone applies to every document", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaData)))

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `other: value`} {
			doc := yamltest.FirstDocument(t, stringtest.Input(input))
			_, err := reg.Lookup(t.Context(), doc)
			require.NoError(t, err)
		}
	})

	t.Run("no match returns error", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("empty registry matches nothing", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("variadic Register keeps order", func(t *testing.T) {
		t.Parallel()

		first, firstLoads := countingLoader("first.json", schemaData)
		second, secondLoads := countingLoader("second.json", schemaData)

		reg := schema.NewRegistry(schema.WithResolvers(first, second))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		_, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(1), firstLoads.Load())
		assert.Equal(t, int32(0), secondLoads.Load())
	})
}

func TestRegistry_Validate_ScopedNode(t *testing.T) {
	t.Parallel()

	// The resolvers read the file path, the preamble, and the content of a
	// whole document, so Validate and Lookup both refuse a scoped Node
	// rather than check the document around it or return its schema for a
	// single node.
	schemaData := []byte(`{
		"type": "object",
		"properties": {
			"kind": {"type": "string"},
			"spec": {"type": "object", "properties": {"replicas": {"type": "integer"}}}
		}
	}`)
	reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaData)))

	t.Run("Validate refuses a scoped node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			spec:
			  replicas: 1
		`))
		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		err := reg.Validate(t.Context(), spec)
		require.ErrorIs(t, err, schema.ErrScopedDocument)
		assert.Contains(t, err.Error(), "$.spec")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc.Source(), bound.Source())

		err = spec.Validate(t.Context(), reg)
		require.ErrorIs(t, err, schema.ErrScopedDocument)
	})

	t.Run("a scoped decode under a registry fails before it decodes", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			spec:
			  replicas: 1
		`))
		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		var v struct {
			Replicas int `yaml:"replicas"`
		}

		err := spec.DecodeInto(t.Context(), &v, niceyaml.WithValidator(reg))
		require.ErrorIs(t, err, schema.ErrScopedDocument)
		assert.Zero(t, v.Replicas)
	})

	t.Run("a registry that requires no schema refuses a scoped node", func(t *testing.T) {
		t.Parallel()

		lax := schema.NewRegistry(schema.WithRequireSchema(false))

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			spec:
			  replicas: 1
		`))
		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		require.ErrorIs(t, lax.Validate(t.Context(), spec), schema.ErrScopedDocument)
	})

	t.Run("the root validates once and the nodes decode without it", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			spec:
			  replicas: 1
		`))

		require.NoError(t, doc.Validate(t.Context(), reg))

		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		var v struct {
			Replicas int `yaml:"replicas"`
		}

		require.NoError(t, spec.DecodeInto(t.Context(), &v))
		assert.Equal(t, 1, v.Replicas)
	})

	t.Run("Lookup refuses a scoped node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			spec:
			  replicas: 1
		`))
		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		_, err := reg.Lookup(t.Context(), spec)
		require.ErrorIs(t, err, schema.ErrScopedDocument)
		assert.Contains(t, err.Error(), "$.spec")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc.Source(), bound.Source())

		// The whole document resolves, whichever Node of it reaches the
		// root.
		_, err = reg.Lookup(t.Context(), doc)
		require.NoError(t, err)

		_, err = reg.Lookup(t.Context(), spec.Document())
		require.NoError(t, err)
	})
}

func TestRegistry_Lookup_CancelledContext(t *testing.T) {
	t.Parallel()

	t.Run("canceled before the lookup", func(t *testing.T) {
		t.Parallel()

		// A Ref ignores its context, so the lookup reports the
		// cancellation itself rather than name the schema.
		reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded([]byte(`{"type":"object"}`))))

		doc, err := niceyaml.NewSourceFromString("kind: Deployment\n").Document()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = reg.Lookup(ctx, doc)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("last resolver cancels and declines", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				cancel()

				return schema.Ref{}, schema.ErrNoMatch
			})),
			schema.WithRequireSchema(false),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		_, err := reg.Lookup(ctx, doc)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.NotErrorIs(t, err, schema.ErrNoMatch)

		// A registry that does not require a schema passes an unmatched
		// document, but never a canceled lookup.
		require.ErrorIs(t, reg.Validate(ctx, doc), context.Canceled)
	})

	t.Run("resolver cancels and fails", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			fail func(ctx context.Context) error
			want string
		}{
			"unrelated error": {
				fail: func(context.Context) error {
					return errors.New("bad directive")
				},
				want: "resolve schema: context canceled",
			},
			"error wraps the context's error": {
				fail: func(ctx context.Context) error {
					return fmt.Errorf("fetch catalog: %w", ctx.Err())
				},
				want: "resolve schema: fetch catalog: context canceled",
			},
			"decline wraps the context's error": {
				fail: func(ctx context.Context) error {
					return fmt.Errorf("%w: %w", schema.ErrNoMatch, ctx.Err())
				},
				want: "resolve schema: context canceled",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

				newRegistry := func(cancel context.CancelFunc) *schema.Registry {
					return schema.NewRegistry(schema.WithResolvers(schema.ResolverFunc(
						func(ctx context.Context, _ *niceyaml.Node) (schema.Ref, error) {
							cancel()

							return schema.Ref{}, tc.fail(ctx)
						},
					)))
				}

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				_, err := newRegistry(cancel).Lookup(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
				require.NotErrorIs(t, err, schema.ErrNoMatch)
				require.EqualError(t, err, tc.want)

				ctx, cancel = context.WithCancel(t.Context())
				defer cancel()

				err = newRegistry(cancel).Validate(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
			})
		}
	})

	t.Run("resolver cancels and names a schema", func(t *testing.T) {
		t.Parallel()

		load := func(context.Context) ([]byte, error) {
			return []byte(`{"type":"object"}`), nil
		}

		tcs := map[string]struct {
			ref  schema.Ref
			warm bool
		}{
			"loadable": {
				ref: schema.Loadable("cancel.json", load),
			},
			"cached loadable": {
				ref:  schema.Loadable("cancel.json", load),
				warm: true,
			},
			"embedded": {
				ref: schema.Embedded([]byte(`{"type":"object"}`)),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				var cancelOnResolve atomic.Bool

				reg := schema.NewRegistry(schema.WithResolvers(schema.ResolverFunc(
					func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
						if cancelOnResolve.Load() {
							cancel()
						}

						return tc.ref, nil
					},
				)))

				doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

				if tc.warm {
					_, err := reg.Lookup(ctx, doc)
					require.NoError(t, err)
				}

				cancelOnResolve.Store(true)

				// The outcome does not depend on whether the registry
				// already holds the schema.
				_, err := reg.Lookup(ctx, doc)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, schema.ErrResolve)
				require.NotErrorIs(t, err, schema.ErrLoad)
			})
		}
	})

	t.Run("ends while the schema loads", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// A resolver named the schema, so the ended context is a failed
		// load rather than a failed resolution.
		reg := schema.NewRegistry(schema.WithResolvers(schema.Loadable(
			"cancel.json",
			func(loadCtx context.Context) ([]byte, error) {
				cancel()

				return nil, loadCtx.Err()
			},
		)))

		_, err := reg.Lookup(ctx, yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`)))
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.NotErrorIs(t, err, schema.ErrResolve)
	})
}

func TestRegistry_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.Validate(t.Context(), doc)
		require.NoError(t, err)
	})

	t.Run("invalid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.Validate(t.Context(), doc)
		require.Error(t, err)

		var validationErr *niceyaml.Error

		require.ErrorAs(t, err, &validationErr)

		gotPath, ok := validationErr.Path()
		require.True(t, ok)
		assert.Equal(t, "$.kind", gotPath.String())
	})

	t.Run("no match returns ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		// Service matches no resolver, so Validate returns ErrNoMatch.
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestRegistry_WithRequireSchema(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)

	newRegistry := func() *schema.Registry {
		return schema.NewRegistry(
			schema.WithResolvers(schema.When(
				matcher.Content(kindPath, "Deployment"),
				schema.Embedded(schemaData),
			)),
			schema.WithRequireSchema(false),
		)
	}

	t.Run("accepts a document no resolver applies to", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		require.NoError(t, newRegistry().Validate(t.Context(), doc))
	})

	t.Run("accepts a document without content", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "# only a comment\n")
		require.NoError(t, newRegistry().Validate(t.Context(), doc))
	})

	t.Run("still validates a document a resolver applies to", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := newRegistry().Validate(t.Context(), doc)
		require.Error(t, err)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("still reports other resolver errors", func(t *testing.T) {
		t.Parallel()

		cannotDecide := errors.New("cannot decide")

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Ref{}, cannotDecide
			})),
			schema.WithRequireSchema(false),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, cannotDecide)
	})

	t.Run("still reports a load error that wraps ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		// The resolver applies, so the failed load is no unmatched
		// document, whatever the load error wraps.
		reg := schema.NewRegistry(
			schema.WithResolvers(schema.Loadable("k", func(_ context.Context) ([]byte, error) {
				return nil, fmt.Errorf("upstream lookup: %w", schema.ErrNoMatch)
			})),
			schema.WithRequireSchema(false),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrLoad)
	})

	t.Run("Lookup still reports ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := newRegistry().Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("runs inside a decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		got, err := doc.Decode[map[string]string](t.Context(), niceyaml.WithValidator(newRegistry()))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"kind": "Service"}, got)
	})
}

func TestRegistry_Caching(t *testing.T) {
	t.Parallel()

	t.Run("validators are cached by URL", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		// First lookup compiles and caches.
		doc1 := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v1, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)

		// Second lookup uses cache.
		doc2 := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v2, err := reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)

		assert.Same(t, v1, v2, "validators should be the same instance")
	})

	t.Run("loader runs once per URL", func(t *testing.T) {
		t.Parallel()

		r, loads := countingLoader("test.json", []byte(`{"type": "object"}`))

		reg := schema.NewRegistry(schema.WithResolvers(r))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		for range 5 {
			err := reg.Validate(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), loads.Load(), "the cache should be checked before loading")
	})

	t.Run("scheme case does not split the cache", func(t *testing.T) {
		t.Parallel()

		// The client serves the schema from memory and counts every request
		// it sends. No server listens on a port, so a connection that
		// another process opens cannot add to the count. Names under .test
		// never resolve, so a fetch that bypasses the client fails rather
		// than going uncounted.
		var fetches atomic.Int32

		client := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				fetches.Add(1)

				rec := httptest.NewRecorder()
				//nolint:errcheck // Test helper.
				rec.WriteString(`{"type": "object"}`)

				return rec.Result(), nil
			}),
		}

		resolve := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			// Each document names the same schema with a different scheme
			// case, as two directives in two files might.
			scheme := "http"
			if doc.DocumentIndex() == 1 {
				scheme = "HTTP"
			}

			return schema.URL(scheme + "://schemas.test/schema.json"), nil
		})

		reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithResolvers(resolve))

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			b: 2
		`))

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		for _, doc := range docs {
			require.NoError(t, reg.Validate(t.Context(), doc))
		}

		assert.Equal(t, int32(1), fetches.Load(), "one schema should be fetched once")
	})

	t.Run("distinct URLs load separately", func(t *testing.T) {
		t.Parallel()

		deployment, deploymentLoads := countingLoader("deployment.json", []byte(`{"type": "object"}`))
		service, serviceLoads := countingLoader("service.json", []byte(`{"type": "object"}`))

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), deployment),
			schema.When(matcher.Content(kindPath, "Service"), service),
		))

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `kind: Deployment`, `kind: Service`} {
			doc := yamltest.FirstDocument(t, stringtest.Input(input))
			err := reg.Validate(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), deploymentLoads.Load())
		assert.Equal(t, int32(1), serviceLoads.Load())
	})

	t.Run("zero ref is rejected", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Ref{}, nil
			}),
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorContains(t, err, "names no schema")
	})

	t.Run("reference error is rejected", func(t *testing.T) {
		t.Parallel()

		// A resolver that hands back what FileOrURL returns for a reference
		// that names nothing reports that error.
		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
				return schema.FileOrURL(doc.FilePath(), "")
			}),
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, schema.ErrEmptyPath)
	})

	t.Run("load failure is not cached", func(t *testing.T) {
		t.Parallel()

		var loads atomic.Int32

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Loadable("flaky.json", func(_ context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						return nil, errors.New("transient")
					}

					return []byte(`{"type": "object"}`), nil
				}), nil
			})),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "transient")

		_, err = reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(2), loads.Load())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)
		reg := schema.NewRegistry(schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)))

		// Pre-create documents outside goroutines, since only the test
		// goroutine may run the helper's require calls.
		docs := make([]*niceyaml.Node, 100)
		for i := range docs {
			docs[i] = yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		}

		var wg sync.WaitGroup

		wg.Add(len(docs))

		for _, doc := range docs {
			go func() {
				defer wg.Done()

				_, err := reg.Lookup(t.Context(), doc)
				assert.NoError(t, err)
			}()
		}

		wg.Wait()
	})
}

func TestRegistry_ConcurrentLoad(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Concurrent lookups for one URL share a single load and compile, and
		// every caller receives the same validator.
		const goroutines = 10

		release := make(chan struct{})

		var loads atomic.Int32

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Loadable("test.json", func(_ context.Context) ([]byte, error) {
					loads.Add(1)
					<-release // Hold the load open until every caller waits on it.

					return []byte(`{"type": "object"}`), nil
				}), nil
			})),
		)

		// Pre-create documents outside goroutines.
		docs := make([]*niceyaml.Node, goroutines)
		for i := range docs {
			docs[i] = yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		}

		validators := make([]*schema.Schema, goroutines)
		errs := make([]error, goroutines)

		var wg sync.WaitGroup

		for i := range goroutines {
			wg.Go(func() {
				validators[i], errs[i] = reg.Lookup(t.Context(), docs[i])
			})
		}

		// Wait until every caller is waiting on the one load in flight.
		synctest.Wait()
		close(release)
		wg.Wait()

		for _, err := range errs {
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), loads.Load(), "concurrent lookups should share one load")

		for i := 1; i < goroutines; i++ {
			assert.Same(t, validators[0], validators[i])
		}
	})
}

func TestRegistry_SharedLoad(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	t.Run("load that times out on its own runs once", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// Load wraps context.DeadlineExceeded the way an http.Client timeout
			// does while every caller's context stays live, so the callers share
			// the one failed load.
			const goroutines = 5

			release := make(chan struct{})

			var loads atomic.Int32

			reg := schema.NewRegistry(
				schema.WithResolvers(
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
						return schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
							loads.Add(1)
							<-release

							return nil, fmt.Errorf("fetch slow.json: %w", context.DeadlineExceeded)
						}), nil
					}),
				),
			)

			doc := yamltest.FirstDocument(t, `key: value`)
			errs := make([]error, goroutines)

			var wg sync.WaitGroup

			for i := range goroutines {
				wg.Go(func() {
					_, errs[i] = reg.Lookup(t.Context(), doc)
				})
			}

			// Wait until every caller is waiting on the one load in flight.
			synctest.Wait()
			close(release)
			wg.Wait()

			assert.Equal(t, int32(1), loads.Load(), "callers should share the failed load")

			for _, err := range errs {
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	})

	t.Run("joiner returns when its own deadline passes", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})

			reg := schema.NewRegistry(
				schema.WithResolvers(
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
						return schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
							<-release

							return schemaData, nil
						}), nil
					}),
				),
			)

			doc := yamltest.FirstDocument(t, `key: value`)

			var (
				wg        sync.WaitGroup
				leaderErr error
				joinerErr error
			)

			wg.Go(func() {
				_, leaderErr = reg.Lookup(t.Context(), doc)
			})

			// Start the joiner once the leader's load is in flight.
			synctest.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			joinerDone := make(chan struct{})

			go func() {
				defer close(joinerDone)

				_, joinerErr = reg.Lookup(ctx, doc)
			}()

			time.Sleep(time.Second)
			synctest.Wait()

			select {
			case <-joinerDone:
			default:
				assert.Fail(t, "joiner should return at its deadline, before the load finishes")
			}

			close(release)
			wg.Wait()
			<-joinerDone

			require.NoError(t, leaderErr)
			require.ErrorIs(t, joinerErr, schema.ErrLoad)
			require.ErrorIs(t, joinerErr, context.DeadlineExceeded)
		})
	})

	t.Run("joiner loads again after the starting caller cancels", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			var loads atomic.Int32

			reg := schema.NewRegistry(
				schema.WithResolvers(
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
						return schema.Loadable("slow.json", func(ctx context.Context) ([]byte, error) {
							if loads.Add(1) == 1 {
								// The first load runs under the starting caller's context
								// and ends with its cancellation.
								<-ctx.Done()

								return nil, fmt.Errorf("fetch slow.json: %w", ctx.Err())
							}

							return schemaData, nil
						}), nil
					}),
				),
			)

			doc := yamltest.FirstDocument(t, `key: value`)

			leaderCtx, cancelLeader := context.WithCancel(t.Context())
			defer cancelLeader()

			var (
				wg        sync.WaitGroup
				leaderErr error
				joinerErr error
			)

			wg.Go(func() {
				_, leaderErr = reg.Lookup(leaderCtx, doc)
			})

			synctest.Wait()

			wg.Go(func() {
				_, joinerErr = reg.Lookup(t.Context(), doc)
			})

			// Cancel the leader once the joiner is waiting on its load.
			synctest.Wait()
			cancelLeader()
			wg.Wait()

			require.ErrorIs(t, leaderErr, schema.ErrLoad)
			require.ErrorIs(t, leaderErr, context.Canceled)
			require.NoError(t, joinerErr)
			assert.Equal(t, int32(2), loads.Load())
		})
	})

	t.Run("caller whose context ends takes no schema from a later load", func(t *testing.T) {
		t.Parallel()

		// A late caller joins a load, and its deadline passes before it
		// waits on the result. The load fails because its starter cancels,
		// and a joiner with a live context loads again and caches the
		// schema. The late caller then finds both its context and the
		// failed load ready, and the select picks one at random, so the
		// test runs the race enough times to take both picks. Either way
		// the late caller reports its own deadline rather than the
		// starter's cancellation.
		synctest.Test(t, func(t *testing.T) {
			for range 64 {
				var loads atomic.Int32

				ref := schema.Loadable("slow.json", func(ctx context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						<-ctx.Done()

						return nil, fmt.Errorf("fetch slow.json: %w", ctx.Err())
					}

					return schemaData, nil
				})

				reg := schema.NewRegistry()

				starterCtx, cancelStarter := context.WithCancel(t.Context())

				var (
					wg         sync.WaitGroup
					starterErr error
					joinerErr  error
				)

				wg.Go(func() {
					_, starterErr = reg.Schema(starterCtx, ref)
				})

				synctest.Wait()

				wg.Go(func() {
					_, joinerErr = reg.Schema(t.Context(), ref)
				})

				synctest.Wait()

				// The late caller's deadline has already passed, and the gate
				// holds the caller between joining the load and waiting on it
				// until the joiner has loaded again.
				gate := make(chan struct{})
				lateCtx := withGate(pastDeadline(t), gate)
				lateDone := make(chan struct{})

				var (
					late    *schema.Schema
					lateErr error
				)

				go func() {
					defer close(lateDone)

					late, lateErr = reg.Schema(lateCtx, ref)
				}()

				synctest.Wait()
				cancelStarter()
				wg.Wait()

				// Let the failed load reach the late caller before it waits
				// on the result.
				synctest.Wait()
				close(gate)
				<-lateDone

				require.ErrorIs(t, starterErr, schema.ErrLoad)
				require.ErrorIs(t, starterErr, context.Canceled)
				require.NoError(t, joinerErr)
				require.ErrorIs(t, lateErr, schema.ErrLoad)
				require.ErrorIs(t, lateErr, context.DeadlineExceeded)
				require.NotErrorIs(t, lateErr, context.Canceled)
				require.Nil(t, late)
				require.Equal(t, int32(2), loads.Load())
			}
		})
	})

	t.Run("caller whose context ends takes a load that already finished", func(t *testing.T) {
		t.Parallel()

		errBroken := errors.New("broken")

		tcs := map[string]struct {
			data []byte
			err  error
		}{
			"schema": {
				data: schemaData,
			},
			"failure": {
				err: errBroken,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// A late caller joins a load, and its deadline passes before
				// it waits on the result. The load then finishes, so the late
				// caller finds both its context and the result ready, and the
				// select picks one at random. The result counts either way,
				// and the test runs the race enough times to take both picks.
				synctest.Test(t, func(t *testing.T) {
					for range 64 {
						release := make(chan struct{})

						var loads atomic.Int32

						ref := schema.Loadable("slow.json", func(_ context.Context) ([]byte, error) {
							loads.Add(1)
							<-release

							return tc.data, tc.err
						})

						reg := schema.NewRegistry()

						var (
							wg         sync.WaitGroup
							starter    *schema.Schema
							starterErr error
						)

						wg.Go(func() {
							starter, starterErr = reg.Schema(t.Context(), ref)
						})

						synctest.Wait()

						// The gate holds the late caller between joining the load
						// and waiting on it until the load has finished.
						gate := make(chan struct{})
						lateCtx := withGate(pastDeadline(t), gate)
						lateDone := make(chan struct{})

						var (
							late    *schema.Schema
							lateErr error
						)

						go func() {
							defer close(lateDone)

							late, lateErr = reg.Schema(lateCtx, ref)
						}()

						synctest.Wait()
						close(release)
						wg.Wait()

						// Let the result reach the late caller before it waits on
						// it.
						synctest.Wait()
						close(gate)
						<-lateDone

						require.Equal(t, int32(1), loads.Load())
						require.Same(t, starter, late)

						if tc.err == nil {
							require.NoError(t, starterErr)
							require.NoError(t, lateErr)
							require.NotNil(t, late)
						} else {
							require.ErrorIs(t, starterErr, tc.err)
							require.ErrorIs(t, lateErr, schema.ErrLoad)
							require.ErrorIs(t, lateErr, tc.err)
							require.NotErrorIs(t, lateErr, context.DeadlineExceeded)
						}
					}
				})
			})
		}
	})
}

func TestRegistry_DynamicResolver(t *testing.T) {
	t.Parallel()

	t.Run("ResolverFunc for dynamic schema", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		// Create schema files for different kinds.
		for _, kind := range []string{"Deployment", "Service"} {
			schemaData := []byte(`{"type": "object", "properties": {"kind": {"const": "` + kind + `"}}}`)
			err := os.WriteFile(filepath.Join(tmpDir, kind+".json"), schemaData, 0o600)
			require.NoError(t, err)
		}

		reg := schema.NewRegistry(
			schema.WithResolvers(
				schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
					node, err := doc.At(kindPath)
					if err != nil {
						return schema.Ref{}, schema.ErrNoMatch
					}

					kind, err := node.Decode[string](ctx)
					if err != nil || (kind != "Deployment" && kind != "Service") {
						return schema.Ref{}, schema.ErrNoMatch
					}

					return schema.File(filepath.Join(tmpDir, kind+".json")), nil
				}),
			),
		)

		// Deployment should validate.
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.Validate(t.Context(), doc)
		require.NoError(t, err)

		// Service should validate.
		doc = yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err = reg.Validate(t.Context(), doc)
		require.NoError(t, err)

		// ConfigMap has no schema.
		doc = yamltest.FirstDocument(t, stringtest.Input(`kind: ConfigMap`))
		err = reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("Directive integration", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		err := os.WriteFile(filepath.Join(tmpDir, "test.json"), schemaData, 0o600)
		require.NoError(t, err)

		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=test.json\nkind: Deployment\n")
		err = os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		source, err := niceyaml.NewSourceFromFile(yamlPath)
		require.NoError(t, err)

		docs, err := source.Documents()
		require.NoError(t, err)

		for _, doc := range docs {
			err = reg.Validate(t.Context(), doc)
			require.NoError(t, err)
		}
	})
}

func TestRegistry_CompileOptionsNotAliased(t *testing.T) {
	t.Parallel()

	// The registry compiles each schema on its first lookup, so aliasing
	// the caller's slice would let a later write change how the next
	// schema compiles. The option copies the slice when the caller builds
	// it, so a write between WithCompileOptions and NewRegistry changes
	// nothing either. Asserting formats makes the difference observable
	// here.
	tcs := map[string]struct {
		writeFirst bool
	}{
		"write after NewRegistry":  {},
		"write before NewRegistry": {writeFirst: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []schema.CompileOption{schema.WithJSONSchemaOptions(jsonschema.WithFormats(true))}
			opt := schema.WithCompileOptions(opts...)

			if tc.writeFirst {
				opts[0] = schema.WithJSONSchemaOptions(jsonschema.WithFormats(false))
			}

			reg := schema.NewRegistry(
				opt,
				schema.WithResolvers(schema.Embedded([]byte(`{"type": "string", "format": "ipv4"}`))),
			)

			opts[0] = schema.WithJSONSchemaOptions(jsonschema.WithFormats(false))

			doc := yamltest.FirstDocument(t, stringtest.Input(`not-an-ip`))
			err := reg.Validate(t.Context(), doc)
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrNoMatch)
		})
	}
}

func TestRegistry_ResolversNotAliased(t *testing.T) {
	t.Parallel()

	// The option copies the resolvers when the caller builds it, so a nil
	// written to the caller's slice afterwards neither slips past the nil
	// check nor reaches Lookup.
	res := []schema.Resolver{schema.Embedded([]byte(`{"type": "string"}`))}
	opt := schema.WithResolvers(res...)
	res[0] = nil

	reg := schema.NewRegistry(opt)

	doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
	err := reg.Validate(t.Context(), doc)
	require.Error(t, err)
	require.NotErrorIs(t, err, schema.ErrNoMatch)
	assert.Contains(t, err.Error(), "string")
}

func TestRegistry_JSONSchemaOptionsNotAliased(t *testing.T) {
	t.Parallel()

	// The registry runs a CompileOption only when it first compiles a
	// schema, so WithJSONSchemaOptions must copy its slice when the caller
	// builds the option. Otherwise a write after NewRegistry would turn
	// format assertions off.
	jopts := []jsonschema.ValidateOption{jsonschema.WithFormats(true)}

	reg := schema.NewRegistry(
		schema.WithCompileOptions(schema.WithJSONSchemaOptions(jopts...)),
		schema.WithResolvers(schema.Embedded([]byte(`{"type": "string", "format": "ipv4"}`))),
	)

	jopts[0] = jsonschema.WithFormats(false)

	doc := yamltest.FirstDocument(t, stringtest.Input(`not-an-ip`))
	err := reg.Validate(t.Context(), doc)
	require.Error(t, err)
	require.NotErrorIs(t, err, schema.ErrNoMatch)
}

func TestRegistry_CompileOptionsAppend(t *testing.T) {
	t.Parallel()

	// Each call appends, so the format assertions the first call asks for
	// still reach the compiler after the second call adds to them.
	reg := schema.NewRegistry(
		schema.WithCompileOptions(schema.WithJSONSchemaOptions(jsonschema.WithFormats(true))),
		schema.WithCompileOptions(schema.WithJSONSchemaOptions()),
		schema.WithResolvers(schema.Embedded([]byte(`{"type": "string", "format": "ipv4"}`))),
	)

	doc := yamltest.FirstDocument(t, stringtest.Input(`not-an-ip`))
	err := reg.Validate(t.Context(), doc)
	require.Error(t, err)
	require.NotErrorIs(t, err, schema.ErrNoMatch)
}

func TestRegistry_ErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("resolve error propagates", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Ref{}, errors.New("cannot decide")
			})),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrResolve)
		assert.Contains(t, err.Error(), "cannot decide")
	})

	t.Run("load error propagates through Validate", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				return schema.Loadable("broken.json", func(_ context.Context) ([]byte, error) {
					return nil, errors.New("disk on fire")
				}), nil
			})),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.Contains(t, err.Error(), "disk on fire")
		assert.Contains(t, err.Error(), "broken.json")
	})

	t.Run("compile error propagates", func(t *testing.T) {
		t.Parallel()

		broken, _ := countingLoader("bad.json", []byte(`{not valid json`))
		reg := schema.NewRegistry(schema.WithResolvers(broken))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.Contains(t, err.Error(), "bad.json")
	})

	t.Run("a nil resolver panics", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			resolver schema.Resolver
		}{
			"nil interface":    {resolver: nil},
			"nil ResolverFunc": {resolver: schema.ResolverFunc(nil)},
			"nil pointer":      {resolver: (*schema.Schema)(nil)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t, "schema.WithResolvers: resolver is nil", func() {
					schema.WithResolvers(schema.Directive(), tc.resolver)
				})
			})
		}
	})
}

func TestRegistry_MultipleDocuments(t *testing.T) {
	t.Parallel()

	deploymentSchema := []byte(
		`{"type": "object", "properties": {"kind": {"const": "Deployment"}}, "required": ["kind"]}`,
	)
	serviceSchema := []byte(`{"type": "object", "properties": {"kind": {"const": "Service"}}, "required": ["kind"]}`)

	reg := schema.NewRegistry(schema.WithResolvers(
		schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded(deploymentSchema)),
		schema.When(matcher.Content(kindPath, "Service"), schema.Embedded(serviceSchema)),
	))

	input := stringtest.Input(`
		kind: Deployment
		---
		kind: Service
		---
		kind: ConfigMap
	`)

	source := niceyaml.NewSourceFromString(input)
	docs, err := source.Documents()
	require.NoError(t, err)

	// Track validation results.
	validated := make(map[string]bool)
	for _, doc := range docs {
		kind, err := yamltest.At(t, doc, kindPath).Decode[string](t.Context())
		require.NoError(t, err)

		err = reg.Validate(t.Context(), doc)

		if kind == "Deployment" || kind == "Service" {
			require.NoError(t, err, "expected %s to validate", kind)

			validated[kind] = true
		} else {
			// ConfigMap has no matching schema, so Validate returns ErrNoMatch.
			require.ErrorIs(t, err, schema.ErrNoMatch)
		}
	}

	assert.True(t, validated["Deployment"])
	assert.True(t, validated["Service"])
}

func TestRegistry_Validator(t *testing.T) {
	t.Parallel()

	var _ niceyaml.Validator = (*schema.Registry)(nil)

	schemaData := []byte(`{
		"type": "object",
		"properties": {"kind": {"type": "string"}, "replicas": {"type": "integer"}}
	}`)
	reg := schema.NewRegistry(schema.WithResolvers(schema.When(
		matcher.Content(kindPath, "Deployment"),
		schema.Embedded(schemaData),
	)))

	type deployment struct {
		Kind     string `yaml:"kind"`
		Replicas int    `yaml:"replicas"`
	}

	t.Run("decodes a document the registry accepts", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			replicas: 3
		`))

		got, err := doc.Decode[deployment](t.Context(), niceyaml.WithValidator(reg))
		require.NoError(t, err)
		assert.Equal(t, deployment{Kind: "Deployment", Replicas: 3}, got)
	})

	t.Run("stops the decode when the schema rejects the document", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			replicas: many
		`))

		_, err := doc.Decode[deployment](t.Context(), niceyaml.WithValidator(reg))
		require.Error(t, err)

		var validationErr *niceyaml.Error

		require.ErrorAs(t, err, &validationErr)

		gotPath, ok := validationErr.Path()
		require.True(t, ok)
		assert.Equal(t, "$.replicas", gotPath.String())
	})

	t.Run("reports ErrNoMatch for a document no resolver applies to", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))

		_, err := doc.Decode[deployment](t.Context(), niceyaml.WithValidator(reg))
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestRegistry_Lookup_MatcherError(t *testing.T) {
	t.Parallel()

	// The matcher cannot decide on a document whose routing key holds an
	// alias with no anchor, so the lookup stops there rather than routing
	// the document to the next resolver.
	fallbackCalled := false
	reg := schema.NewRegistry(
		schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded([]byte(`{"type":"object"}`))),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
				fallbackCalled = true

				return schema.Embedded([]byte(`{}`)), nil
			}),
		),
		schema.WithRequireSchema(false),
	)

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: *missing`))

	_, err := reg.Lookup(t.Context(), doc)
	require.ErrorIs(t, err, schema.ErrResolve)
	require.ErrorIs(t, err, paths.ErrAlias)
	require.NotErrorIs(t, err, schema.ErrNoMatch)
	assert.False(t, fallbackCalled)

	// A registry that does not require a schema passes an unmatched
	// document, but never one its matcher could not decide on.
	require.ErrorIs(t, reg.Validate(t.Context(), doc), paths.ErrAlias)
}

func TestRegistry_CompiledSchema(t *testing.T) {
	t.Parallel()

	deployment := schema.MustCompile([]byte(`{"type": "object", "required": ["replicas"]}`))
	service := schema.MustCompile([]byte(`{"type": "object", "required": ["port"]}`))

	t.Run("a schema is a resolver the registry validates with as it is", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), deployment),
			schema.When(matcher.Content(kindPath, "Service"), service),
		))

		doc := yamltest.FirstDocument(t, "kind: Service\nport: 80\n")

		got, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, service, got)
		require.NoError(t, reg.Validate(t.Context(), doc))

		bad := yamltest.FirstDocument(t, "kind: Deployment\nport: 80\n")
		err = reg.Validate(t.Context(), bad)
		require.Error(t, err)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
		assert.Contains(t, err.Error(), "replicas")
	})

	t.Run("a schema built from a Go type routes the same way", func(t *testing.T) {
		t.Parallel()

		type pod struct {
			Kind  string `json:"kind"`
			Image string `json:"image"`
		}

		generated, err := jsonschema.NewGenerator().GenerateFor[pod](t.Context())
		require.NoError(t, err)

		podSchema := schema.FromJSONSchema(jsonschema.MustCompile(generated))

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Pod"), podSchema),
		))

		got, err := reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Pod\nimage: nginx\n"))
		require.NoError(t, err)
		assert.Same(t, podSchema, got)

		_, err = reg.Lookup(t.Context(), yamltest.FirstDocument(t, "kind: Job\n"))
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("a resolver returns the ref of a schema", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
				if strings.HasSuffix(doc.FilePath(), ".svc.yaml") {
					return service.Ref(), nil
				}

				return schema.Ref{}, schema.ErrNoMatch
			}),
		))

		doc := yamltest.FirstDocumentWithPath(t, "port: 80\n", "web.svc.yaml")

		got, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, service, got)
	})

	t.Run("a compiled schema keeps the options it was compiled with", func(t *testing.T) {
		t.Parallel()

		// The registry asserts formats, and the schema compiled without
		// them, so the value passes. The registry compiles nothing here.
		lax := schema.MustCompile([]byte(`{"type": "string", "format": "ipv4"}`))

		reg := schema.NewRegistry(
			schema.WithCompileOptions(schema.WithJSONSchemaOptions(jsonschema.WithFormats(true))),
			schema.WithResolvers(lax),
		)

		require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, "not-an-ip")))
	})
}

func TestRegistry_WithHTTPClient(t *testing.T) {
	t.Parallel()

	t.Run("the default client has a timeout", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		client := schema.HTTPClient(reg)
		require.NotNil(t, client)
		assert.Positive(t, client.Timeout)
		assert.NotSame(t, http.DefaultClient, client)
	})

	t.Run("a nil client keeps the default", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithHTTPClient(nil))
		assert.Same(t, schema.HTTPClient(schema.NewRegistry()), schema.HTTPClient(reg))
	})

	t.Run("a client replaces the default", func(t *testing.T) {
		t.Parallel()

		client := &http.Client{}
		reg := schema.NewRegistry(schema.WithHTTPClient(client))
		assert.Same(t, client, schema.HTTPClient(reg))
	})
}

func TestRegistry_WithFS(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "required": ["kind"]}`)

	bundle := fstest.MapFS{
		"configs/app.yaml": &fstest.MapFile{
			Data: []byte("# yaml-language-server: $schema=./app.schema.json\nkind: App\n"),
		},
		"configs/app.schema.json": &fstest.MapFile{Data: schemaData},
		"schemas/pod.json":        &fstest.MapFile{Data: schemaData},
	}

	t.Run("a file ref reads from the file system", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(
			schema.WithFS(bundle),
			schema.WithResolvers(schema.File("schemas/pod.json")),
		)

		data, err := reg.Load(t.Context(), schema.File("./schemas/pod.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)

		require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, "kind: Pod\n")))

		err = reg.Validate(t.Context(), yamltest.FirstDocument(t, "name: x\n"))
		require.Error(t, err)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("a directive resolves beside its document", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
		require.NoError(t, err)

		doc, err := source.Document()
		require.NoError(t, err)

		reg := schema.NewRegistry(
			schema.WithFS(bundle),
			schema.WithResolvers(schema.Directive()),
		)

		require.NoError(t, reg.Validate(t.Context(), doc))

		// The same registry without the file system looks for the file on
		// disk, where it does not exist.
		disk := schema.NewRegistry(schema.WithResolvers(schema.Directive()))
		err = disk.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("a missing file reports the path", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithFS(bundle))

		_, err := reg.Load(t.Context(), schema.File("schemas/missing.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrNotExist)
		assert.Contains(t, err.Error(), "schemas/missing.json")
	})

	t.Run("a named pipe is invalid", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{
			"schemas/pipe.json": &fstest.MapFile{Mode: fs.ModeNamedPipe},
		}

		reg := schema.NewRegistry(schema.WithFS(fsys))

		_, err := reg.Load(t.Context(), schema.File("schemas/pipe.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)
	})

	t.Run("an oversize file exceeds the limit", func(t *testing.T) {
		t.Parallel()

		const maxSchemaSize = 10 * 1024 * 1024 // Must match httpfetch.MaxSize.

		fsys := fstest.MapFS{
			"schemas/big.json": &fstest.MapFile{Data: make([]byte, maxSchemaSize+1)},
		}

		reg := schema.NewRegistry(schema.WithFS(fsys))

		_, err := reg.Load(t.Context(), schema.File("schemas/big.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "exceeds")
	})

	t.Run("an absolute path reads relative to the working directory", func(t *testing.T) {
		t.Parallel()

		wd, err := os.Getwd()
		require.NoError(t, err)

		reg := schema.NewRegistry(schema.WithFS(bundle))

		data, err := reg.Load(t.Context(), schema.File(filepath.Join(wd, "schemas", "pod.json")))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("a directive resolves beside a document opened by absolute path", func(t *testing.T) {
		t.Parallel()

		wd, err := os.Getwd()
		require.NoError(t, err)

		source := niceyaml.NewSourceFromString(
			"# yaml-language-server: $schema=./app.schema.json\nkind: App\n",
			niceyaml.WithFilePath(filepath.Join(wd, "configs", "app.yaml")),
		)

		doc, err := source.Document()
		require.NoError(t, err)

		reg := schema.NewRegistry(
			schema.WithFS(bundle),
			schema.WithResolvers(schema.Directive()),
		)

		require.NoError(t, reg.Validate(t.Context(), doc))
	})

	t.Run("an absolute path outside the working directory names no file", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithFS(bundle))

		_, err := reg.Load(t.Context(), schema.File(filepath.Join(t.TempDir(), "pod.json")))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)

		_, err = reg.Load(t.Context(), schema.File("/schemas/pod.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)
	})

	t.Run("an os.Root refuses a symlink out of its tree", func(t *testing.T) {
		t.Parallel()

		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "s.json"), schemaData, 0o600))

		root := t.TempDir()

		err := os.Symlink(filepath.Join(outside, "s.json"), filepath.Join(root, "s.json"))
		if err != nil {
			t.Skipf("create symlink: %v", err)
		}

		r, err := os.OpenRoot(root)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, r.Close()) })

		reg := schema.NewRegistry(schema.WithFS(r.FS()))

		_, err = reg.Load(t.Context(), schema.File("s.json"))
		require.ErrorIs(t, err, schema.ErrLoad)

		// A registry on os.DirFS follows the link and reads the file
		// outside the tree.
		dirFS := schema.NewRegistry(schema.WithFS(os.DirFS(root)))
		data, err := dirFS.Load(t.Context(), schema.File("s.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("a nil file system keeps the disk", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "schema.json")
		require.NoError(t, os.WriteFile(path, schemaData, 0o600))

		reg := schema.NewRegistry(schema.WithFS(nil))

		data, err := reg.Load(t.Context(), schema.File(path))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})
}

// A program can build a Ref with File, change directory, and only then
// validate through a registry with a file system. The test changes the
// process's working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestRegistry_WithFS_AfterChdir(t *testing.T) {
	bundle := fstest.MapFS{
		"schemas/config.json": &fstest.MapFile{
			Data: []byte(`{"properties": {"a": {"$ref": "defs.json"}}}`),
		},
		"schemas/defs.json": &fstest.MapFile{Data: []byte(`{"type": "string"}`)},
	}

	// The registry reads a relative root schema from the root of the file
	// system as it is, and an absolute one against the directory File saw.
	// The $ref resolves to an absolute path in both cases.
	tcs := map[string]struct {
		path func(wd string) string
	}{
		"relative path": {
			path: func(string) string { return "schemas/config.json" },
		},
		"absolute path": {
			path: func(wd string) string { return filepath.Join(wd, "schemas", "config.json") },
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			wd := t.TempDir()
			t.Chdir(wd)

			ref := schema.File(tc.path(wd))

			t.Chdir(t.TempDir())

			reg := schema.NewRegistry(schema.WithFS(bundle), schema.WithResolvers(ref))

			require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: x\n")))

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), `$.a: expected "string", got "integer"`)
		})
	}
}

// File reads the working directory once, so the key and the directory
// the file system stands for agree even when another goroutine changes
// directory while File runs. The test changes the process's working
// directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestRegistry_WithFS_ChdirDuringFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("File on Windows reads the working directory again in filepath.Abs")
	}

	bundle := fstest.MapFS{
		"schemas/config.json": &fstest.MapFile{
			Data: []byte(`{"properties": {"a": {"$ref": "defs.json"}}}`),
		},
		"schemas/defs.json": &fstest.MapFile{Data: []byte(`{"type": "string"}`)},
	}

	a, b := t.TempDir(), t.TempDir()
	t.Chdir(a)

	var (
		stop   atomic.Bool
		passes atomic.Int64
		wg     sync.WaitGroup
	)

	// Each pass ends in a, so the directory is a again once the loop stops.
	// The goroutine calls os.Chdir, since t.Chdir would register a cleanup
	// for every change.
	wg.Go(func() {
		for !stop.Load() {
			assert.NoError(t, os.Chdir(b)) //nolint:usetesting // See above.
			assert.NoError(t, os.Chdir(a)) //nolint:usetesting // See above.
			passes.Add(1)
		}
	})

	// Build the refs only once the goroutine changes directory, so the
	// two overlap.
	for passes.Load() == 0 {
		runtime.Gosched()
	}

	// Most of the refs are equal, so keep one of each to validate, keyed
	// by its fields. The loop then builds enough refs to catch a change
	// of directory while File runs, and the test compiles only a few
	// schemas.
	refs := make(map[string]schema.Ref)

	for range 5000 {
		ref := schema.File("schemas/config.json")
		refs[fmt.Sprintf("%#v", ref)] = ref
	}

	stop.Store(true)
	wg.Wait()

	doc := yamltest.FirstDocument(t, "a: x\n")

	for _, ref := range refs {
		reg := schema.NewRegistry(schema.WithFS(bundle), schema.WithResolvers(ref))
		require.NoError(t, reg.Validate(t.Context(), doc))
	}
}

func TestRegistry_Load(t *testing.T) {
	t.Parallel()

	t.Run("a loadable ref returns its bytes", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			return []byte(`{}`), nil
		})

		data, err := schema.NewRegistry().Load(t.Context(), ref)
		require.NoError(t, err)
		assert.Equal(t, []byte(`{}`), data)
	})

	t.Run("a URL ref fetches with the client", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"type": "object"}`))
		}))
		defer server.Close()

		var requests atomic.Int32

		reg := schema.NewRegistry(schema.WithHTTPClient(countingClient(&requests)))

		data, err := reg.Load(t.Context(), schema.URL(server.URL+"/schema.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("a compiled schema and the zero ref have no bytes", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		_, err := reg.Load(t.Context(), schema.MustCompile([]byte(`{}`)).Ref())
		require.ErrorIs(t, err, schema.ErrLoad)

		_, err = reg.Load(t.Context(), schema.Ref{})
		require.ErrorIs(t, err, schema.ErrLoad)
	})
}

func TestRegistry_Schema(t *testing.T) {
	t.Parallel()

	t.Run("compiles a loadable ref once and serves the cache after", func(t *testing.T) {
		t.Parallel()

		var loads atomic.Int32

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			loads.Add(1)

			return []byte(`{"type": "object", "required": ["port"]}`), nil
		})

		reg := schema.NewRegistry()

		first, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)

		second, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Same(t, first, second)
		assert.Equal(t, int32(1), loads.Load())

		// The schema validates a Go value as the one Lookup finds does.
		require.NoError(t, first.ValidateValue(t.Context(), map[string]any{"port": 80}))
		require.Error(t, first.ValidateValue(t.Context(), map[string]any{}))
	})

	t.Run("shares the cache with Lookup", func(t *testing.T) {
		t.Parallel()

		var loads atomic.Int32

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			loads.Add(1)

			return []byte(`{"type": "object"}`), nil
		})

		reg := schema.NewRegistry(schema.WithResolvers(ref))

		fromLookup, err := reg.Lookup(t.Context(), document(t))
		require.NoError(t, err)

		fromRef, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
		assert.Same(t, fromLookup, fromRef)
		assert.Equal(t, int32(1), loads.Load())
	})

	t.Run("compiles with the registry's compile options", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			return []byte(`{"type": "string", "format": "email"}`), nil
		})

		plain, err := schema.NewRegistry().Schema(t.Context(), ref)
		require.NoError(t, err)
		require.NoError(t, plain.ValidateValue(t.Context(), "not an email"))

		strict := schema.NewRegistry(schema.WithCompileOptions(
			schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)),
		))

		checked, err := strict.Schema(t.Context(), ref)
		require.NoError(t, err)
		require.Error(t, checked.ValidateValue(t.Context(), "not an email"))
	})

	t.Run("a compiled schema comes back as it is", func(t *testing.T) {
		t.Parallel()

		compiled := schema.MustCompile([]byte(`{}`))

		got, err := schema.NewRegistry().Schema(t.Context(), compiled.Ref())
		require.NoError(t, err)
		assert.Same(t, compiled, got)
	})

	t.Run("the zero ref names no schema", func(t *testing.T) {
		t.Parallel()

		_, err := schema.NewRegistry().Schema(t.Context(), schema.Ref{})
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorContains(t, err, "names no schema")
		assert.NotContains(t, err.Error(), "resolver")
	})

	t.Run("a load that fails is ErrLoad", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			return nil, errors.New("boom")
		})

		_, err := schema.NewRegistry().Schema(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrLoad)
		assert.True(t, strings.HasPrefix(err.Error(), `load schema: "k": `), err.Error())
	})

	t.Run("bytes that do not compile are ErrCompile", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("k", func(_ context.Context) ([]byte, error) {
			return []byte(`{"type": 42}`), nil
		})

		_, err := schema.NewRegistry().Schema(t.Context(), ref)
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.True(t, strings.HasPrefix(err.Error(), `compile schema: "k": `), err.Error())
	})
}

func TestRegistry_Lookup_NoMatchReasons(t *testing.T) {
	t.Parallel()

	errNoKind := fmt.Errorf("%w: no kind", schema.ErrNoMatch)

	byKind := schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Node) (schema.Ref, error) {
		return schema.Ref{}, errNoKind
	})

	t.Run("nests the reason of each resolver that gave one", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.Directive(),
			schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded([]byte(`{}`))),
			byKind,
		))

		doc := yamltest.FirstDocumentWithPath(t, "kind: Service\n", "app.yaml")

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)
		require.ErrorIs(t, err, errNoKind)
		assert.Equal(t, "app.yaml: no matching schema", err.Error())

		// A resolver that returned ErrNoMatch alone adds no reason, and the
		// reasons read without the sentinel the message states already.
		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		reasons := make([]string, 0, 2)
		for _, child := range bound.Errors() {
			reasons = append(reasons, child.Unwrap().Error())
		}

		assert.Equal(t, []string{"no schema directive", "no kind"}, reasons)
		assert.Equal(t,
			"app.yaml: no matching schema\n|-- no schema directive\n`-- no kind",
			fmt.Sprintf("%+v", err),
		)
	})

	t.Run("reports ErrNoMatch alone when no resolver says more", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(
			schema.When(matcher.Content(kindPath, "Deployment"), schema.Embedded([]byte(`{}`))),
		))

		doc := yamltest.FirstDocumentWithPath(t, "kind: Service\n", "app.yaml")

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		assert.Equal(t, "app.yaml: no matching schema", err.Error())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Empty(t, bound.Errors())
	})

	t.Run("Validate passes the reasons through when it requires a schema", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		doc := yamltest.FirstDocument(t, "kind: Service\n")

		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		require.ErrorIs(t, err, schema.ErrNoDirective)

		lenient := schema.NewRegistry(schema.WithResolvers(schema.Directive()), schema.WithRequireSchema(false))
		require.NoError(t, lenient.Validate(t.Context(), doc))
	})
}

func TestRegistry_Schema_PanicReachesCaller(t *testing.T) {
	t.Parallel()

	// The group runs the load on a goroutine of its own, so a panic in the
	// load must come back to the caller's goroutine, where the caller can
	// recover it.
	reg := schema.NewRegistry()
	ref := schema.Loadable("boom.json", func(_ context.Context) ([]byte, error) {
		panic("boom")
	})

	load := func() {
		_, err := reg.Schema(t.Context(), ref)
		require.NoError(t, err)
	}

	assert.PanicsWithValue(t, "boom", load)

	// The next call loads again rather than hanging on the failed load.
	assert.PanicsWithValue(t, "boom", load)
}

func TestRegistry_Schema_GoexitReachesCaller(t *testing.T) {
	t.Parallel()

	// A load that calls runtime.Goexit, as t.FailNow does, must end the
	// caller's goroutine the same way instead of leaving the caller waiting
	// on a load that never answers.
	reg := schema.NewRegistry()
	ref := schema.Loadable("goexit.json", func(context.Context) ([]byte, error) {
		runtime.Goexit()

		return nil, nil
	})

	load := func() bool {
		var returned bool

		done := make(chan struct{})

		go func() {
			defer close(done)

			_, _ = reg.Schema(t.Context(), ref) //nolint:errcheck // The call exits through runtime.Goexit.
			returned = true
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Schema hung after the load called runtime.Goexit")
		}

		return returned
	}

	assert.False(t, load())

	// The next call loads again rather than hanging on the failed load.
	assert.False(t, load())
}

func TestRegistry_FragmentRefs(t *testing.T) {
	t.Parallel()

	// The root takes any object, and each subschema requires a member.
	// Foo reaches Name through a pointer $ref, which resolves against the
	// root of the document rather than against Foo.
	defsSchema := []byte(`{
		"type": "object",
		"$defs": {
			"Foo": {"$ref": "#/$defs/Name"},
			"Name": {"required": ["name"]},
			"Bar": {"required": ["id"]},
			"Named": {"$anchor": "named", "required": ["name"]}
		}
	}`)

	// Names under .test never resolve, so a fetch that bypasses the
	// registry's client fails rather than going uncounted.
	const baseURL = "http://schemas.test"

	// The handler also answers a spelling of the path with dot segments,
	// as a server that removes them does.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/defs.json" && r.URL.Path != "/x/../defs.json" {
			http.NotFound(w, r)

			return
		}

		//nolint:errcheck // Test helper.
		w.Write(defsSchema)
	})

	// Each subtest takes a client of its own, which serves defs.json from
	// memory and counts every request it sends, whatever the path or host.
	// No server listens on a port, so a connection that another process
	// opens cannot add to the count.
	serve := func() (*http.Client, *atomic.Int32) {
		var requests atomic.Int32

		client := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)

				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, r)

				return rec.Result(), nil
			}),
		}

		return client, &requests
	}

	// A file subtest writes defs.json to a directory of its own.
	defsFile := func(t *testing.T) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), "defs.json")
		require.NoError(t, os.WriteFile(path, defsSchema, 0o600))

		return path
	}

	tcs := map[string]struct {
		ref      func(t *testing.T) schema.Ref
		valid    string
		invalid  string
		want     string
		requests int32
	}{
		"url pointer": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL(baseURL + "/defs.json#/$defs/Foo")
			},
			valid:    "name: x\n",
			invalid:  "other: 1\n",
			want:     `missing required property "name"`,
			requests: 1,
		},
		"url pointer with an escaped unreserved character": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL(baseURL + "/%64efs.json#/$defs/Foo")
			},
			valid:    "name: x\n",
			invalid:  "other: 1\n",
			want:     `missing required property "name"`,
			requests: 1,
		},
		"url pointer with a dot segment": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL(baseURL + "/x/../defs.json#/$defs/Foo")
			},
			valid:    "name: x\n",
			invalid:  "other: 1\n",
			want:     `missing required property "name"`,
			requests: 1,
		},
		"url pointer with an upper-case host": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL("http://Schemas.Test/defs.json#/$defs/Foo")
			},
			valid:    "name: x\n",
			invalid:  "other: 1\n",
			want:     `missing required property "name"`,
			requests: 1,
		},
		"url anchor": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL(baseURL + "/defs.json#named")
			},
			valid:    "name: x\n",
			invalid:  "other: 1\n",
			want:     `missing required property "name"`,
			requests: 1,
		},
		"file url pointer": {
			ref: func(t *testing.T) schema.Ref {
				t.Helper()

				return fileOrURL(t, "", "file://"+filepath.ToSlash(defsFile(t))+"#/$defs/Foo")
			},
			valid:   "name: x\n",
			invalid: "other: 1\n",
			want:    `missing required property "name"`,
		},
		"empty fragment names the root": {
			ref: func(_ *testing.T) schema.Ref {
				return schema.URL(baseURL + "/defs.json#")
			},
			valid:    "other: 1\n",
			invalid:  "- x\n",
			want:     `expected "object", got "array"`,
			requests: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, requests := serve()
			reg := schema.NewRegistry(schema.WithHTTPClient(client), schema.WithResolvers(tc.ref(t)))

			require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, tc.valid)))

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, tc.invalid))
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), tc.want)

			assert.Equal(t, tc.requests, requests.Load())
		})
	}

	t.Run("fragments of one document apply their own subschemas", func(t *testing.T) {
		t.Parallel()

		client, requests := serve()
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		foo, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Foo"))
		require.NoError(t, err)

		bar, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Bar"))
		require.NoError(t, err)

		require.NoError(t, yamltest.FirstDocument(t, "name: x\n").Validate(t.Context(), foo))
		require.ErrorContains(t, yamltest.FirstDocument(t, "id: 1\n").Validate(t.Context(), foo),
			`missing required property "name"`)

		require.NoError(t, yamltest.FirstDocument(t, "id: 1\n").Validate(t.Context(), bar))
		require.ErrorContains(t, yamltest.FirstDocument(t, "name: x\n").Validate(t.Context(), bar),
			`missing required property "id"`)

		// The registry fetches the document once for each schema it compiles.
		assert.Equal(t, int32(2), requests.Load())
	})

	t.Run("a pointer that names nothing does not compile", func(t *testing.T) {
		t.Parallel()

		client, _ := serve()
		reg := schema.NewRegistry(schema.WithHTTPClient(client))

		_, err := reg.Schema(t.Context(), schema.URL(baseURL+"/defs.json#/$defs/Missing"))
		require.ErrorIs(t, err, schema.ErrCompile)
	})
}

func TestCanonicalURL(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		url  string
		want string
	}{
		"canonical url": {
			url:  "https://example.com/defs.json",
			want: "https://example.com/defs.json",
		},
		"fragment": {
			url:  "https://example.com/defs.json#/$defs/Foo",
			want: "https://example.com/defs.json",
		},
		"upper-case scheme and host": {
			url:  "HTTPS://Example.COM/Defs.json",
			want: "https://example.com/Defs.json",
		},
		"escaped unreserved characters": {
			url:  "https://example.com/%64efs%2Ejson?v=%7E1",
			want: "https://example.com/defs.json?v=~1",
		},
		"escaped reserved characters keep upper-case hex": {
			url:  "https://example.com/a%2fb%3a.json?q=%2f",
			want: "https://example.com/a%2Fb%3A.json?q=%2F",
		},
		"dot segments": {
			url:  "https://example.com/a/./b/../defs.json",
			want: "https://example.com/a/defs.json",
		},
		"trailing slash": {
			url:  "https://example.com/a/",
			want: "https://example.com/a/",
		},
		"port": {
			url:  "http://Example.com:8080/defs.json",
			want: "http://example.com:8080/defs.json",
		},
		"file url": {
			url:  "file:///srv/My%20Schemas/defs.json#/$defs/Foo",
			want: "file:///srv/My%20Schemas/defs.json",
		},
		"url that does not parse": {
			url:  "https://example.com:port/defs.json#x",
			want: "https://example.com:port/defs.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, schema.CanonicalURL(tc.url))
		})
	}
}

func TestRegistry_FragmentRefsDraft07(t *testing.T) {
	t.Parallel()

	// Draft-07 ignores the keywords beside a $ref, so Bar takes any
	// string. The tuple document also holds array-form items, which only
	// draft-07 accepts, and a draft-07 fragment $id that names an anchor.
	defsSchema := []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"definitions": {
			"Bar": {"$ref": "#/definitions/Str", "type": "integer"},
			"Str": {"type": "string"}
		}
	}`)
	tupleSchema := []byte(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"definitions": {
			"Str": {"type": "string"},
			"T": {"$id": "#tuple", "items": [{"type": "string"}]}
		}
	}`)

	served := map[string][]byte{
		"/defs.json":  defsSchema,
		"/tuple.json": tupleSchema,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	tcs := map[string]struct {
		path    string
		valid   string
		invalid string
		want    string
	}{
		"keywords beside a $ref": {
			path:    "/defs.json#/definitions/Bar",
			valid:   "x\n",
			invalid: "5\n",
			want:    `expected "string", got "integer"`,
		},
		"a pointer into a document with array-form items": {
			path:    "/tuple.json#/definitions/Str",
			valid:   "x\n",
			invalid: "5\n",
			want:    `expected "string", got "integer"`,
		},
		"a draft-07 anchor with array-form items": {
			path:    "/tuple.json#tuple",
			valid:   "- x\n- 5\n",
			invalid: "- 5\n",
			want:    `expected "string", got "integer"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(schema.URL(server.URL + tc.path)))

			require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, tc.valid)))

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, tc.invalid))
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRegistry_RelativeRefs(t *testing.T) {
	t.Parallel()

	mainSchema := []byte(`{"properties": {"a": {"$ref": "defs.json"}}}`)
	defsSchema := []byte(`{"type": "string"}`)

	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.json"), mainSchema, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "defs.json"), defsSchema, 0o600))

	bundle := fstest.MapFS{
		"schemas/main.json": &fstest.MapFile{Data: mainSchema},
		"schemas/defs.json": &fstest.MapFile{Data: defsSchema},
	}

	// A schema served over HTTP that names the defs file on disk.
	defsURL := "file://" + filepath.ToSlash(filepath.Join(tmpDir, "defs.json"))
	localSchema := fmt.Appendf(nil, `{"properties": {"a": {"$ref": %q}}}`, defsURL)

	// A schema served over HTTP whose $id moves its base to the directory
	// on disk, so "defs.json" names the defs file there.
	idBaseSchema := fmt.Appendf(nil, `{"$id": %q, "properties": {"a": {"$ref": "defs.json"}}}`,
		"file://"+filepath.ToSlash(tmpDir)+"/")

	// A schema served over HTTP that names the defs file on disk from an
	// unknown keyword, which a JSON pointer $ref reaches.
	keywordSchema := fmt.Appendf(nil,
		`{"properties": {"a": {"$ref": "#/x-local"}}, "x-local": {"$ref": %q}}`, defsURL)

	// Schemas served over HTTP that spell the keyword in upper case. The
	// schema decoder reads "$REF" as $ref and "$ID" as $id.
	upperRefSchema := fmt.Appendf(nil, `{"properties": {"a": {"$REF": %q}}}`, defsURL)
	upperIDSchema := fmt.Appendf(nil, `{"$ID": %q, "properties": {"a": {"$ref": "defs.json"}}}`,
		"file://"+filepath.ToSlash(tmpDir)+"/")

	served := map[string][]byte{
		"/s/main.json":     mainSchema,
		"/s/defs.json":     defsSchema,
		"/s/local.json":    localSchema,
		"/s/idbase.json":   idBaseSchema,
		"/s/keyword.json":  keywordSchema,
		"/s/upperref.json": upperRefSchema,
		"/s/upperid.json":  upperIDSchema,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := served[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		//nolint:errcheck // Test helper.
		w.Write(data)
	}))
	t.Cleanup(server.Close)

	tcs := map[string]struct {
		ref  schema.Ref
		opts []schema.RegistryOption
	}{
		"file on disk": {
			ref: schema.File(filepath.Join(tmpDir, "main.json")),
		},
		"file in a file system": {
			ref:  schema.File("schemas/main.json"),
			opts: []schema.RegistryOption{schema.WithFS(bundle)},
		},
		"url": {
			ref: schema.URL(server.URL + "/s/main.json"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(append(tc.opts, schema.WithResolvers(tc.ref))...)

			require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: x\n")))

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), `$.a: expected "string", got "integer"`)
		})
	}

	t.Run("a url schema reads no local file", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.URL(server.URL + "/s/local.json")))

		err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.Contains(t, err.Error(), "cannot resolve $ref")
	})

	t.Run("a url reached from a file schema reads no local file", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			remote string
		}{
			"$ref names the file": {
				remote: "/s/local.json",
			},
			"$id names a file base": {
				remote: "/s/idbase.json",
			},
			"an unknown keyword names the file": {
				remote: "/s/keyword.json",
			},
			"$REF names the file": {
				remote: "/s/upperref.json",
			},
			"$ID names a file base": {
				remote: "/s/upperid.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				root := filepath.Join(t.TempDir(), "root.json")
				rootSchema := fmt.Appendf(nil, `{"$ref": %q}`, server.URL+tc.remote)
				require.NoError(t, os.WriteFile(root, rootSchema, 0o600))

				reg := schema.NewRegistry(schema.WithResolvers(schema.File(root)))

				err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
				require.ErrorIs(t, err, schema.ErrValidate)
				assert.NotContains(t, err.Error(), `expected "string", got "integer"`)
				assert.Contains(t, err.Error(), "names local file")

				// A string fails too, since the registry refuses the remote
				// schema rather than skip the reference.
				err = reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: x\n"))
				require.ErrorContains(t, err, "names local file")
			})
		}
	})
}

func TestRegistry_RefDocuments(t *testing.T) {
	t.Parallel()

	defsFile := filepath.Join(t.TempDir(), "defs.json")
	require.NoError(t, os.WriteFile(defsFile, []byte(`{"type": "string"}`), 0o600))

	defsURL := "file://" + filepath.ToSlash(defsFile)
	namesFile := fmt.Appendf(nil, `{"properties": {"a": {"$ref": %q}}}`, defsURL)

	served := map[string][]byte{
		"/a.json":          []byte(`{"properties": {"x": {"$ref": "defs.json"}}}`),
		"/b.json":          []byte(`{"properties": {"x": {"$ref": "defs.json"}}}`),
		"/defs.json":       []byte(`{"type": "string"}`),
		"/f.json":          []byte(`{"properties": {"b": {"$ref": "flaky.json"}}}`),
		"/flaky.json":      []byte(`{"type": "integer"}`),
		"/g.json":          []byte(`{"properties": {"b": {"$ref": "missing.json"}}}`),
		"/names-file.json": namesFile,
		"/via-remote.json": []byte(`{"$ref": "remote-names-file.json"}`),
		// The remote document the two roots of one subtest share.
		"/remote-names-file.json": namesFile,
	}

	// Count the requests for each path, so a subtest can tell how often the
	// registry fetched a document. The flaky document answers 500 to its
	// first request, and the missing one answers 404 to every request.
	hits := map[string]*atomic.Int32{"/missing.json": {}}
	for path := range served {
		hits[path] = &atomic.Int32{}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter, ok := hits[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		n := counter.Add(1)
		data, ok := served[r.URL.Path]

		switch {
		case r.URL.Path == "/flaky.json" && n == 1:
			http.Error(w, "unavailable", http.StatusInternalServerError)

		case !ok:
			http.NotFound(w, r)

		default:
			//nolint:errcheck // Test helper.
			w.Write(data)
		}
	}))
	t.Cleanup(server.Close)

	t.Run("a document several schemas reference loads once", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()

		for _, path := range []string{"/a.json", "/b.json"} {
			s, err := reg.Schema(t.Context(), schema.URL(server.URL+path))
			require.NoError(t, err)

			err = yamltest.FirstDocument(t, "x: 5\n").Validate(t.Context(), s)
			require.ErrorContains(t, err, `expected "string"`)
		}

		assert.Equal(t, int32(1), hits["/defs.json"].Load())
	})

	t.Run("a document that missed at compile loads once after it recovers", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.URL(server.URL + "/f.json")))

		for range 5 {
			require.NoError(t, reg.Validate(t.Context(), yamltest.FirstDocument(t, "b: 2\n")))
		}

		// The compile missed the document, and the first validation loaded
		// it for every validation after.
		assert.Equal(t, int32(2), hits["/flaky.json"].Load())

		err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "b: x\n"))
		require.ErrorContains(t, err, `expected "integer"`)
	})

	t.Run("a document that fails to load is fetched again", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry(schema.WithResolvers(schema.URL(server.URL + "/g.json")))

		_, err := reg.Schema(t.Context(), schema.URL(server.URL+"/g.json"))
		require.NoError(t, err)

		for i := range 3 {
			before := hits["/missing.json"].Load()

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "b: 2\n"))
			require.ErrorIs(t, err, schema.ErrValidate)
			assert.Equal(t, before+1, hits["/missing.json"].Load(), "validation %d", i)
		}
	})

	t.Run("a url schema still reads no local file after a file schema loaded it", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "root.json")
		require.NoError(t, os.WriteFile(root, namesFile, 0o600))

		reg := schema.NewRegistry(schema.WithResolvers(schema.URL(server.URL + "/names-file.json")))

		s, err := reg.Schema(t.Context(), schema.File(root))
		require.NoError(t, err)

		err = yamltest.FirstDocument(t, "a: 5\n").Validate(t.Context(), s)
		require.ErrorContains(t, err, `expected "string"`)

		err = reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.Contains(t, err.Error(), "cannot resolve $ref")
	})

	t.Run("a cached remote document still cannot name a local file", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join(t.TempDir(), "root.json")
		rootSchema := fmt.Appendf(nil, `{"$ref": %q}`, server.URL+"/remote-names-file.json")
		require.NoError(t, os.WriteFile(root, rootSchema, 0o600))

		reg := schema.NewRegistry(schema.WithResolvers(schema.File(root)))

		// A url root loads the remote document first, which may name a
		// local file there because the file stays out of its reach.
		_, err := reg.Schema(t.Context(), schema.URL(server.URL+"/via-remote.json"))
		require.NoError(t, err)
		require.Equal(t, int32(1), hits["/remote-names-file.json"].Load())

		err = reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.NotContains(t, err.Error(), `expected "string", got "integer"`)
		assert.Contains(t, err.Error(), "names local file")

		// The file root took the document from the registry.
		assert.Equal(t, int32(1), hits["/remote-names-file.json"].Load())
	})
}

func TestRegistry_Schema_RedactsPassword(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"type": 5}`))

		case "/defs.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"$defs": {"Foo": {}}}`))

		case "/garbled.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`not json`))

		case "/d/root.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"properties": {"a": {"$ref": "../anchor.json"}}}`))

		case "/anchor.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"$defs": {"a": {"$anchor": "1bad"}}}`))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	withPassword := strings.Replace(server.URL, "://", "://user:secret@", 1)

	tcs := map[string]struct {
		err error
		url string
	}{
		"a fetch that fails": {
			url: withPassword + "/missing.json",
			err: schema.ErrLoad,
		},
		"a schema that does not compile": {
			url: withPassword + "/bad.json",
			err: schema.ErrCompile,
		},
		"a fragment that names no subschema": {
			url: withPassword + "/defs.json#/$defs/Missing",
			err: schema.ErrCompile,
		},
		"a fragment of a document that does not parse": {
			url: withPassword + "/garbled.json#/$defs/Foo",
			err: schema.ErrCompile,
		},
		"a relative $ref to a document with an invalid anchor": {
			url: withPassword + "/d/root.json",
			err: schema.ErrCompile,
		},
		"a fragment whose relative $ref names an invalid anchor": {
			url: withPassword + "/d/root.json#/properties/a",
			err: schema.ErrCompile,
		},
		"a url that does not parse": {
			url: "http://user:secret@127.0.0.1:port/s.json",
			err: schema.ErrLoad,
		},
		"a password with a slash in a url that does not parse": {
			url: "http://user:secret/x@127.0.0.1:1/s.json",
			err: schema.ErrLoad,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry()

			_, err := reg.Schema(t.Context(), schema.URL(tc.url))
			require.ErrorIs(t, err, tc.err)
			assert.NotContains(t, err.Error(), "secret")
			assert.True(t, strings.HasPrefix(err.Error(), tc.err.Error()+`: "http://user:xxxxx@`), err.Error())
		})
	}
}

func TestRegistry_Validate_RedactsPassword(t *testing.T) {
	t.Parallel()

	// The referenced document answers 500 to its first request, so the
	// compile misses it and the first validation loads it, and then holds
	// an anchor the compiler rejects.
	var lateHits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/d/root.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"properties": {"a": {"$ref": "../late.json"}}}`))

		case "/late.json":
			if lateHits.Add(1) == 1 {
				http.Error(w, "unavailable", http.StatusInternalServerError)

				return
			}

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"$defs": {"a": {"$anchor": "1bad"}}}`))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ref := schema.URL(strings.Replace(server.URL, "://", "://user:secret@", 1) + "/d/root.json")
	reg := schema.NewRegistry(schema.WithResolvers(ref))

	_, err := reg.Schema(t.Context(), ref)
	require.NoError(t, err)

	err = reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
	require.ErrorIs(t, err, schema.ErrValidate)
	assert.Contains(t, err.Error(), "1bad")
	assert.NotContains(t, err.Error(), "secret")
}

func TestRegistry_RefCredentials(t *testing.T) {
	t.Parallel()

	// The server answers only a request that carries the password, so a
	// referenced document loads only when the registry sends it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "user" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		switch r.URL.Path {
		case "/d/root.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"properties": {"a": {"$ref": "../defs.json#/$defs/Str"}}}`))

		case "/defs.json":
			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"$defs": {"Str": {"type": "string"}}}`))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	withPassword := strings.Replace(server.URL, "://", "://user:secret@", 1)

	tcs := map[string]struct {
		url   string
		input string
	}{
		"a whole document": {
			url:   withPassword + "/d/root.json",
			input: "a: 5\n",
		},
		"a fragment": {
			url:   withPassword + "/d/root.json#/properties/a",
			input: "5\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(schema.URL(tc.url)))

			err := reg.Validate(t.Context(), yamltest.FirstDocument(t, tc.input))
			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), `expected "string", got "integer"`)
		})
	}

	t.Run("a document on another host gets no userinfo", func(t *testing.T) {
		t.Parallel()

		var sawAuth atomic.Bool

		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, _, ok := r.BasicAuth(); ok {
				sawAuth.Store(true)
			}

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"type": "string"}`))
		}))
		t.Cleanup(other.Close)

		root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			fmt.Fprintf(w, `{"properties": {"a": {"$ref": %q}}}`, other.URL+"/defs.json")
		}))
		t.Cleanup(root.Close)

		ref := schema.URL(strings.Replace(root.URL, "://", "://user:secret@", 1) + "/root.json")
		reg := schema.NewRegistry(schema.WithResolvers(ref))

		err := reg.Validate(t.Context(), yamltest.FirstDocument(t, "a: 5\n"))
		require.ErrorContains(t, err, `expected "string", got "integer"`)
		assert.False(t, sawAuth.Load())
	})
}
