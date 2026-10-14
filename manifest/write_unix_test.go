//go:build unix

package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestPlan_Apply_ReadOnlyFile(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		change func(t *testing.T, path string)
		err    string
	}{
		"file with two hard links": {
			change: func(t *testing.T, path string) {
				t.Helper()

				require.NoError(t, os.Link(path, filepath.Join(t.TempDir(), "other.yaml")))
			},
			err: stringtest.JoinLF(
				"hours.yaml: cannot rewrite a file with 2 hard links",
				"|-- a rewrite replaces the file, so the other links keep the old text",
				"`-- replace the file with a copy to break the links, then run the command again",
			),
		},
		"file without write permission": {
			change: func(t *testing.T, path string) {
				t.Helper()

				require.NoError(t, os.Chmod(path, 0o444))
			},
			err: "hours.yaml: cannot rewrite a read-only file",
		},
		"file in a directory without write permission": {
			change: func(t *testing.T, path string) {
				t.Helper()

				dir := filepath.Dir(path)
				require.NoError(t, os.Chmod(dir, 0o555))
				t.Cleanup(func() { assert.NoError(t, os.Chmod(dir, 0o755)) })
			},
			err: "hours.yaml: cannot rewrite a file in a read-only directory",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
			path := filepath.Join(config, "hours.yaml")

			tc.change(t, path)

			res, err := plan.Apply(t.Context())
			require.NoError(t, err)
			require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
			assert.Equal(t, config+"/"+tc.err, niceyaml.FormatError(res.Err()))
			assert.Equal(t, hoursDoc, yamltest.ReadFile(t, path))
		})
	}
}
