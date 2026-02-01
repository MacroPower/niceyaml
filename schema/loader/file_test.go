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

		l := loader.File(schemaPath)
		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, schemaPath, result.URL)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		l := loader.File("/nonexistent/path/schema.json")
		_, err := l.Load(t.Context(), nil)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /nonexistent/path/schema.json")
	})
}
