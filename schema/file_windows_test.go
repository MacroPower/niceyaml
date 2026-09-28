//go:build windows

package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestFile_FSRootedPath(t *testing.T) {
	t.Parallel()

	// Windows reads a rooted path without a drive, such as \proj\x.json,
	// as relative, while File makes it absolute on the current drive. A
	// registry with a file system reads such a path relative to the
	// working directory, as it reads a path with a drive, and so does a
	// $ref that names one.
	cwd, err := os.Getwd()
	require.NoError(t, err)

	rooted := filepath.ToSlash(strings.TrimPrefix(cwd, filepath.VolumeName(cwd)))

	fsys := fstest.MapFS{
		"schemas/main.json": &fstest.MapFile{Data: []byte(`{"$ref": "` + rooted + `/schemas/defs.json"}`)},
		"schemas/defs.json": &fstest.MapFile{Data: []byte(`{"type": "string"}`)},
	}

	tcs := map[string]struct {
		path string
	}{
		"backslashes": {path: filepath.FromSlash(rooted + "/schemas/main.json")},
		"slashes":     {path: rooted + "/schemas/main.json"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithFS(fsys))

			s, err := reg.Schema(t.Context(), schema.File(tc.path))
			require.NoError(t, err)

			// The $ref reached defs.json, so a number fails.
			require.NoError(t, s.ValidateValue(t.Context(), "x"))
			require.Error(t, s.ValidateValue(t.Context(), 1))
		})
	}
}
