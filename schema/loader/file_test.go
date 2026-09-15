package loader_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/loader"
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

		url, data, err := load(t, loader.File(schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, schemaPath, url)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		// Resolve names the file without touching it; only Load reads it.
		url, _, err := load(t, loader.File("/nonexistent/path/schema.json"))
		assert.Equal(t, "/nonexistent/path/schema.json", url)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /nonexistent/path/schema.json")
	})
}
