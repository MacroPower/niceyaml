package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestPlan_Apply_Link(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}

	dir := t.TempDir()
	tree{
		files: map[string]string{"config/shared/hours.yaml": hoursDoc},
		links: map[string]string{"config/hours.yaml": "shared/hours.yaml"},
	}.write(t, dir)

	config := filepath.Join(dir, "config")

	plan, err := newCafeRegistry(t).PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)
	require.Len(t, plan.Files(), 1)

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())

	info, err := os.Lstat(filepath.Join(config, "hours.yaml"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	assert.Equal(t, string(plan.Files()[0].After()), readFile(t, filepath.Join(config, "shared", "hours.yaml")))
}

func TestPlan_Apply_Mode(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps no Unix mode")
	}

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
	path := filepath.Join(config, "hours.yaml")
	require.NoError(t, os.Chmod(path, 0o640))

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}
