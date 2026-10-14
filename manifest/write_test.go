package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestPlan_Apply_Link(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		files  map[string]string
		links  map[string]string
		config string
		target string
	}{
		"relative link": {
			files:  map[string]string{"config/shared/hours.yaml": hoursDoc},
			links:  map[string]string{"config/hours.yaml": "shared/hours.yaml"},
			config: "config",
			target: "config/shared/hours.yaml",
		},
		"relative link through the parent of the directory": {
			files:  map[string]string{"config/shared/hours.yaml": hoursDoc},
			links:  map[string]string{"config/hours.yaml": "../config/shared/hours.yaml"},
			config: "config",
			target: "config/shared/hours.yaml",
		},
		"absolute link through a linked directory": {
			files: map[string]string{"real/config/shared/hours.yaml": hoursDoc},
			links: map[string]string{
				"alias":                  "real",
				"real/config/hours.yaml": "{dir}/alias/config/shared/hours.yaml",
			},
			config: "real/config",
			target: "real/config/shared/hours.yaml",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			links := make(map[string]string, len(tc.links))
			for link, target := range tc.links {
				links[link] = strings.ReplaceAll(target, "{dir}", dir)
			}

			tree{files: tc.files, links: links}.write(t, dir)

			config := filepath.Join(dir, filepath.FromSlash(tc.config))

			plan, err := newCafeRegistry(t).PlanMigration(t.Context(), manifest.Path(config))
			require.NoError(t, err)
			require.Len(t, plan.Files(), 1)

			res, err := plan.Apply(t.Context())
			require.NoError(t, err)
			require.NoError(t, res.Err())

			info, err := os.Lstat(filepath.Join(config, "hours.yaml"))
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&os.ModeSymlink)
			assert.Equal(
				t,
				string(plan.Files()[0].After()),
				yamltest.ReadFile(t, filepath.Join(dir, filepath.FromSlash(tc.target))),
			)
		})
	}
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
