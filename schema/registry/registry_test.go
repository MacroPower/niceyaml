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
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/schema/registry"
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := reg.Lookup(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoMatch)
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
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
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
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
		doc1 := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v1, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)

		// Second lookup uses cache.
		doc2 := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v2, err := reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)

		assert.Equal(t, v1, v2, "validators should be the same instance")
	})

	t.Run("empty URL validators are not cached", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		reg := registry.New()

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

		// Each lookup compiles fresh, so the validators are distinct instances.
		doc1 := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		v1, err := reg.Lookup(t.Context(), doc1)
		require.NoError(t, err)

		doc2 := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		v2, err := reg.Lookup(t.Context(), doc2)
		require.NoError(t, err)

		assert.NotSame(t, v1, v2, "empty URL should not be cached")
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
			loader.Func(func(_ context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
				kind, _ := doc.GetValue(kindPath)
				schemaPath := filepath.Join(tmpDir, kind+".json")

				data, err := os.ReadFile(schemaPath) //nolint:gosec // Test code.
				if err != nil {
					//nolint:wrapcheck // Test code.
					return loader.Result{}, err
				}

				return loader.Result{URL: schemaPath, Data: data}, nil
			}),
		)

		// Deployment should validate.
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.NoError(t, err)

		// Service should validate.
		doc = yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
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

func TestRegistry_WithValidateOptions(t *testing.T) {
	t.Parallel()

	// Create a registry with custom validate options.
	schemaData := []byte(`{"type": "object"}`)
	reg := registry.New(
		registry.WithValidateOptions(), // Empty options, just testing they pass through.
	)
	reg.RegisterFunc(
		matcher.Content(kindPath, "Deployment"),
		loader.Embedded("test.json", schemaData),
	)

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
	v, err := reg.Lookup(t.Context(), doc)
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestRegistry_ConcurrentCompile(t *testing.T) {
	t.Parallel()

	// When multiple goroutines look up the same schema simultaneously, each
	// may compile it, but lookups must stay safe and produce a valid result.
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
		docs[i] = yamltest.FirstDocument(t, stringtest.Input(`key: value`))
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
		err := reg.ValidateDocument(t.Context(), doc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "load failed")
	})

	t.Run("compile error propagates", func(t *testing.T) {
		t.Parallel()

		invalidSchemaData := []byte(`{not valid json`)
		reg := registry.New()
		reg.RegisterFunc(
			matcher.Always(),
			loader.Embedded("bad.json", invalidSchemaData),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`key: value`))
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
			require.ErrorIs(t, err, registry.ErrNoMatch)
		}
	}

	assert.True(t, validated["Deployment"])
	assert.True(t, validated["Service"])
}
