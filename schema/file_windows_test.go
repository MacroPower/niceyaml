//go:build windows

package schema_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Windows reads a rooted path without a drive, such as \proj\x.json, as
// relative, but File makes it absolute on the current drive. A registry
// under AllowRoot reads such a path relative to the directory of its
// root, as it reads a path with a drive, and so does a $ref that names
// one. The test changes the process's working directory to put the
// directory on the current drive, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFile_FSRootedPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	rooted := filepath.ToSlash(strings.TrimPrefix(dir, filepath.VolumeName(dir)))

	yamltest.WriteFiles(t, dir, map[string]string{
		"schemas/main.json": `{"$ref": "` + rooted + `/schemas/defs.json"}`,
		"schemas/defs.json": `{"type": "string"}`,
	})

	tcs := map[string]struct {
		path string
	}{
		"backslashes": {path: filepath.FromSlash(rooted + "/schemas/main.json")},
		"slashes":     {path: rooted + "/schemas/main.json"},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			reg := schema.NewRegistry(withAccess(t, allowRoot(t, dir)))

			s, err := reg.Schema(t.Context(), schema.File(tc.path))
			require.NoError(t, err)

			// The $ref reached defs.json, so a number fails.
			require.NoError(t, s.ValidateValue(t.Context(), "x"))
			require.Error(t, s.ValidateValue(t.Context(), 1))
		})
	}
}
