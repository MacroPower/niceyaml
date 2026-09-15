package registry_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/registry"
)

// resolveAndLoad resolves doc through res and loads the schema it names,
// returning the ref's URL alongside the loaded bytes. Both steps must
// succeed.
func resolveAndLoad(t *testing.T, res schema.Resolver, doc *niceyaml.DocumentDecoder) (string, []byte) {
	t.Helper()

	ref, err := res.Resolve(t.Context(), doc)
	require.NoError(t, err)

	data, err := ref.Load(t.Context())
	require.NoError(t, err)

	return ref.URL, data
}

func TestDirective(t *testing.T) {
	t.Parallel()

	t.Run("returns Resolver implementation", func(t *testing.T) {
		t.Parallel()

		res := registry.Directive()
		assert.NotNil(t, res)
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
		res := registry.Directive(loader.WithHTTPClient(customClient))

		doc := firstDocumentFromFile(t, yamlPath)
		_, data := resolveAndLoad(t, res, doc)
		assert.Equal(t, []byte(schemaData), data)
	})
}

func TestDirective_Resolve_Match(t *testing.T) {
	t.Parallel()

	// A resolver "matches" when it reports anything other than ErrNoMatch.
	// Loading the named schema may still fail, which is a match.
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
				return firstDocumentWithNilTokens(t, stringtest.Input(`kind: Deployment`))
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
			res := registry.Directive()
			_, err := res.Resolve(t.Context(), doc)

			got := !errors.Is(err, schema.ErrNoMatch)
			assert.Equal(t, tt.want, got)

			if !tt.want {
				require.ErrorIs(t, err, registry.ErrNoDirective)
			}
		})
	}
}

func TestDirective_Resolve(t *testing.T) {
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
		url, data := resolveAndLoad(t, registry.Directive(), doc)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, filepath.Join(tmpDir, "schema.json"), url)
	})

	t.Run("trims trailing whitespace from the directive", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(filepath.Join(tmpDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=./schema.json   \nkind: Deployment\n")
		err = os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		url, data := resolveAndLoad(t, registry.Directive(), doc)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, filepath.Join(tmpDir, "schema.json"), url)
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
		url, data := resolveAndLoad(t, registry.Directive(), doc)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, server.URL+"/schema.json", url)
	})

	t.Run("returns ErrNoDirective when no directive present", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("kind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		res := registry.Directive()
		_, err = res.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoDirective)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("returns ErrNoDirective when tokens are nil", func(t *testing.T) {
		t.Parallel()

		// NewDocumentDecoder creates a decoder without tokens.
		doc := firstDocumentWithNilTokens(t, stringtest.Input(`kind: Deployment`))
		res := registry.Directive()
		_, err := res.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoDirective)
	})

	t.Run("returns ErrNoFilePath when document has no file path", func(t *testing.T) {
		t.Parallel()

		// Create a document with a directive but no file path (from string input).
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			# yaml-language-server: $schema=./schema.json
			kind: Deployment
		`))
		res := registry.Directive()
		_, err := res.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, registry.ErrNoFilePath)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
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
		_, data := resolveAndLoad(t, registry.Directive(), doc)
		assert.Equal(t, schemaData, data)
	})

	t.Run("names a missing schema file and fails on load", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		yamlPath := filepath.Join(tmpDir, "config.yaml")
		yamlData := []byte("# yaml-language-server: $schema=nonexistent.json\nkind: Deployment\n")
		err := os.WriteFile(yamlPath, yamlData, 0o600)
		require.NoError(t, err)

		doc := firstDocumentFromFile(t, yamlPath)
		res := registry.Directive()
		ref, err := res.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tmpDir, "nonexistent.json"), ref.URL)

		_, err = ref.Load(t.Context())
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read")
		require.ErrorContains(t, err, "nonexistent.json")
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

	return niceyaml.NewDocumentDecoder(file.Docs[0], niceyaml.DocumentContext{})
}
