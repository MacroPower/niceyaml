package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSources(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// Files to load, relative to a temporary directory.
		files []string
		// The file among files that the test leaves unwritten, or empty
		// when it writes every file.
		missing string
		err     error
	}{
		"distinct base names": {
			files: []string{"a/one.yaml", "b/two.yaml"},
		},
		"shared base name": {
			files: []string{"a/config.yaml", "b/config.yaml"},
		},
		"missing file": {
			files:   []string{"a/config.yaml", "b/config.yaml"},
			missing: "b/config.yaml",
			err:     fs.ErrNotExist,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			paths := make([]string, len(tc.files))
			for i, file := range tc.files {
				paths[i] = filepath.Join(dir, file)
				if file == tc.missing {
					continue
				}

				require.NoError(t, os.MkdirAll(filepath.Dir(paths[i]), 0o750))
				require.NoError(t, os.WriteFile(paths[i], []byte("a: 1\n"), 0o600))
			}

			sources, err := loadSources(paths)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				// The read error names the path, and loadSources adds no
				// second copy.
				assert.Equal(t, 1, strings.Count(err.Error(), filepath.Join(dir, tc.missing)))

				return
			}

			require.NoError(t, err)
			require.Len(t, sources, len(paths))

			labels := revisionLabels(paths)
			for i, source := range sources {
				assert.Equal(t, labels[i], source.Name())
				assert.Equal(t, paths[i], source.FilePath())
			}

			m := newModel(&modelOptions{sources: sources})
			assert.Equal(t, labels, m.viewport.RevisionNames())
		})
	}
}
