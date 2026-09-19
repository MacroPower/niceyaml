package schema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestFile(t *testing.T) {
	t.Parallel()

	t.Run("existing file", func(t *testing.T) {
		t.Parallel()

		// Create temp file with schema.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		_, data, err := load(t, schema.File(schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		// Resolve names the file without touching it; only Load reads it.
		url, _, err := load(t, schema.File("/nonexistent/path/schema.json"))
		assert.Equal(t, "file:///nonexistent/path/schema.json", url)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /nonexistent/path/schema.json")
	})

	t.Run("empty path panics", func(t *testing.T) {
		t.Parallel()

		// An empty path must not resolve to the working directory.
		assert.PanicsWithValue(t, "schema.File: "+schema.ErrEmptyPath.Error(), func() {
			schema.File("")
		})
	})
}

func TestFile_URL(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		want string
	}{
		"absolute path": {
			path: "/schemas/config.json",
			want: "file:///schemas/config.json",
		},
		"unclean absolute path": {
			path: "/schemas/../schemas/./config.json",
			want: "file:///schemas/config.json",
		},
		"space in path": {
			path: "/schemas/my config.json",
			want: "file:///schemas/my%20config.json",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, fileURL(t, tt.path))
		})
	}
}

func TestFile_RelativePath(t *testing.T) {
	t.Parallel()

	// A relative path names the same schema as the absolute path it resolves
	// to against the working directory.
	wd, err := os.Getwd()
	require.NoError(t, err)

	want := fileURL(t, filepath.Join(wd, "schemas", "config.json"))

	for _, path := range []string{"schemas/config.json", "./schemas/config.json"} {
		assert.Equal(t, want, fileURL(t, path), path)
	}
}
