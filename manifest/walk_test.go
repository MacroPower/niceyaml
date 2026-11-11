package manifest_test

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// tree describes the files and the links of a directory for a test.
type tree struct {
	files map[string]string
	links map[string]string
	dirs  []string
}

// write creates tr below dir. It skips the test when tr holds a link
// that the platform refuses to create.
func (tr tree) write(tb testing.TB, dir string) {
	tb.Helper()

	for _, d := range tr.dirs {
		require.NoError(tb, os.MkdirAll(filepath.Join(dir, d), 0o750))
	}

	yamltest.WriteFiles(tb, dir, tr.files)

	for name, target := range tr.links {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(tb, os.MkdirAll(filepath.Dir(p), 0o750))
		yamltest.SymlinkOrSkip(tb, target, p)
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

// notExistReason returns the reason that the system gives for a path
// that does not exist, such as "no such file or directory".
func notExistReason(t *testing.T) string {
	t.Helper()

	_, err := os.Stat(filepath.Join(t.TempDir(), "missing"))

	var pe *fs.PathError

	require.ErrorAs(t, err, &pe)

	return pe.Err.Error()
}

func TestPath(t *testing.T) {
	t.Parallel()

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
		"hidden entries": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":          cafeDoc,
				"config/.hidden.yaml":       "",
				"config/.notes.txt":         "",
				"config/.git/d.yaml":        "",
				"config/sub/.deploy/e.yaml": "",
			}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithExclude(".git/"),
			},
			want: []string{"config/.hidden.yaml", "config/cafe.yaml", "config/sub/.deploy/e.yaml"},
		},
		"hidden entries without recursion": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":    cafeDoc,
				"config/.hidden.yaml": "",
				"config/.sub/a.yaml":  "",
			}},
			opts: []manifest.InputOption{manifest.WithHidden(true)},
			want: []string{"config/.hidden.yaml", "config/cafe.yaml"},
		},
		"hidden entries that include patterns name": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":                 cafeDoc,
				"config/.cafe.yaml":                "",
				"config/.github/workflows/ci.yaml": "",
				"config/.github/dependabot.yaml":   "",
				"config/.git/workflows/x.yaml":     "",
			}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithInclude(".github/workflows/*.yaml", ".cafe.yaml"),
				manifest.WithExclude(".git/"),
			},
			want: []string{"config/.cafe.yaml", "config/.github/workflows/ci.yaml"},
		},
		"hidden directory of links excluded": {
			tree: tree{
				files: map[string]string{"config/..2026_11_10/cafe.yaml": cafeDoc},
				links: map[string]string{
					"config/..data":    "..2026_11_10",
					"config/cafe.yaml": "..data/cafe.yaml",
				},
			},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithExclude("..*/"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"exclude pattern for a hidden entry without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":   cafeDoc,
				"config/.git/d.yaml": "",
			}},
			opts: []manifest.InputOption{manifest.WithRecursive(true), manifest.WithExclude(".git/")},
			want: []string{"config/cafe.yaml"},
		},
		"exclude pattern with a hidden brace alternative without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":   cafeDoc,
				"config/.git/d.yaml": "",
				"config/sub/a.yaml":  "",
			}},
			opts: []manifest.InputOption{manifest.WithRecursive(true), manifest.WithExclude("{.git,sub}/")},
			want: []string{"config/cafe.yaml"},
		},
		"patterns with a dot element without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":  cafeDoc,
				"config/b.yaml":     "",
				"config/sub/a.yaml": "",
				"config/sub/c.yaml": "",
			}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithInclude("./cafe.yaml", "./b.yaml", "sub/./*.yaml"),
				manifest.WithExclude("./b.yaml", "sub/./c.yaml"),
			},
			want: []string{"config/cafe.yaml", "config/sub/a.yaml"},
		},
		"include pattern for a hidden file without hidden": {
			tree: tree{files: map[string]string{"config/.cafe.yaml": cafeDoc}},
			opts: []manifest.InputOption{manifest.WithInclude(".cafe.yaml")},
			err: `pattern ".cafe.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".cafe.yaml" matches only those names`,
			is: []error{matcher.ErrInvalidPattern},
		},
		"include pattern for a hidden directory without hidden": {
			tree: tree{files: map[string]string{"config/.github/workflows/ci.yaml": cafeDoc}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithInclude("*.yaml", ".github/workflows/*.yaml"),
			},
			err: `pattern ".github/workflows/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".github" matches only those names`,
			is: []error{matcher.ErrInvalidPattern},
		},
		"include pattern with a hidden brace alternative without hidden": {
			tree: tree{files: map[string]string{"config/config/cafe.yaml": cafeDoc}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithInclude("{.config,config}/*.yaml"),
			},
			err: `pattern "{.config,config}/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".config" matches only those names`,
			is: []error{matcher.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names without hidden": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{manifest.WithInclude("/cafe.yaml", ".store/*.yaml")},
			err: `pattern ".store/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".store" matches only those names`,
			is: []error{matcher.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{
				manifest.WithHidden(true),
				manifest.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"link into a hidden directory that no include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{manifest.WithHidden(true), manifest.WithInclude("/cafe.yaml")},
			err:  "config/cafe.yaml: links to .store/cafe.yaml, which does not match /cafe.yaml",
			is:   []error{manifest.ErrOutsideLink},
		},
		"link into a hidden directory under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/.store/cafe.yaml"},
		},
		"link into a hidden directory excluded under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithInclude("/cafe.yaml", ".store/*.yaml"),
				manifest.WithExclude(".store/"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"links into the hidden directory of a volume that an include pattern names without hidden": {
			tree: tree{
				files: map[string]string{"config/..2026_11_10/cafe.yaml": cafeDoc},
				links: map[string]string{
					"config/..data":    "..2026_11_10",
					"config/cafe.yaml": "..data/cafe.yaml",
				},
			},
			opts: []manifest.InputOption{manifest.WithInclude("/*.yaml", "..*/*.yaml")},
			err: `pattern "..*/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and "..*" matches only those names`,
			is: []error{matcher.ErrInvalidPattern},
		},
		"links into the hidden directory of a volume that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/..2026_11_10/cafe.yaml": cafeDoc},
				links: map[string]string{
					"config/..data":    "..2026_11_10",
					"config/cafe.yaml": "..data/cafe.yaml",
				},
			},
			opts: []manifest.InputOption{
				manifest.WithHidden(true),
				manifest.WithInclude("/*.yaml", "..*/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
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
			err: stringtest.JoinLF(
				"{dir}/config/hours.yaml: links to ../shared/hours.yaml, which leads outside {dir}/config",
				"`-- copy the file into {dir}/config, or make {dir}/config itself the link",
			),
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
			err:   "confg: " + notExistReason(t),
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
var hoursV1Doc = stringtest.LinesLF(
	"apiVersion: cafe.jacobcolvin.com/v1",
	"kind: Hours",
	"metadata:",
	"  name: weekday",
)

func TestFS(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree tree
		opts []manifest.InputOption
		want []string
		err  string
		is   []error
	}{
		"hidden entries skipped": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":        cafeDoc,
				"config/.hidden.yaml":     "",
				"config/.deploy/e.yaml":   "",
				"config/sub/.hidden.yaml": "",
			}},
			opts: []manifest.InputOption{manifest.WithRecursive(true)},
			want: []string{"config/cafe.yaml"},
		},
		"hidden entries": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":        cafeDoc,
				"config/.hidden.yaml":     "",
				"config/.deploy/e.yaml":   "",
				"config/.git/d.yaml":      "",
				"config/sub/.hidden.yaml": "",
			}},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithExclude(".git/"),
			},
			want: []string{
				"config/.deploy/e.yaml",
				"config/.hidden.yaml",
				"config/cafe.yaml",
				"config/sub/.hidden.yaml",
			},
		},
		"include pattern for a hidden file without hidden": {
			tree: tree{files: map[string]string{"config/.cafe.yaml": cafeDoc}},
			opts: []manifest.InputOption{manifest.WithInclude(".cafe.yaml")},
			err:  `and ".cafe.yaml" matches only those names`,
			is:   []error{matcher.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names without hidden": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{manifest.WithInclude("/cafe.yaml", ".store/*.yaml")},
			err:  `and ".store" matches only those names`,
			is:   []error{matcher.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{
				manifest.WithHidden(true),
				manifest.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"link into a hidden directory excluded under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []manifest.InputOption{
				manifest.WithRecursive(true),
				manifest.WithHidden(true),
				manifest.WithInclude("/cafe.yaml", ".store/*.yaml"),
				manifest.WithExclude(".store/"),
			},
			want: []string{"config/cafe.yaml"},
		},
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
			err: "config: " + notExistReason(t),
			is:  []error{fs.ErrNotExist},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.tree.write(t, dir)

			reg := newCafeRegistry(t)

			set, err := reg.Load(t.Context(), manifest.FS(os.DirFS(dir), "config", tc.opts...),
				manifest.WithSetChecks(false))
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
		input func(t *testing.T, dir string) manifest.Input
		links map[string]string
		err   string
		is    []error
	}{
		"absolute link with outside links allowed": {
			input: func(_ *testing.T, dir string) manifest.Input {
				return manifest.FS(os.DirFS(dir), "config", manifest.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			err:   "config/hours.yaml: links to {dir}/shared/hours.yaml, which leads outside the file system",
			is:    []error{manifest.ErrOutsideLink},
		},
		"link outside in fs.Sub of a file system that reports links": {
			input: func(t *testing.T, dir string) manifest.Input {
				t.Helper()

				return manifest.FS(subFS(t, os.DirFS(filepath.Dir(dir)), filepath.Base(dir)), "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: links to ../shared/hours.yaml, which leads outside config",
			is:    []error{manifest.ErrOutsideLink},
		},
		"link out of fs.Sub of a file system that reports links": {
			input: func(t *testing.T, dir string) manifest.Input {
				t.Helper()

				return manifest.FS(subFS(t, os.DirFS(dir), "config"), ".", manifest.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "hours.yaml: links to ../shared/hours.yaml, which leads outside the file system",
			is:    []error{manifest.ErrOutsideLink},
		},
		"link that ReadLink cannot read": {
			input: func(_ *testing.T, dir string) manifest.Input {
				return manifest.FS(deniedFS{FS: os.DirFS(dir), readLink: true}, "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"absolute link that ReadLink cannot read with outside links allowed": {
			input: func(_ *testing.T, dir string) manifest.Input {
				return manifest.FS(deniedFS{FS: os.DirFS(dir), readLink: true}, "config",
					manifest.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"link that Lstat cannot describe": {
			input: func(_ *testing.T, dir string) manifest.Input {
				return manifest.FS(deniedFS{FS: os.DirFS(dir), lstat: true}, "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"link that Lstat cannot describe with outside links allowed": {
			input: func(_ *testing.T, dir string) manifest.Input {
				return manifest.FS(deniedFS{FS: os.DirFS(dir), lstat: true}, "config",
					manifest.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
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

			set, err := newCafeRegistry(t).Load(t.Context(), tc.input(t, dir), manifest.WithSetChecks(false))
			require.Error(t, err)
			assert.Nil(t, set)

			for _, target := range tc.is {
				require.ErrorIs(t, err, target)
			}

			assert.Contains(t, niceyaml.FormatError(err), strings.ReplaceAll(tc.err, "{dir}", dir))
		})
	}
}

// subFS returns the directory at dir in fsys as a file system.
func subFS(t *testing.T, fsys fs.FS, dir string) fs.FS {
	t.Helper()

	sub, err := fs.Sub(fsys, dir)
	require.NoError(t, err)

	return sub
}

// followFS is a file system that implements [fs.ReadLinkFS] with an Lstat
// that follows links. Its Lstat thus describes the file that a link leads
// to, and never the link.
type followFS struct{ fs.FS }

func (f followFS) ReadLink(name string) (string, error) {
	return fs.ReadLink(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

func (f followFS) Lstat(name string) (fs.FileInfo, error) {
	return fs.Stat(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

// deniedFS is a file system that implements [fs.ReadLinkFS] and reports
// its links. The ReadLink and the Lstat that it names fail with an error
// that matches [fs.ErrPermission].
type deniedFS struct {
	fs.FS

	readLink bool
	lstat    bool
}

func (f deniedFS) ReadLink(name string) (string, error) {
	if f.readLink {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrPermission}
	}

	return fs.ReadLink(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

func (f deniedFS) Lstat(name string) (fs.FileInfo, error) {
	if f.lstat {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrPermission}
	}

	return fs.Lstat(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

// flatFS is a file system without [fs.ReadLinkFS] whose directory
// listings describe each link as the file or the directory that the link
// leads to.
type flatFS struct{ fs.FS }

func (f flatFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, err //nolint:wrapcheck // The walk reads the error of the file system.
	}

	for i, e := range entries {
		info, err := fs.Stat(f.FS, path.Join(name, e.Name()))
		if err != nil {
			return nil, err //nolint:wrapcheck // The walk reads the error of the file system.
		}

		entries[i] = fs.FileInfoToDirEntry(info)
	}

	return entries, nil
}

// flatReportFS is a file system that reports its links through
// [fs.ReadLinkFS] and lists its directories as a [flatFS] does.
type flatReportFS struct{ flatFS }

func (f flatReportFS) ReadLink(name string) (string, error) {
	return fs.ReadLink(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

func (f flatReportFS) Lstat(name string) (fs.FileInfo, error) {
	return fs.Lstat(f.FS, name) //nolint:wrapcheck // The walk reads the error of the file system.
}

func TestFS_LinksListedAsTargets(t *testing.T) {
	t.Parallel()

	// Each file system holds the tree of dir, and its listings describe
	// each link as the file or the directory that the link leads to.
	fsyss := map[string]func(dir string) fs.FS{
		"without ReadLinkFS": func(dir string) fs.FS {
			return flatFS{os.DirFS(dir)}
		},
		"that reports links": func(dir string) fs.FS {
			return flatReportFS{flatFS{os.DirFS(dir)}}
		},
	}

	tcs := map[string]struct {
		// The files that the tree holds besides config/cafe.yaml and
		// shared/hours.yaml.
		files map[string]string
		links map[string]string
		opts  []manifest.InputOption
		want  []string
	}{
		// A file loads once for each entry that reaches it, and once at
		// its own path.
		"link to a file inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			want:  []string{"config/cafe.yaml", "config/same.yaml"},
		},
		"link to a file inside with outside links allowed": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(true)},
			want:  []string{"config/cafe.yaml", "config/same.yaml"},
		},
		"link to a link to a file inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml", "config/chain.yaml": "same.yaml"},
			want:  []string{"config/cafe.yaml", "config/chain.yaml", "config/same.yaml"},
		},
		"link to a directory inside in a recursive walk": {
			files: map[string]string{"config/real/hours.yaml": hoursV1Doc},
			links: map[string]string{"config/sub": "real"},
			opts:  []manifest.InputOption{manifest.WithRecursive(true)},
			want:  []string{"config/cafe.yaml", "config/real/hours.yaml", "config/sub/hours.yaml"},
		},
		"link to a file outside": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link to a file outside with outside links refused": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(false)},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link to a directory outside": {
			links: map[string]string{"config/sub": "../shared"},
			want:  []string{"config/cafe.yaml"},
		},
		"link to a directory outside in a recursive walk": {
			links: map[string]string{"config/sub": "../shared"},
			opts:  []manifest.InputOption{manifest.WithRecursive(true)},
			want:  []string{"config/cafe.yaml", "config/sub/hours.yaml"},
		},
		"link to a directory outside in a recursive walk with outside links refused": {
			links: map[string]string{"config/sub": "../shared"},
			opts:  []manifest.InputOption{manifest.WithRecursive(true), manifest.WithOutsideLinks(false)},
			want:  []string{"config/cafe.yaml", "config/sub/hours.yaml"},
		},
	}

	for fsName, newFS := range fsyss {
		for name, tc := range tcs {
			t.Run(fsName+"/"+name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()

				files := map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc}
				maps.Copy(files, tc.files)

				tree{files: files, links: tc.links}.write(t, dir)

				// The walk does not ask the file system about an entry that
				// the listing does not name as a link.
				set, err := newCafeRegistry(t).Load(t.Context(),
					manifest.FS(newFS(dir), "config", tc.opts...), manifest.WithSetChecks(false))
				require.NoError(t, err)
				assert.Equal(t, tc.want, loadedNames(set))
			})
		}
	}
}

func TestFS_UnreportedLinks(t *testing.T) {
	t.Parallel()

	// Each file system holds the tree of dir and reports none of its links.
	fsyss := map[string]func(t *testing.T, dir string) fs.FS{
		"without ReadLinkFS": func(_ *testing.T, dir string) fs.FS {
			// The struct hides every method of the file system but Open.
			return struct{ fs.FS }{os.DirFS(dir)}
		},
		"fs.Sub of one without ReadLinkFS": func(t *testing.T, dir string) fs.FS {
			t.Helper()

			// The file system that fs.Sub returns implements fs.ReadLinkFS.
			// Its ReadLink fails and its Lstat follows the link.
			return subFS(t, struct{ fs.FS }{os.DirFS(filepath.Dir(dir))}, filepath.Base(dir))
		},
		"Lstat that follows links": func(_ *testing.T, dir string) fs.FS {
			return followFS{os.DirFS(dir)}
		},
	}

	unreported := func(name string) string {
		return stringtest.JoinLF(
			name+": is a link, and the file system does not report where it leads",
			"`-- pass WithOutsideLinks(true) to read it, or implement fs.ReadLinkFS in the file system "+
				"and in each file system that it wraps",
		)
	}

	tcs := map[string]struct {
		links map[string]string
		opts  []manifest.InputOption
		want  []string
		err   string
		is    []error
	}{
		"link outside": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   unreported("config/hours.yaml"),
			is:    []error{manifest.ErrOutsideLink},
		},
		"link outside with outside links refused": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(false)},
			err:   unreported("config/hours.yaml"),
			is:    []error{manifest.ErrOutsideLink},
		},
		"link outside with outside links allowed": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(true)},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			err:   unreported("config/same.yaml"),
			is:    []error{manifest.ErrOutsideLink},
		},
		"link inside with outside links allowed": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(true)},
			want:  []string{"config/cafe.yaml", "config/same.yaml"},
		},
		"link that the patterns do not select": {
			links: map[string]string{"config/hours.txt": "../shared/hours.yaml"},
			want:  []string{"config/cafe.yaml"},
		},
		"link to a directory": {
			links: map[string]string{"config/shared.yaml": "../shared"},
			want:  []string{"config/cafe.yaml"},
		},
		"dangling link": {
			links: map[string]string{"config/old.yaml": "gone.yaml"},
			err:   "config/old.yaml: ",
			is:    []error{fs.ErrNotExist},
		},
		"dangling link with outside links allowed": {
			links: map[string]string{"config/old.yaml": "gone.yaml"},
			opts:  []manifest.InputOption{manifest.WithOutsideLinks(true)},
			err:   "config/old.yaml: ",
			is:    []error{fs.ErrNotExist},
		},
	}

	for fsName, newFS := range fsyss {
		for name, tc := range tcs {
			t.Run(fsName+"/"+name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()

				tree{
					files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
					links: tc.links,
				}.write(t, dir)

				set, err := newCafeRegistry(t).Load(t.Context(),
					manifest.FS(newFS(t, dir), "config", tc.opts...), manifest.WithSetChecks(false))
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

// failingReader is an [io.Reader] whose first read fails with err, and
// whose later reads find the end of the text.
type failingReader struct {
	err  error
	done bool
}

// Read returns the error of r on the first call, and [io.EOF] after.
func (r *failingReader) Read([]byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}

	r.done = true

	return 0, r.err
}

func TestReader(t *testing.T) {
	t.Parallel()

	errBroken := errors.New("broken pipe")

	tcs := map[string]struct {
		in   manifest.Input
		want []string
		err  string
		is   []error
	}{
		"text": {
			in:   manifest.Reader("cafe.yaml", strings.NewReader(cafeDoc)),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"name option": {
			in: manifest.Reader("cafe.yaml", strings.NewReader(cafeDoc),
				manifest.WithSourceOptions(niceyaml.WithName("ignored"))),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"one reader twice": {
			in: func() manifest.Input {
				in := manifest.Reader("cafe.yaml", strings.NewReader(cafeDoc))

				return manifest.Inputs(in, in)
			}(),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"read that fails": {
			in:  manifest.Reader("cafe.yaml", &failingReader{err: errBroken}),
			err: "cafe.yaml: read: broken pipe",
			is:  []error{errBroken},
		},
		"byte limit": {
			in: manifest.Reader("cafe.yaml", strings.NewReader(cafeDoc),
				manifest.WithSourceOptions(niceyaml.WithByteLimit(10), niceyaml.WithName("ignored"))),
			err: "cafe.yaml: file is larger than 10 bytes",
			is:  []error{niceyaml.ErrLimit},
		},
		"bad pattern": {
			in:  manifest.Reader("cafe.yaml", strings.NewReader(cafeDoc), manifest.WithExclude("[")),
			err: `pattern "["`,
			is:  []error{matcher.ErrInvalidPattern},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t)

			// Every load of the Input gives the result of the first read.
			for range 2 {
				set, err := reg.Load(t.Context(), tc.in, manifest.WithSetChecks(false))
				if tc.err != "" {
					require.Error(t, err)

					for _, target := range tc.is {
						require.ErrorIs(t, err, target)
					}

					assert.Contains(t, niceyaml.FormatError(err), tc.err)

					continue
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, listObjects(set))
				assert.Equal(t, []string{"cafe.yaml"}, loadedNames(set))
			}
		})
	}
}

func TestReader_PlanThenLoad(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)
	in := manifest.Reader("menu.yaml", strings.NewReader(menuDoc))

	plan, err := reg.PlanMigration(t.Context(), in)
	require.NoError(t, err)
	require.Len(t, plan.Files(), 1)
	assert.Equal(t, menuV1Doc, string(plan.Files()[0].After()))

	set, err := reg.Load(t.Context(), in, manifest.WithSetChecks(false))
	require.NoError(t, err)
	assert.Equal(t, []string{
		`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
		`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
	}, listObjects(set))
}

// panicReader is an [io.Reader] whose reads panic.
type panicReader struct{}

// Read panics.
func (panicReader) Read([]byte) (int, error) {
	panic("reader broke")
}

func TestReader_Panic(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)
	in := manifest.Reader("cafe.yaml", panicReader{})

	// Every load of the Input repeats the panic of the first read.
	for range 2 {
		assert.PanicsWithValue(t, "reader broke", func() {
			_, _ = reg.Load(t.Context(), in, manifest.WithSetChecks(false)) //nolint:errcheck // The load panics.
		})
	}
}

func TestWithSourceOptions(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	_, err := reg.Load(t.Context(), configFS(map[string]string{"cafe.yaml": cafeDoc},
		manifest.WithSourceOptions(niceyaml.WithByteLimit(10), niceyaml.WithName("ignored"))))
	require.ErrorIs(t, err, niceyaml.ErrLimit)
	assert.True(t, niceyaml.IsInvalid(err))
	assert.Contains(t, err.Error(), "config/cafe.yaml: file is larger than 10 bytes")
}
