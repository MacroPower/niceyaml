package schema_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
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

		url, data, err := load(t, schema.FileOrURL(tmpDir, "schema.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("relative path without base directory", func(t *testing.T) {
		t.Parallel()

		r := schema.FileOrURL("", "schema.json")
		_, err := r.Resolve(t.Context(), document(t))
		require.ErrorIs(t, err, schema.ErrNoBaseDir)
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
		url, data, err := load(t, schema.FileOrURL("/some/other/dir", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("absolute path without base directory", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		url, data, err := load(t, schema.FileOrURL("", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
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
		url, data, err := load(t, schema.FileOrURL("/some/dir", schemaURL))
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

		url, data, err := load(t, schema.FileOrURL("", schemaURL))
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

		url, data, err := load(t, schema.FileOrURL("/some/dir", schemaURL))
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
		url, data, err := load(t, schema.FileOrURL("/some/other/dir", "file://"+schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("file URL scheme in upper case", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		_, data, err := load(t, schema.FileOrURL("/some/other/dir", "FILE://"+schemaPath))
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

		url, data, err := load(t, schema.FileOrURL("/some/other/dir", encoded))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
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
		r := schema.FileOrURL("/dir", server.URL+"/schema.json", schema.WithHTTPClient(customClient))

		_, data, err := load(t, r)
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, schema.FileOrURL("/some/dir", "nonexistent.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /some/dir/nonexistent.json")
	})
}

func TestFileURLPath(t *testing.T) {
	t.Parallel()

	// The result is in the platform's native separator, so a Windows-shaped
	// URL yields C:/schemas/config.json here and C:\schemas\config.json on
	// Windows. Either way the drive letter comes first, so filepath.IsAbs
	// reports the path absolute on Windows.
	tcs := map[string]struct {
		ref  string
		want string
	}{
		"unix path": {
			ref:  "file:///srv/schemas/config.json",
			want: filepath.FromSlash("/srv/schemas/config.json"),
		},
		"windows drive letter": {
			ref:  "file:///C:/schemas/config.json",
			want: filepath.FromSlash("C:/schemas/config.json"),
		},
		"windows drive letter in lower case": {
			ref:  "file:///c:/schemas/config.json",
			want: filepath.FromSlash("c:/schemas/config.json"),
		},
		"windows drive letter with localhost": {
			ref:  "file://localhost/C:/schemas/config.json",
			want: filepath.FromSlash("C:/schemas/config.json"),
		},
		"drive letter alone": {
			ref:  "file:///C:",
			want: "C:",
		},
		"colon after a digit is not a drive": {
			ref:  "file:///1:/schemas/config.json",
			want: filepath.FromSlash("/1:/schemas/config.json"),
		},
		"colon in a longer first segment is not a drive": {
			ref:  "file:///ab:/schemas/config.json",
			want: filepath.FromSlash("/ab:/schemas/config.json"),
		},
		"percent-encoded path": {
			ref:  "file:///srv/my%20schemas/config.json",
			want: filepath.FromSlash("/srv/my schemas/config.json"),
		},
		"remote host is left as written": {
			ref:  "file://host/C:/schemas/config.json",
			want: "file://host/C:/schemas/config.json",
		},
		"empty path is left as written": {
			ref:  "file://",
			want: "file://",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, schema.FileURLPath(tc.ref))
		})
	}
}
