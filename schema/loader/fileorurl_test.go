package loader_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/loader"
)

func TestFileOrURL(t *testing.T) {
	t.Parallel()

	t.Run("relative path", func(t *testing.T) {
		t.Parallel()

		// Create temp schema file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		url, data, err := load(t, loader.FileOrURL(tmpDir, "schema.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
	})

	t.Run("relative path without base directory", func(t *testing.T) {
		t.Parallel()

		r := loader.FileOrURL("", "schema.json")
		_, err := r.Resolve(t.Context(), nil)
		require.ErrorIs(t, err, loader.ErrNoBaseDir)
		require.ErrorContains(t, err, `"schema.json"`)
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
		url, data, err := load(t, loader.FileOrURL("/some/other/dir", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
	})

	t.Run("absolute path without base directory", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		url, data, err := load(t, loader.FileOrURL("", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
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
		url, data, err := load(t, loader.FileOrURL("/some/dir", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("URL without base directory", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := server.URL + "/schema.json"

		url, data, err := load(t, loader.FileOrURL("", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("URL scheme in upper case", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := "HTTP://" + strings.TrimPrefix(server.URL, "http://") + "/schema.json"

		url, data, err := load(t, loader.FileOrURL("/some/dir", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("file URL", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		// BaseDir is ignored for file URLs, which name an absolute path.
		url, data, err := load(t, loader.FileOrURL("/some/other/dir", "file://"+schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
	})

	t.Run("file URL scheme in upper case", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		_, data, err := load(t, loader.FileOrURL("/some/other/dir", "FILE://"+schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("file URL with percent-encoded path", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "my schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		encoded := "file://" + filepath.Join(tmpDir, "my%20schema.json")

		url, data, err := load(t, loader.FileOrURL("/some/other/dir", encoded))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
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
		r := loader.FileOrURL("/dir", server.URL+"/schema.json", loader.WithHTTPClient(customClient))

		_, data, err := load(t, r)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, loader.FileOrURL("/some/dir", "nonexistent.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /some/dir/nonexistent.json")
	})
}
