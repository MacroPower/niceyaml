package schema_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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

// countingLoader returns a resolver that names key and serves data, counting
// how many times its Load runs.
func countingLoader(key string, data []byte) (schema.Resolver, *atomic.Int32) {
	var loads atomic.Int32

	r := schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
		return schema.Ref{
			Key: key,
			Load: func(_ context.Context) ([]byte, error) {
				loads.Add(1)

				return data, nil
			},
		}, nil
	})

	return r, &loads
}

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)

	t.Run("first match wins", func(t *testing.T) {
		t.Parallel()

		// The second resolver matches everything but is never reached.
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

		// Service doesn't match, returns ErrNoMatch.
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
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
				return schema.Ref{}, cannotDecide
			})),
			schema.WithRequireSchema(false),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err := reg.Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrResolve)
		require.ErrorIs(t, err, cannotDecide)
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

		var fetches atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fetches.Add(1)

			//nolint:errcheck // Test helper.
			w.Write([]byte(`{"type": "object"}`))
		}))
		t.Cleanup(server.Close)

		host := strings.TrimPrefix(server.URL, "http://")

		resolve := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
			// Each document names the same schema with a different scheme
			// case, as two directives in two files might.
			scheme := "http"
			if doc.Index() == 1 {
				scheme = "HTTP"
			}

			return schema.URL(scheme+"://"+host+"/schema.json").Resolve(ctx, doc)
		})

		reg := schema.NewRegistry(schema.WithResolvers(resolve))

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

	t.Run("empty key is rejected", func(t *testing.T) {
		t.Parallel()

		r, loads := countingLoader("", []byte(`{"type": "object"}`))

		reg := schema.NewRegistry(schema.WithResolvers(r))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoKey)
		require.ErrorIs(t, err, schema.ErrResolve)
		assert.Equal(t, int32(0), loads.Load(), "a ref without a key should not be loaded")
	})

	t.Run("load failure is not cached", func(t *testing.T) {
		t.Parallel()

		var loads atomic.Int32

		reg := schema.NewRegistry(
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
				return schema.Ref{
					Key: "flaky.json",
					Load: func(_ context.Context) ([]byte, error) {
						if loads.Add(1) == 1 {
							return nil, errors.New("transient")
						}

						return []byte(`{"type": "object"}`), nil
					},
				}, nil
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

		// Pre-create documents outside goroutines to avoid assertion issues.
		docs := make([]*niceyaml.Document, 100)
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

	// Concurrent lookups for one URL share a single load and compile, and
	// every caller receives the same validator.
	const goroutines = 10

	release := make(chan struct{})

	var loads atomic.Int32

	reg := schema.NewRegistry(
		schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
			return schema.Ref{
				Key: "test.json",
				Load: func(_ context.Context) ([]byte, error) {
					loads.Add(1)
					<-release // Hold the load open until every goroutine has looked up.

					return []byte(`{"type": "object"}`), nil
				},
			}, nil
		})),
	)

	// Pre-create documents outside goroutines.
	docs := make([]*niceyaml.Document, goroutines)
	for i := range docs {
		docs[i] = yamltest.FirstDocument(t, stringtest.Input(`key: value`))
	}

	validators := make([]*schema.Schema, goroutines)

	var (
		started sync.WaitGroup
		done    sync.WaitGroup
	)

	started.Add(goroutines)
	done.Add(goroutines)

	for i := range goroutines {
		go func() {
			defer done.Done()

			started.Done()

			v, err := reg.Lookup(t.Context(), docs[i])
			assert.NoError(t, err)

			validators[i] = v
		}()
	}

	started.Wait()
	close(release)
	done.Wait()

	assert.Equal(t, int32(1), loads.Load(), "concurrent lookups should share one load")

	for i := 1; i < goroutines; i++ {
		assert.Same(t, validators[0], validators[i])
	}
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
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
						return schema.Ref{
							Key: "slow.json",
							Load: func(_ context.Context) ([]byte, error) {
								loads.Add(1)
								<-release

								return nil, fmt.Errorf("fetch slow.json: %w", context.DeadlineExceeded)
							},
						}, nil
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
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
						return schema.Ref{
							Key: "slow.json",
							Load: func(_ context.Context) ([]byte, error) {
								<-release

								return schemaData, nil
							},
						}, nil
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
					schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
						return schema.Ref{
							Key: "slow.json",
							Load: func(ctx context.Context) ([]byte, error) {
								if loads.Add(1) == 1 {
									// The first load runs under the starting caller's context
									// and ends with its cancellation.
									<-ctx.Done()

									return nil, fmt.Errorf("fetch slow.json: %w", ctx.Err())
								}

								return schemaData, nil
							},
						}, nil
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

			require.ErrorIs(t, leaderErr, context.Canceled)
			require.NoError(t, joinerErr)
			assert.Equal(t, int32(2), loads.Load())
		})
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
				schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
					kind, err := doc.Get[string](ctx, kindPath)
					if err != nil || (kind != "Deployment" && kind != "Service") {
						return schema.Ref{}, schema.ErrNoMatch
					}

					return schema.File(filepath.Join(tmpDir, kind+".json")).Resolve(ctx, doc)
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

func TestRegistry_WithCompileOptions(t *testing.T) {
	t.Parallel()

	// Create a registry with custom compile options.
	schemaData := []byte(`{"type": "object"}`)
	reg := schema.NewRegistry(
		schema.WithCompileOptions(), // Empty options, just testing they pass through.
		schema.WithResolvers(schema.When(
			matcher.Content(kindPath, "Deployment"),
			schema.Embedded(schemaData),
		)),
	)

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
	v, err := reg.Lookup(t.Context(), doc)
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestRegistry_CompileOptionsNotAliased(t *testing.T) {
	t.Parallel()

	// The registry compiles each schema on its first lookup, so aliasing
	// the caller's slice would let a later write change how the next
	// schema compiles. Asserting formats is what makes the difference
	// observable here.
	opts := []schema.CompileOption{schema.WithJSONSchemaOptions(jsonschema.WithFormats(true))}

	reg := schema.NewRegistry(
		schema.WithCompileOptions(opts...),
		schema.WithResolvers(schema.Embedded([]byte(`{"type": "string", "format": "ipv4"}`))),
	)

	opts[0] = schema.WithJSONSchemaOptions(jsonschema.WithFormats(false))

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
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
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
			schema.WithResolvers(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
				return schema.Ref{
					Key: "broken.json",
					Load: func(_ context.Context) ([]byte, error) {
						return nil, errors.New("disk on fire")
					},
				}, nil
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
		kind, err := doc.Get[string](t.Context(), kindPath)
		require.NoError(t, err)

		err = reg.Validate(t.Context(), doc)

		if kind == "Deployment" || kind == "Service" {
			require.NoError(t, err, "expected %s to validate", kind)

			validated[kind] = true
		} else {
			// ConfigMap has no matching schema, returns ErrNoMatch.
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
