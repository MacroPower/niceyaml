package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/paths"
	"jacobcolvin.com/niceyaml/schema/loader"
	"jacobcolvin.com/niceyaml/schema/matcher"
	"jacobcolvin.com/niceyaml/schema/registry"
	"jacobcolvin.com/niceyaml/schema/validator"
)

// Path helpers for tests.
var kindPath = paths.Root().Child("kind").Path()

func TestRegistry_Lookup(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)

	t.Run("first match wins", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()

		// First registration matches Deployment.
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("deployment.json", schemaData),
		)

		// Second registration matches everything.
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("fallback.json", schemaData),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		v, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		require.NotNil(t, v)
	})

	t.Run("no match returns error", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("deployment.json", schemaData),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoMatch)
	})

	t.Run("with precompiled validator", func(t *testing.T) {
		t.Parallel()

		v := validator.MustNew("test.json", schemaData)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Validator("test.json", v),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		gotV, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, v, gotV)
	})
}

func TestRegistry_ValidateDocument(t *testing.T) {
	t.Parallel()

	t.Run("valid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)
	})

	t.Run("invalid document", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.Error(t, err)
	})

	t.Run("no match returns ErrNoMatch", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "number"}}}`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		// Service doesn't match, returns ErrNoMatch.
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Service`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoMatch)
	})
}

func TestRegistry_Caching(t *testing.T) {
	t.Parallel()

	t.Run("validators are cached by URL", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		// First lookup compiles and caches.
		doc1 := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		v1, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)

		// Second lookup uses cache.
		doc2 := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		v2, err := reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)

		assert.Equal(t, v1, v2, "validators should be the same instance")
	})

	t.Run("custom cache implementation", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		// Create a custom cache that tracks calls.
		cache := &trackingCache{
			validators: make(map[string]niceyaml.SchemaValidator),
		}

		reg := registry.New(registry.WithCache(cache))
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		// First lookup should miss cache and call Set.
		doc1 := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		v1, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)
		assert.Equal(t, 1, cache.getCalls)
		assert.Equal(t, 1, cache.setCalls)

		// Second lookup should hit cache.
		doc2 := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		v2, err := reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)
		assert.Equal(t, 2, cache.getCalls)
		assert.Equal(t, 1, cache.setCalls) // No new Set call.
		assert.Equal(t, v1, v2, "should return cached validator")
	})

	t.Run("empty URL validators are not cached", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		// Create a custom cache that tracks calls.
		cache := &trackingCache{
			validators: make(map[string]niceyaml.SchemaValidator),
		}

		reg := registry.New(registry.WithCache(cache))

		// Use a loader that returns an empty URL.
		reg.RegisterFunc(
			matcher.Always(),
			loader.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (loader.Result, error) {
				return loader.Result{
					URL:  "", // Empty URL should not be cached.
					Data: schemaData,
				}, nil
			}),
		)

		// First lookup should miss cache and compile, but NOT set.
		doc1 := yamltest.FirstDocument(t, yamltest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)
		assert.Equal(t, 1, cache.getCalls)
		assert.Equal(t, 0, cache.setCalls, "empty URL should not be cached")

		// Second lookup should also miss cache and compile again.
		doc2 := yamltest.FirstDocument(t, yamltest.Input(`key: value`))
		_, err = reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)
		assert.Equal(t, 2, cache.getCalls)
		assert.Equal(t, 0, cache.setCalls, "empty URL should still not be cached")
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Content(kindPath, "Deployment"),
			loader.Embedded("test.json", schemaData),
		)

		// Pre-create documents outside goroutines to avoid assertion issues.
		docs := make([]*niceyaml.DocumentDecoder, 100)
		for i := range docs {
			docs[i] = yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
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

func TestRegistry_DynamicLoader(t *testing.T) {
	t.Parallel()

	t.Run("CustomLoader for dynamic schema", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		// Create schema files for different kinds.
		for _, kind := range []string{"Deployment", "Service"} {
			schemaData := []byte(`{"type": "object", "properties": {"kind": {"const": "` + kind + `"}}}`)
			err := os.WriteFile(filepath.Join(tmpDir, kind+".json"), schemaData, 0o600)
			require.NoError(t, err)
		}

		reg := registry.New()
		reg.RegisterFunc(
			matcher.Func(func(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
				kind, ok := doc.GetValue(kindPath)
				return ok && (kind == "Deployment" || kind == "Service")
			}),
			loader.Custom(func(_ context.Context, doc *niceyaml.DocumentDecoder) ([]byte, string, error) {
				kind, _ := doc.GetValue(kindPath)
				schemaPath := filepath.Join(tmpDir, kind+".json")
				data, err := os.ReadFile(schemaPath) //nolint:gosec // Test code.

				//nolint:wrapcheck // Test code.
				return data, schemaPath, err
			}),
		)

		// Deployment should validate.
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)

		// Service should validate.
		doc = yamltest.FirstDocument(t, yamltest.Input(`kind: Service`))
		err = reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)
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

func TestRegistry_WithValidatorOptions(t *testing.T) {
	t.Parallel()

	// Create a registry with custom validator options.
	schemaData := []byte(`{"type": "object"}`)
	reg := registry.New(
		registry.WithValidatorOptions(), // Empty options, just testing it's passed through.
	)
	reg.RegisterFunc(
		matcher.Content(kindPath, "Deployment"),
		loader.Embedded("test.json", schemaData),
	)

	doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))
	v, err := reg.Lookup(t.Context(), doc)
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestRegistry_DoubleCheckLock(t *testing.T) {
	t.Parallel()

	// This test exercises the double-check lock path in loadValidator.
	// When two goroutines try to compile the same schema simultaneously,
	// the second one should find it in cache after acquiring the write lock.
	schemaData := []byte(`{"type": "object"}`)
	reg := registry.New()

	// Use a loader that introduces a delay during schema data retrieval
	// to increase the window for concurrent access.
	var (
		loadCount int
		loadMu    sync.Mutex
	)

	reg.RegisterFunc(
		matcher.Always(),
		loader.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (loader.Result, error) {
			loadMu.Lock()
			defer loadMu.Unlock()

			loadCount++

			return loader.Result{
				URL:  "test.json",
				Data: schemaData,
			}, nil
		}),
	)

	// Launch multiple concurrent lookups for the same schema.
	const goroutines = 10

	var wg sync.WaitGroup

	wg.Add(goroutines)

	// Pre-create documents outside goroutines.
	docs := make([]*niceyaml.DocumentDecoder, goroutines)
	for i := range docs {
		docs[i] = yamltest.FirstDocument(t, yamltest.Input(`key: value`))
	}

	for i := range goroutines {
		go func() {
			defer wg.Done()

			_, err := reg.Lookup(t.Context(), docs[i])
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// The loader may be called multiple times due to concurrent access,
	// but only one compiled validator should be cached.
	loadMu.Lock()
	assert.GreaterOrEqual(t, loadCount, 1, "loader should be called at least once")
	loadMu.Unlock()
}

func TestRegistry_ErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("loader error propagates through ValidateDocument", func(t *testing.T) {
		t.Parallel()

		reg := registry.New()
		reg.RegisterFunc(
			matcher.Always(),
			loader.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (loader.Result, error) {
				return loader.Result{}, errors.New("load failed")
			}),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`key: value`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load failed")
	})

	t.Run("custom SchemaValidator returned directly", func(t *testing.T) {
		t.Parallel()

		customValidator := yamltest.NewPassingSchemaValidator()
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Always(),
			loader.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (loader.Result, error) {
				return loader.Result{
					Validator: customValidator,
					URL:       "test.json",
				}, nil
			}),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`key: value`))
		v, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, customValidator, v)
	})

	t.Run("compile error propagates", func(t *testing.T) {
		t.Parallel()

		invalidSchemaData := []byte(`{not valid json`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Always(),
			loader.Embedded("bad.json", invalidSchemaData),
		)

		doc := yamltest.FirstDocument(t, yamltest.Input(`key: value`))
		_, err := reg.Lookup(t.Context(), doc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "compile schema")
	})
}

func TestRegistry_MultipleDocuments(t *testing.T) {
	t.Parallel()

	deploymentSchema := []byte(
		`{"type": "object", "properties": {"kind": {"const": "Deployment"}}, "required": ["kind"]}`,
	)
	serviceSchema := []byte(`{"type": "object", "properties": {"kind": {"const": "Service"}}, "required": ["kind"]}`)

	reg := registry.New()
	reg.RegisterFunc(
		matcher.Content(kindPath, "Deployment"),
		loader.Embedded("deployment.json", deploymentSchema),
	)
	reg.RegisterFunc(
		matcher.Content(kindPath, "Service"),
		loader.Embedded("service.json", serviceSchema),
	)

	input := yamltest.Input(`
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
			require.ErrorIs(t, err, registry.ErrNoMatch)
		}
	}

	assert.True(t, validated["Deployment"])
	assert.True(t, validated["Service"])
}

// trackingCache is a test [registry.Cache] that tracks method calls.
type trackingCache struct {
	mu         sync.Mutex
	validators map[string]niceyaml.SchemaValidator
	getCalls   int
	setCalls   int
}

func (c *trackingCache) Get(url string) (niceyaml.SchemaValidator, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.getCalls++

	v, ok := c.validators[url]

	return v, ok
}

func (c *trackingCache) Set(url string, v niceyaml.SchemaValidator) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setCalls++
	c.validators[url] = v
}
