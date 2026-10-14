package yamltest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestMapFS(t *testing.T) {
	t.Parallel()

	fsys := yamltest.MapFS(map[string]string{
		"a.yaml":     "a: 1\n",
		"dir/b.yaml": "b: 2\n",
	})

	require.NoError(t, fstest.TestFS(fsys, "a.yaml", "dir/b.yaml"))

	data, err := fs.ReadFile(fsys, "dir/b.yaml")
	require.NoError(t, err)

	assert.Equal(t, "b: 2\n", string(data))
}

func TestWriteFile(t *testing.T) {
	t.Parallel()

	t.Run("the file and its parents come into being", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := yamltest.WriteFile(t, dir, "sub/deep/a.yaml", "a: 1\n")

		assert.Equal(t, filepath.Join(dir, "sub", "deep", "a.yaml"), path)
		assert.Equal(t, "a: 1\n", yamltest.ReadFile(t, path))
	})

	t.Run("a parent that is a file fails the test", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		yamltest.WriteFile(t, dir, "a.yaml", "a: 1\n")

		rec := record(func(tb *recordingTB) {
			yamltest.WriteFile(tb, dir, "a.yaml/b.yaml", "b: 2\n")
		})

		assert.True(t, rec.failed)
	})
}

func TestWriteFiles(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"a.yaml":     "a: 1\n",
		"dir/b.yaml": "b: 2\n",
	}

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, files)

	for name, text := range files {
		assert.Equal(t, text, yamltest.ReadFile(t, filepath.Join(dir, filepath.FromSlash(name))), name)
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.yaml")

	rec := record(func(tb *recordingTB) {
		yamltest.ReadFile(tb, missing)
	})

	assert.True(t, rec.failed, "a missing file fails the test")
}

func TestSymlinkOrSkip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := yamltest.WriteFile(t, dir, "a.yaml", "a: 1\n")
	link := filepath.Join(dir, "link.yaml")

	yamltest.SymlinkOrSkip(t, target, link)

	assert.Equal(t, "a: 1\n", yamltest.ReadFile(t, link))
}

// TestRemoveWorkingDirectory removes the working directory of the
// process, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestRemoveWorkingDirectory(t *testing.T) {
	yamltest.RemoveWorkingDirectory(t)

	_, err := os.Getwd()
	require.Error(t, err)
}
