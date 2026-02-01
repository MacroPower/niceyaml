package registry_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/registry"
)

func TestDirective(t *testing.T) {
	t.Parallel()

	t.Run("returns MatchLoader implementation", func(t *testing.T) {
		t.Parallel()

		ml := registry.Directive()
		assert.NotNil(t, ml)
	})

	t.Run("options are passed through", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=" + server.URL + "/schema.json\nkind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		customClient := &http.Client{}
		ml := registry.Directive(loader.WithHTTPClient(customClient))

		doc := firstDocumentFromFile(t, yamlPath)
		result, err := ml.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
	})
}

func TestDirective_Match(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup func(t *testing.T) *niceyaml.DocumentDecoder
		want  bool
	}{
		"returns true when document has valid schema directive": {
			setup: func(t *testing.T) *niceyaml.DocumentDecoder {
				t.Helper()

				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "config.yaml")
				yamlData := []byte("# yaml-language-server: $schema=./schema.json\nkind: Deployment\n")
				err := os.WriteFile(yamlPath, yamlData, 0o600)
				require.NoError(t, err)

				return firstDocumentFromFile(t, yamlPath)
			},
			want: true,
		},
		"returns false when document has no directive": {
			setup: func(t *testing.T) *niceyaml.DocumentDecoder {
				t.Helper()

				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "config.yaml")
				yamlData := []byte("kind: Deployment\n")
				err := os.WriteFile(yamlPath, yamlData, 0o600)
				require.NoError(t, err)

				return firstDocumentFromFile(t, yamlPath)
			},
			want: false,
		},
		"returns false when document has nil tokens": {
			setup: func(t *testing.T) *niceyaml.DocumentDecoder {
				t.Helper()

				// NewDocumentDecoder creates a decoder without tokens.
				return firstDocumentWithNilTokens(t, yamltest.Input(`kind: Deployment`))
			},
			want: false,
		},
		"returns false when directive appears after content": {
			setup: func(t *testing.T) *niceyaml.DocumentDecoder {
				t.Helper()

				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "config.yaml")
				yamlData := []byte("kind: Deployment\n# yaml-language-server: $schema=./schema.json\n")
				err := os.WriteFile(yamlPath, yamlData, 0o600)
				require.NoError(t, err)

				return firstDocumentFromFile(t, yamlPath)
			},
			want: false,
		},
		"returns true when directive follows document header": {
			setup: func(t *testing.T) *niceyaml.DocumentDecoder {
				t.Helper()

				tmpDir := t.TempDir()
				yamlPath := filepath.Join(tmpDir, "config.yaml")
				yamlData := []byte("---\n# yaml-language-server: $schema=./schema.json\nkind: Deployment\n")
				err := os.WriteFile(yamlPath, yamlData, 0o600)
				require.NoError(t, err)

				return firstDocumentFromFile(t, yamlPath)
			},
			want: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := tt.setup(t)
			ml := registry.Directive()
			got := ml.Match(t.Context(), doc)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDirective_Load(t *testing.T) {
	t.Parallel()

	t.Run("successfully loads schema from file directive", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		err := os.WriteFile(filepath.Join(tmpDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=schema.json\nkind: Deployment\n")
		err = os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		ml := registry.Directive()
		result, err := ml.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, filepath.Join(tmpDir, "schema.json"), result.URL)
	})

	t.Run("successfully loads schema from URL directive", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=" + server.URL + "/schema.json\nkind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		ml := registry.Directive()
		result, err := ml.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
		assert.Equal(t, server.URL+"/schema.json", result.URL)
	})

	t.Run("returns ErrNoDirective when no directive present", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("kind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		ml := registry.Directive()
		_, err = ml.Load(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoDirective)
	})

	t.Run("returns ErrNoDirective when tokens are nil", func(t *testing.T) {
		t.Parallel()

		// NewDocumentDecoder creates a decoder without tokens.
		doc := firstDocumentWithNilTokens(t, yamltest.Input(`kind: Deployment`))
		ml := registry.Directive()
		_, err := ml.Load(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoDirective)
	})

	t.Run("returns ErrNoFilePath when document has no file path", func(t *testing.T) {
		t.Parallel()

		// Create a document with a directive but no file path (from string input).
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			# yaml-language-server: $schema=./schema.json
			kind: Deployment
		`))
		ml := registry.Directive()
		_, err := ml.Load(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoFilePath)
	})

	t.Run("resolves relative paths against document directory", func(t *testing.T) {
		t.Parallel()

		// Create nested directory structure.
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "configs")
		schemaDir := filepath.Join(tmpDir, "schemas")
		err := os.MkdirAll(configDir, 0o755)
		require.NoError(t, err)

		err = os.MkdirAll(schemaDir, 0o755)
		require.NoError(t, err)

		schemaData := []byte(`{"type": "object"}`)
		err = os.WriteFile(filepath.Join(schemaDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		yamlPath := filepath.Join(configDir, "config.yaml")
		// Relative path from configs/ to schemas/.
		yamlData := []byte("# yaml-language-server: $schema=../schemas/schema.json\nkind: Deployment\n")
		err = os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		ml := registry.Directive()
		result, err := ml.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
	})

	t.Run("returns error when schema file not found", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=nonexistent.json\nkind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		ml := registry.Directive()
		_, err = ml.Load(t.Context(), doc)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read")
		require.ErrorContains(t, err, "nonexistent.json")
	})
}

func TestDirective_CacheInvalidation(t *testing.T) {
	t.Parallel()

	t.Run("different doc pointers trigger reparse", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		// Create two separate files with directives.
		tmpDir := t.TempDir()

		yaml1Path := filepath.Join(tmpDir, "config1.yaml")
		yaml1Data := []byte("# yaml-language-server: $schema=" + server.URL + "/schema.json\nkind: Deployment\n")
		err := os.WriteFile(yaml1Path, yaml1Data, 0o600)
		require.NoError(t, err)

		yaml2Path := filepath.Join(tmpDir, "config2.yaml")
		yaml2Data := []byte("# yaml-language-server: $schema=" + server.URL + "/other-schema.json\nkind: Service\n")
		err = os.WriteFile(yaml2Path, yaml2Data, 0o600)
		require.NoError(t, err)

		ml := registry.Directive()

		// First document should match.
		doc1 := firstDocumentFromFile(t, yaml1Path)
		assert.True(t, ml.Match(t.Context(), doc1))

		result1, err := ml.Load(t.Context(), doc1)
		require.NoError(t, err)
		assert.Equal(t, server.URL+"/schema.json", result1.URL)

		// Second document with different pointer should also match and return
		// different URL (proving cache was invalidated).
		doc2 := firstDocumentFromFile(t, yaml2Path)
		assert.True(t, ml.Match(t.Context(), doc2))

		result2, err := ml.Load(t.Context(), doc2)
		require.NoError(t, err)
		assert.Equal(t, server.URL+"/other-schema.json", result2.URL)

		// Verify the URLs are different (cache was properly invalidated).
		assert.NotEqual(t, result1.URL, result2.URL)
	})

	t.Run("same doc pointer uses cached directive", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		var fetchCount int

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fetchCount++
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=" + server.URL + "/schema.json\nkind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		ml := registry.Directive()
		doc := firstDocumentFromFile(t, yamlPath)

		// Call Match then Load on same doc (simulating Registry behavior).
		assert.True(t, ml.Match(t.Context(), doc))

		result, err := ml.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, server.URL+"/schema.json", result.URL)

		// Only one schema fetch should have occurred (directive was cached).
		assert.Equal(t, 1, fetchCount)
	})
}

// firstDocumentFromFile creates a DocumentDecoder from a YAML file path.
func firstDocumentFromFile(t *testing.T, path string) *niceyaml.DocumentDecoder {
	t.Helper()

	source, err := niceyaml.NewSourceFromFile(path)
	require.NoError(t, err)

	decoder, err := source.Decoder()
	require.NoError(t, err)

	for _, doc := range decoder.Documents() {
		return doc
	}

	t.Fatal("no documents found")

	return nil
}

// firstDocumentWithNilTokens creates a DocumentDecoder with nil tokens for testing.
func firstDocumentWithNilTokens(t *testing.T, input string) *niceyaml.DocumentDecoder {
	t.Helper()

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)
	require.NotEmpty(t, file.Docs)

	return niceyaml.NewDocumentDecoder(file.Docs[0])
}
