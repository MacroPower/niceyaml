package loader_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/loader"
)

func TestRef(t *testing.T) {
	t.Parallel()

	t.Run("relative path", func(t *testing.T) {
		t.Parallel()

		// Create temp schema file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		l := loader.Ref(tmpDir, "schema.json")
		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, schemaPath, result.URL)
	})

	t.Run("absolute path", func(t *testing.T) {
		t.Parallel()

		// Create temp schema file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		// BaseDir is ignored for absolute paths.
		l := loader.Ref("/some/other/dir", schemaPath)
		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, schemaPath, result.URL)
	})

	t.Run("URL schema", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := server.URL + "/schema.json"

		// BaseDir is ignored for URLs.
		l := loader.Ref("/some/dir", schemaURL)
		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
		assert.Equal(t, schemaURL, result.URL)
	})

	t.Run("URL with custom client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		customClient := &http.Client{}
		l := loader.Ref("/dir", server.URL+"/schema.json", loader.WithHTTPClient(customClient))
		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), result.Data)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		l := loader.Ref("/some/dir", "nonexistent.json")
		_, err := l.Load(t.Context(), nil)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /some/dir/nonexistent.json")
	})
}
