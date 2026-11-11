package manifest_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
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
		"relative link through a directory link outside the directory and back up": {
			files: map[string]string{"config/shared/hours.yaml": hoursDoc, "config/sub/notes.txt": ""},
			links: map[string]string{
				"x":                 "config/sub",
				"config/hours.yaml": "../x/../shared/hours.yaml",
			},
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

// TestPlan_Apply_LinkThroughFile re-points a link after the plan, so that
// a regular file precedes a ".." in its target. The system refuses to
// follow such a link, and so does Apply, even though the target names the
// file of the plan by its text.
func TestPlan_Apply_LinkThroughFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	tree{
		files: map[string]string{"config/shared/hours.yaml": hoursDoc, "config/notes.txt": ""},
		links: map[string]string{"config/hours.yaml": "shared/hours.yaml"},
	}.write(t, dir)

	config := filepath.Join(dir, "config")
	link := filepath.Join(config, "hours.yaml")

	plan, err := newCafeRegistry(t).PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)
	require.Len(t, plan.Files(), 1)

	require.NoError(t, os.Remove(link))
	yamltest.SymlinkOrSkip(t, "notes.txt/../shared/hours.yaml", link)

	_, err = os.ReadFile(link) //nolint:gosec // A test file.

	var pe *fs.PathError

	require.ErrorAs(t, err, &pe)

	res, err := plan.Apply(t.Context())
	require.Error(t, err)
	assert.Empty(t, res.Files())
	assert.Equal(t, link+": cannot replace the file: "+pe.Err.Error(), niceyaml.FormatError(err))
	assert.Equal(t, hoursDoc, yamltest.ReadFile(t, filepath.Join(config, "shared", "hours.yaml")))
}

// TestPlan_Apply_RelativeLink applies plans of a relative input path. It
// changes the working directory, so it cannot run in parallel.
//
//nolint:paralleltest // t.Chdir does not work in a parallel test.
func TestPlan_Apply_RelativeLink(t *testing.T) {
	tcs := map[string]struct {
		links  map[string]string
		chdir  string
		config string
	}{
		"link that climbs above the working directory": {
			links:  map[string]string{"config/hours.yaml": "../config/shared/hours.yaml"},
			chdir:  "config",
			config: ".",
		},
		"link that climbs above the parent of the directory": {
			links:  map[string]string{"config/hours.yaml": "../../{base}/config/shared/hours.yaml"},
			config: "config",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			links := make(map[string]string, len(tc.links))
			for link, target := range tc.links {
				links[link] = strings.ReplaceAll(target, "{base}", filepath.Base(dir))
			}

			tree{
				files: map[string]string{"config/shared/hours.yaml": hoursDoc},
				links: links,
			}.write(t, dir)

			t.Chdir(filepath.Join(dir, filepath.FromSlash(tc.chdir)))

			plan, err := newCafeRegistry(t).PlanMigration(t.Context(), manifest.Path(tc.config))
			require.NoError(t, err)
			require.Len(t, plan.Files(), 1)

			res, err := plan.Apply(t.Context())
			require.NoError(t, err)
			require.NoError(t, res.Err())

			assert.Equal(
				t,
				string(plan.Files()[0].After()),
				yamltest.ReadFile(t, filepath.Join(dir, "config", "shared", "hours.yaml")),
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

func TestWriteAtomic(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})
	path := filepath.Join(config, "hours.yaml")
	f := plan.Files()[0]

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, manifest.WriteAtomic(ctx, f), context.Canceled)
	assert.Equal(t, hoursDoc, yamltest.ReadFile(t, path))

	require.NoError(t, manifest.WriteAtomic(t.Context(), f))
	assert.Equal(t, string(f.After()), yamltest.ReadFile(t, path))

	// The file now holds the new text, so a second write finds it changed.
	require.ErrorIs(t, manifest.WriteAtomic(t.Context(), f), manifest.ErrChanged)
}
