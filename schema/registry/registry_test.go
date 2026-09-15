package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/schema/registry"
)

// Path helpers for tests.
var kindPath = paths.Root().Child("kind").Path()

// countingLoader returns a resolver that names url and serves data, counting
// how many times its Load runs.
func countingLoader(url string, data []byte) (schema.Resolver, *atomic.Int32) {
	var loads atomic.Int32

	r := schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
		return schema.Ref{
			URL: url,
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

		reg := registry.New()

		// First registration matches Deployment.
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("deployment.json", schemaData),
		))

		// Second registration matches everything but is never reached.
		fallback, fallbackLoads := countingLoader("fallback.json", schemaData)
		reg.Register(fallback)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, int32(0), fallbackLoads.Load())
	})

	t.Run("loader alone applies to every document", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.Register(loader.Embedded("any.json", schemaData))

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `other: value`} {
			doc := yamltest.FirstDocument(t, stringtest.Input(input))
			_, err := reg.Lookup(t.Context(), doc)
			require.NoError(t, err)
		}
	})

	t.Run("no match returns error", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("deployment.json", schemaData),
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("empty registry matches nothing", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("variadic Register keeps order", func(t *testing.T) {
		t.Parallel()

		first, firstLoads := countingLoader("first.json", schemaData)
		second, secondLoads := countingLoader("second.json", schemaData)

		reg := registry.New()
		reg.Register(first, second)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		_, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(1), firstLoads.Load())
		assert.Equal(t, int32(0), secondLoads.Load())
	})
}

func TestRegistry_ValidateDocument(t *testing.T) {
	t.Parallel()

	t.Run("valid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)
	})

	t.Run("invalid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.Error(t, err)

		var validationErr *niceyaml.Error

		require.ErrorAs(t, err, &validationErr)
		assert.Equal(t, "$.kind", validationErr.Path())
	})

	t.Run("no match returns ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		))

		// Service doesn't match, returns ErrNoMatch.
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})
}

func TestRegistry_Caching(t *testing.T) {
	t.Parallel()

	t.Run("validators are cached by URL", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		))

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

		reg := registry.New()
		reg.Register(r)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		for range 5 {
			err := reg.ValidateDocument(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), loads.Load(), "the cache should be checked before loading")
	})

	t.Run("distinct URLs load separately", func(t *testing.T) {
		t.Parallel()

		deployment, deploymentLoads := countingLoader("deployment.json", []byte(`{"type": "object"}`))
		service, serviceLoads := countingLoader("service.json", []byte(`{"type": "object"}`))

		reg := registry.New()
		reg.Register(
			registry.When(matcher.Content(kindPath, "Deployment"), deployment),
			registry.When(matcher.Content(kindPath, "Service"), service),
		)

		for _, input := range []string{`kind: Deployment`, `kind: Service`, `kind: Deployment`, `kind: Service`} {
			doc := yamltest.FirstDocument(t, stringtest.Input(input))
			err := reg.ValidateDocument(t.Context(), doc)
			require.NoError(t, err)
		}

		assert.Equal(t, int32(1), deploymentLoads.Load())
		assert.Equal(t, int32(1), serviceLoads.Load())
	})

	t.Run("empty URL is rejected", func(t *testing.T) {
		t.Parallel()

		r, loads := countingLoader("", []byte(`{"type": "object"}`))

		reg := registry.New()
		reg.Register(r)

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoURL)
		require.ErrorIs(t, err, registry.ErrResolve)
		assert.Equal(t, int32(0), loads.Load(), "a ref without a URL should not be loaded")
	})

	t.Run("load failure is not cached", func(t *testing.T) {
		t.Parallel()

		var loads atomic.Int32

		reg := registry.New()
		reg.Register(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
			return schema.Ref{
				URL: "flaky.json",
				Load: func(_ context.Context) ([]byte, error) {
					if loads.Add(1) == 1 {
						return nil, errors.New("transient")
					}

					return []byte(`{"type": "object"}`), nil
				},
			}, nil
		}))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))

		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrLoad)
		require.ErrorContains(t, err, "transient")

		_, err = reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(2), loads.Load())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)
		reg := registry.New()
		reg.Register(registry.When(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		))

		// Pre-create documents outside goroutines to avoid assertion issues.
		docs := make([]*niceyaml.DocumentDecoder, 100)
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

	reg := registry.New()
	reg.Register(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
		return schema.Ref{
			URL: "test.json",
			Load: func(_ context.Context) ([]byte, error) {
				loads.Add(1)
				<-release // Hold the load open until every goroutine has looked up.

				return []byte(`{"type": "object"}`), nil
			},
		}, nil
	}))

	// Pre-create documents outside goroutines.
	docs := make([]*niceyaml.DocumentDecoder, goroutines)
	for i := range docs {
		docs[i] = yamltest.FirstDocument(t, stringtest.Input(`key: value`))
	}

	validators := make([]*schema.Validator, goroutines)

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

func TestRegistry_Register_Concurrent(t *testing.T) {
	t.Parallel()

	// Registering while lookups run must not race.
	schemaData := []byte(`{"type": "object"}`)
	reg := registry.New()
	reg.Register(loader.Embedded("base.json", schemaData))

	doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(2)

		go func() {
			defer wg.Done()

			reg.Register(loader.Embedded("extra.json", schemaData))
		}()

		go func() {
			defer wg.Done()

			_, err := reg.Lookup(t.Context(), doc)
			assert.NoError(t, err, "lookup %d", i)
		}()
	}

	wg.Wait()
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

		reg := registry.New()
		reg.Register(schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.DocumentDecoder) (schema.Ref, error) {
			kind, ok := doc.GetValue(kindPath)
			if !ok || (kind != "Deployment" && kind != "Service") {
				return schema.Ref{}, schema.ErrNoMatch
			}

			return loader.File(filepath.Join(tmpDir, kind+".json")).Resolve(ctx, doc)
		}))

		// Deployment should validate.
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)

		// Service should validate.
		doc = yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		err = reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)

		// ConfigMap has no schema.
		doc = yamltest.FirstDocument(t, stringtest.Input(`kind: ConfigMap`))
		err = reg.ValidateDocument(t.Context(), doc)
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

		reg := registry.New()
		reg.Register(registry.Directive())

		source, err := niceyaml.NewSourceFromFile(yamlPath)
		require.NoError(t, err)

		decoder, err := source.Decoder()
		require.NoError(t, err)

		for _, doc := range decoder.Documents() {
			err = reg.ValidateDocument(t.Context(), doc)
			require.NoError(t, err)
		}
	})
}

func TestRegistry_WithValidateOptions(t *testing.T) {
	t.Parallel()

	// Create a registry with custom validate options.
	schemaData := []byte(`{"type": "object"}`)
	reg := registry.New(
		registry.WithValidateOptions(), // Empty options, just testing they pass through.
	)
	reg.Register(registry.When(
		matcher.Content(kindPath, "Deployment"),
		loader.Embedded("test.json", schemaData),
	))

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
	v, err := reg.Lookup(t.Context(), doc)
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestRegistry_ErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("resolve error propagates", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.Register(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
			return schema.Ref{}, errors.New("cannot decide")
		}))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrResolve)
		assert.Contains(t, err.Error(), "cannot decide")
	})

	t.Run("load error propagates through ValidateDocument", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.Register(schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
			return schema.Ref{
				URL: "broken.json",
				Load: func(_ context.Context) ([]byte, error) {
					return nil, errors.New("disk on fire")
				},
			}, nil
		}))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrLoad)
		assert.Contains(t, err.Error(), "disk on fire")
		assert.Contains(t, err.Error(), "broken.json")
	})

	t.Run("compile error propagates", func(t *testing.T) {
		t.Parallel()

		invalidSchemaData := []byte(`{not valid json`)
		reg := registry.New()
		reg.Register(loader.Embedded("bad.json", invalidSchemaData))

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrCompile)
		assert.Contains(t, err.Error(), "bad.json")
	})
}

func TestRegistry_MultipleDocuments(t *testing.T) {
	t.Parallel()

	deploymentSchema := []byte(
		`{"type": "object", "properties": {"kind": {"const": "Deployment"}}, "required": ["kind"]}`,
	)
	serviceSchema := []byte(`{"type": "object", "properties": {"kind": {"const": "Service"}}, "required": ["kind"]}`)

	reg := registry.New()
	reg.Register(
		registry.When(matcher.Content(kindPath, "Deployment"), loader.Embedded("deployment.json", deploymentSchema)),
		registry.When(matcher.Content(kindPath, "Service"), loader.Embedded("service.json", serviceSchema)),
	)

	input := stringtest.Input(`
		kind: Deployment
		---
		kind: Service
		---
		kind: ConfigMap
	`)

	source := niceyaml.NewSourceFromString(input)
	decoder, err := source.Decoder()
	require.NoError(t, err)

	// Track validation results.
	validated := make(map[string]bool)
	for _, doc := range decoder.Documents() {
		kind, _ := doc.GetValue(kindPath)
		err := reg.ValidateDocument(t.Context(), doc)

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
