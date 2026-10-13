//go:build unix

package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestPlan_Apply_HardLinks(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
	path := filepath.Join(config, "hours.yaml")
	require.NoError(t, os.Link(path, filepath.Join(t.TempDir(), "other.yaml")))

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.Equal(t, ""+
		path+": cannot rewrite a file with 2 hard links\n"+
		"|-- a rewrite replaces the file, so the other links keep the old text\n"+
		"`-- replace the file with a copy to break the links, then run the command again",
		niceyaml.FormatError(res.Err()))
	assert.Equal(t, hoursDoc, readFile(t, path))
}

func TestPlan_Apply_ReadOnlyFile(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
	path := filepath.Join(config, "hours.yaml")
	require.NoError(t, os.Chmod(path, 0o444))

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.Equal(t, path+": cannot rewrite a read-only file", res.Err().Error())
	assert.Equal(t, hoursDoc, readFile(t, path))
}

func TestPlan_Apply_ReadOnlyDirectory(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
	path := filepath.Join(config, "hours.yaml")
	require.NoError(t, os.Chmod(config, 0o555))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(config, 0o755)) })

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.Equal(t, path+": cannot rewrite a file in a read-only directory", res.Err().Error())
	assert.Equal(t, hoursDoc, readFile(t, path))
}
