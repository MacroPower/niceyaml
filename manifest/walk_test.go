package manifest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// tree describes the files and the links of a directory for a test.
type tree struct {
	files map[string]string
	links map[string]string
	dirs  []string
}

// write creates t below dir.
func (tr tree) write(tb testing.TB, dir string) {
	tb.Helper()

	for _, d := range tr.dirs {
		require.NoError(tb, os.MkdirAll(filepath.Join(dir, d), 0o750))
	}

	for name, text := range tr.files {
		p := filepath.Join(dir, name)
		require.NoError(tb, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(tb, os.WriteFile(p, []byte(text), 0o600))
	}

	for name, target := range tr.links {
		p := filepath.Join(dir, name)
		require.NoError(tb, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(tb, os.Symlink(target, p))
	}
}

// loadedNames returns the names of the sources that set read.
func loadedNames(set *manifest.Set) []string {
	var names []string

	for _, src := range set.Sources() {
		names = append(names, filepath.ToSlash(src.Name()))
	}

	return names
}

func TestPath(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}

	tcs := map[string]struct {
		tree  tree
		opts  []manifest.InputOption
		input string
		want  []string
		err   string
		is    []error
	}{
		"default patterns and order": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":       cafeDoc,
				"config/menu-extra.yaml": "",
				"config/menu.yml":        "",
				"config/notes.txt":       "",
				"config/.hidden.yaml":    "",
				"config/sub/deep.yaml":   "",
			}},
			want: []string{"config/cafe.yaml", "config/menu-extra.yaml", "config/menu.yml"},
		},
		"recursive with an excluded directory": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":              cafeDoc,
				"config/sub/a.yaml":             "",
				"config/testdata/b.yaml":        "",
				"config/sub/c.example.yaml":     "",
				"config/.git/d.yaml":            "",
				"config/sub/.hidden/e.yaml":     "",
				"config/sub/testdata/deep.yaml": "",
			}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithExclude("*.example.yaml", "testdata/"),
			},
			want: []string{"config/cafe.yaml", "config/sub/a.yaml"},
		},
		"include patterns": {
			tree: tree{files: map[string]string{
				"config/cafe.conf": cafeDoc,
				"config/x.yaml":    "",
			}},
			opts: []manifest.InputOption{manifest.WithInclude("*.conf")},
			want: []string{"config/cafe.conf"},
		},
		"named file loads whatever its name": {
			tree:  tree{files: map[string]string{"cafe.txt": cafeDoc}},
			input: "cafe.txt",
			want:  []string{"cafe.txt"},
		},
		"hidden input directory": {
			tree:  tree{files: map[string]string{".cafe/cafe.yaml": cafeDoc}},
			input: ".cafe",
			want:  []string{".cafe/cafe.yaml"},
		},
		"link inside the directory": {
			tree: tree{
				files: map[string]string{"config/..data/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": "..data/cafe.yaml"},
			},
			want: []string{"config/cafe.yaml"},
		},
		"file through a link and its own path": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/same.yaml": "cafe.yaml"},
			},
			want: []string{"config/cafe.yaml"},
		},
		"link to a directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "elsewhere/x.yaml": ""},
				links: map[string]string{"config/sub": "../elsewhere"},
			},
			opts: []manifest.InputOption{manifest.WithRecursive(true)},
			want: []string{"config/cafe.yaml"},
		},
		"link outside the directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": ""},
				links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			},
			err: "" +
				"{dir}/config/hours.yaml: links to ../shared/hours.yaml, which leads outside {dir}/config\n" +
				"`-- copy the file into {dir}/config, or make {dir}/config itself the link",
			is: []error{manifest.ErrOutsideLink},
		},
		"link outside the directory allowed": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			},
			opts: []manifest.InputOption{manifest.WithOutsideLinks(true)},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link to a file the patterns do not select": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/.env": "SECRET=1\n"},
				links: map[string]string{"config/ci.yaml": ".env"},
			},
			err: "config/ci.yaml: links to .env, which does not match *.yaml or *.yml",
			is:  []error{manifest.ErrOutsideLink},
		},
		"dangling link": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/old.yaml": "archive/old.yaml"},
			},
			err: "config/old.yaml: links to archive/old.yaml, which does not exist",
			is:  []error{fs.ErrNotExist},
		},
		"input that does not exist": {
			input: "confg",
			err:   "confg: no such file or directory",
			is:    []error{fs.ErrNotExist},
		},
		"bad pattern": {
			tree: tree{dirs: []string{"config"}},
			opts: []manifest.InputOption{manifest.WithExclude("[")},
			err:  `pattern "["`,
			is:   []error{matcher.ErrInvalidPattern},
		},
		"include pattern with a slash at its end": {
			tree: tree{dirs: []string{"config"}},
			opts: []manifest.InputOption{manifest.WithInclude("sub/")},
			is:   []error{matcher.ErrInvalidPattern},
		},
		"pattern that climbs out": {
			tree: tree{dirs: []string{"config"}},
			opts: []manifest.InputOption{manifest.WithExclude("../x.yaml")},
			is:   []error{matcher.ErrInvalidPattern},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.tree.write(t, dir)

			input := tc.input
			if input == "" {
				input = "config"
			}

			reg := newCafeRegistry(t, manifest.WithKinds())

			set, err := reg.Load(t.Context(), manifest.Path(filepath.Join(dir, input), tc.opts...),
				manifest.WithSetChecks(false))

			if tc.err != "" || tc.is != nil {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				if tc.err != "" {
					want := strings.ReplaceAll(tc.err, "{dir}", filepath.ToSlash(dir))
					assert.Contains(t, filepath.ToSlash(niceyaml.FormatError(err)), want)
				}

				return
			}

			require.NoError(t, err)

			var want []string

			for _, w := range tc.want {
				want = append(want, filepath.ToSlash(filepath.Join(dir, w)))
			}

			assert.Equal(t, want, loadedNames(set))
		})
	}
}

// hoursV1Doc holds an Hours manifest that declares the written version.
const hoursV1Doc = "apiVersion: cafe.jacobcolvin.com/v1\nkind: Hours\nmetadata:\n  name: weekday\n"

func TestPath_Relative(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{files: map[string]string{"config/cafe.yaml": cafeDoc}}.write(t, dir)

	reg := newCafeRegistry(t)

	_, err := reg.Load(t.Context(), manifest.Path(filepath.Join(dir, "config", "missing")))
	require.ErrorIs(t, err, fs.ErrNotExist)

	set, err := reg.Load(t.Context(), manifest.Inputs(
		manifest.Path(filepath.Join(dir, "config")),
		manifest.Path(filepath.Join(dir, "config", "cafe.yaml")),
	))
	require.NoError(t, err)
	assert.Len(t, set.Sources(), 1)
}

func TestFS(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}

	tcs := map[string]struct {
		tree tree
		want []string
		err  string
		is   []error
	}{
		"files and a link inside": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/data/menu.yaml": ""},
				links: map[string]string{"config/menu.yaml": "data/menu.yaml"},
			},
			want: []string{"config/cafe.yaml", "config/menu.yaml"},
		},
		"link outside": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": ""},
				links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			},
			err: "config/hours.yaml: links to ../shared/hours.yaml, which leads outside config",
			is:  []error{manifest.ErrOutsideLink},
		},
		"link through a directory link inside": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/data/menu.yaml": ""},
				links: map[string]string{"config/sub": "data", "config/menu.yaml": "sub/menu.yaml"},
			},
			want: []string{"config/cafe.yaml", "config/menu.yaml"},
		},
		"link through a directory link outside": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": ""},
				links: map[string]string{"config/sub": "../shared", "config/hours.yaml": "sub/hours.yaml"},
			},
			err: "config/hours.yaml: links to sub/hours.yaml, which leads outside config",
			is:  []error{manifest.ErrOutsideLink},
		},
		"dangling link": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/old.yaml": "gone.yaml"},
			},
			err: "config/old.yaml: links to gone.yaml, which does not exist",
			is:  []error{fs.ErrNotExist},
		},
		"missing input": {
			err: "config: no such file or directory",
			is:  []error{fs.ErrNotExist},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.tree.write(t, dir)

			reg := newCafeRegistry(t)

			set, err := reg.Load(t.Context(), manifest.FS(os.DirFS(dir), "config"), manifest.WithSetChecks(false))
			if tc.err != "" {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, loadedNames(set))
		})
	}
}

func TestFS_UnreadableLinks(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input func(dir string) manifest.Input
		links map[string]string
		err   string
		is    []error
	}{
		"absolute link with outside links allowed": {
			input: func(dir string) manifest.Input {
				return manifest.FS(os.DirFS(dir), "config", manifest.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			err:   "config/hours.yaml: links to {dir}/shared/hours.yaml, which leads outside the file system",
			is:    []error{manifest.ErrOutsideLink},
		},
		"dangling link in a file system without ReadLink": {
			input: func(dir string) manifest.Input {
				// The struct hides every method of the file system but Open.
				return manifest.FS(struct{ fs.FS }{os.DirFS(dir)}, "config")
			},
			links: map[string]string{"config/old.yaml": "gone.yaml"},
			err:   "config/old.yaml: ",
			is:    []error{fs.ErrNotExist},
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

			tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
				links: links,
			}.write(t, dir)

			_, err := newCafeRegistry(t).Load(t.Context(), tc.input(dir), manifest.WithSetChecks(false))
			require.Error(t, err)

			for _, target := range tc.is {
				require.ErrorIs(t, err, target)
			}

			assert.Contains(t, niceyaml.FormatError(err), strings.ReplaceAll(tc.err, "{dir}", dir))
		})
	}
}

func TestFS_UncomparableFS(t *testing.T) {
	t.Parallel()

	// The struct type is comparable, but == panics on the map it holds.
	wrapped := struct{ fs.FS }{mapFS(map[string]string{"cafe.yaml": cafeDoc})}
	in := manifest.FS(wrapped, "config/cafe.yaml")

	set, err := newCafeRegistry(t).Load(t.Context(), manifest.Inputs(in, in), manifest.WithSetChecks(false))
	require.NoError(t, err)
	assert.Equal(t, []string{"config/cafe.yaml", "config/cafe.yaml"}, loadedNames(set))
}

func TestWithSourceOptions(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	_, err := reg.Load(t.Context(), manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config",
		manifest.WithSourceOptions(niceyaml.WithByteLimit(10), niceyaml.WithName("ignored"))))
	require.ErrorIs(t, err, niceyaml.ErrLimit)
	assert.True(t, niceyaml.IsInvalid(err))
	assert.Contains(t, err.Error(), "config/cafe.yaml: file is larger than 10 bytes")
}
