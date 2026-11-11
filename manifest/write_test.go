package manifest_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/input"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

// tree describes the files and the links of a directory for a test.
type tree struct {
	files map[string]string
	// The target of each link. A target can name the directory of the test
	// as "{dir}".
	links map[string]string
	dirs  []string
	// The files and the directories that the system refuses to read.
	locked []string
}

// write creates tr below dir. It skips the test when the platform refuses
// to create a link of tr or a directory whose name is not valid UTF-8. It
// also skips the test when the platform reads a locked path anyway.
func (tr tree) write(tb testing.TB, dir string) {
	tb.Helper()

	for _, d := range tr.dirs {
		err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o750)
		if err != nil && !utf8.ValidString(d) {
			tb.Skipf("the file system refuses the name %q: %v", d, err)
		}

		require.NoError(tb, err)
	}

	yamltest.WriteFiles(tb, dir, tr.files)

	for name, target := range tr.links {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(tb, os.MkdirAll(filepath.Dir(p), 0o750))
		yamltest.SymlinkOrSkip(tb, strings.ReplaceAll(target, "{dir}", dir), p)
	}

	for _, name := range tr.locked {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			tb.Skip("the platform reads a file without the permission to read it")
		}

		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(tb, os.Chmod(p, 0))

		// The cleanup of the test cannot remove a directory that it cannot
		// list.
		tb.Cleanup(func() { assert.NoError(tb, os.Chmod(p, 0o700)) }) //nolint:gosec // A test directory.
	}
}

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

			plan, err := newCafeRegistry(t).PlanMigration(t.Context(), input.Path(config))
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

			plan, err := newCafeRegistry(t).PlanMigration(t.Context(), input.Path(tc.config))
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
