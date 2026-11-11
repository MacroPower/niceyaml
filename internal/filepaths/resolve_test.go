package filepaths_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// linkedDir returns a temporary directory that holds the directory
// sub/deep/inner and three symlinks. The symlink ldir leads to sub/deep,
// adir leads there by its absolute path, and dangling leads nowhere. The
// function also returns the directory with its links resolved. It skips
// the test where the OS makes no symlink, and on Windows, which drops a
// ".." as text.
func linkedDir(t *testing.T) (string, string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub", "deep", "inner"), 0o750))

	yamltest.SymlinkOrSkip(t, filepath.Join("sub", "deep"), filepath.Join(dir, "ldir"))
	require.NoError(t, os.Symlink(filepath.Join(dir, "sub", "deep"), filepath.Join(dir, "adir")))
	require.NoError(t, os.Symlink("nowhere", filepath.Join(dir, "dangling")))

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	return dir, physical
}

// relToWorkDir returns path relative to the working directory.
func relToWorkDir(t *testing.T, path string) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, path)
	require.NoError(t, err)

	return rel
}

func TestAbs(t *testing.T) {
	t.Parallel()

	dir, physical := linkedDir(t)
	rel := relToWorkDir(t, dir)

	wd, err := os.Getwd()
	require.NoError(t, err)

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path string
		want string
	}{
		"no dot-dot keeps a symlinked name": {
			path: dir + "/ldir/x.yaml",
			want: filepath.Join(dir, "ldir", "x.yaml"),
		},
		"dot-dot after a symlinked directory": {
			path: dir + "/ldir/../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"dot-dot after an absolute symlink": {
			path: dir + "/adir/../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"dot-dot after a plain directory": {
			path: dir + "/sub/../x.yaml",
			want: filepath.Join(dir, "x.yaml"),
		},
		"dot-dot after a directory under a symlink": {
			path: dir + "/ldir/inner/../../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"symlinked name after the last dot-dot": {
			path: dir + "/sub/../ldir/x.yaml",
			want: filepath.Join(dir, "ldir", "x.yaml"),
		},
		"relative dot-dot after a symlinked directory": {
			path: rel + "/ldir/../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"relative path without dot-dot": {
			path: "x.yaml",
			want: filepath.Join(wd, "x.yaml"),
		},
		"dot-dot after a missing directory": {
			path: dir + "/missing/../x.yaml",
			want: filepath.Join(dir, "x.yaml"),
		},
		"dot-dot after a dangling symlink": {
			path: dir + "/dangling/../x.yaml",
			want: filepath.Join(dir, "x.yaml"),
		},
		"dot-dot at the root": {
			path: "/../x.yaml",
			want: "/x.yaml",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := filepaths.Abs(tc.path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClean(t *testing.T) {
	t.Parallel()

	dir, physical := linkedDir(t)
	rel := relToWorkDir(t, dir)

	tcs := map[string]struct {
		path string
		want string
	}{
		"no dot-dot": {
			path: dir + "/./ldir//x.yaml",
			want: filepath.Join(dir, "ldir", "x.yaml"),
		},
		"dot-dot after a symlinked directory": {
			path: dir + "/ldir/../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"dot-dot after a plain directory": {
			path: dir + "/sub/deep/../x.yaml",
			want: filepath.Join(dir, "sub", "x.yaml"),
		},
		"relative dot-dot after a symlinked directory": {
			path: rel + "/ldir/../x.yaml",
			want: filepath.Join(rel, "sub", "x.yaml"),
		},
		"relative dot-dot after an absolute symlink": {
			path: rel + "/adir/../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"dot-dot after a dangling symlink": {
			path: dir + "/dangling/../x.yaml",
			want: filepath.Join(dir, "x.yaml"),
		},
		"dot-dot after a missing entry beyond a symlink": {
			path: dir + "/ldir/../missing/../x.yaml",
			want: filepath.Join(dir, "ldir", "..", "missing", "..", "x.yaml"),
		},
		"symlinked directories before a dot-dot after a symlink": {
			path: dir + "/ldir/inner/../../x.yaml",
			want: filepath.Join(physical, "sub", "x.yaml"),
		},
		"leading dot-dot stays": {
			path: "../../x.yaml",
			want: filepath.Join("..", "..", "x.yaml"),
		},
		"dot-dot back to the working directory": {
			path: "a/..",
			want: ".",
		},
		"dot": {
			path: ".",
			want: ".",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, filepaths.Clean(tc.path))
		})
	}
}

// TestAbs_SymlinkedWorkDir builds absolute paths in a working directory
// that the shell names by cwdlink, a link to real/sub. A ".." that
// climbs out of the working directory climbs from real/sub, as the OS
// climbs. A ".." that follows a plain directory keeps the working
// directory as the shell names it, so a relative pattern still matches.
//
//nolint:paralleltest // t.Chdir does not work in a parallel test.
func TestAbs_SymlinkedWorkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "real", "sub", "a"), 0o750))

	cwd := filepath.Join(dir, "cwdlink")
	yamltest.SymlinkOrSkip(t, filepath.Join("real", "sub"), cwd)

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	// Chdir sets PWD, so os.Getwd returns the name of the link.
	t.Chdir(cwd)

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path string
		want string
	}{
		"no dot-dot": {
			path: "x.yaml",
			want: filepath.Join(cwd, "x.yaml"),
		},
		"dot-dot after a plain directory": {
			path: "a/../x.yaml",
			want: filepath.Join(cwd, "x.yaml"),
		},
		"dot-dot out of the working directory": {
			path: "../x.yaml",
			want: filepath.Join(physical, "real", "x.yaml"),
		},
		"dot-dot after a plain directory and out of the working directory": {
			path: "a/../../x.yaml",
			want: filepath.Join(physical, "real", "x.yaml"),
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // t.Chdir does not work in a parallel test.
		t.Run(name, func(t *testing.T) {
			got, err := filepaths.Abs(tc.path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestClean_RelativeLink cleans paths relative to a working directory
// that holds links with relative targets. [filepath.EvalSymlinks] then
// returns "." or a path of ".." elements, and the step up from it must
// climb as the OS climbs. The test changes the working directory, so it
// does not run in parallel.
//
//nolint:paralleltest // t.Chdir does not work in a parallel test.
func TestClean_RelativeLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	cwd := filepath.Join(t.TempDir(), "g", "p", "c")
	require.NoError(t, os.MkdirAll(cwd, 0o750))
	yamltest.SymlinkOrSkip(t, "..", filepath.Join(cwd, "up"))
	require.NoError(t, os.Symlink(".", filepath.Join(cwd, "self")))

	t.Chdir(cwd)

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path string
		want string
	}{
		"dot-dot after a link to the parent": {
			path: "up/../x.yaml",
			want: filepath.Join("..", "..", "x.yaml"),
		},
		"dot-dot after a link to the working directory": {
			path: "self/../x.yaml",
			want: filepath.Join("..", "x.yaml"),
		},
		"directory that ends in a dot-dot after a link": {
			path: "up/..",
			want: filepath.Join("..", ".."),
		},
		"dot-dot after a link to the parent behind another link": {
			path: "self/up/../x.yaml",
			want: filepath.Join("..", "..", "x.yaml"),
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, filepaths.Clean(tc.path))
		})
	}
}
