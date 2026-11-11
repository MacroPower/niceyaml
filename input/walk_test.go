package input_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/input"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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

var (
	// The text of a file of a test.
	cafeDoc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Cafe",
		"metadata:",
		"  name: yamull",
	)

	// The text of another file of a test.
	hoursV1Doc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Hours",
		"metadata:",
		"  name: weekday",
	)
)

// mapFS returns a file system that holds files under the directory config.
func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys["config/"+name] = &fstest.MapFile{Data: []byte(text)}
	}

	return fsys
}

// readAll returns the files of in. The error is the error of
// [input.Input.Files], or the errors of the files joined when Files
// passes.
func readAll(t *testing.T, in input.Input) ([]input.File, error) {
	t.Helper()

	files, err := in.Files(t.Context())
	if err != nil {
		require.Nil(t, files)

		return nil, err //nolint:wrapcheck // The test reads the error of the read.
	}

	var errs []error

	for _, f := range files {
		if f.Err != nil {
			assert.Nil(t, f.Source, f.Name)

			errs = append(errs, f.Err)

			continue
		}

		require.NotNil(t, f.Source, f.Name)
		assert.Equal(t, f.Source.Name(), f.Name)
	}

	return files, errors.Join(errs...)
}

// formatAll returns each error that err joins, formatted as
// [niceyaml.FormatError] does, one after another.
func formatAll(err error) string {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return niceyaml.FormatError(err)
	}

	var parts []string

	for _, e := range joined.Unwrap() {
		parts = append(parts, niceyaml.FormatError(e))
	}

	return strings.Join(parts, "\n")
}

// readNames returns the slash names of the files that the read took in.
func readNames(files []input.File) []string {
	var names []string

	for _, f := range files {
		if f.Source != nil {
			names = append(names, filepath.ToSlash(f.Name))
		}
	}

	return names
}

// readTexts returns the text of each file that the read took in, by its
// slash name.
func readTexts(files []input.File) map[string]string {
	texts := map[string]string{}

	for _, f := range files {
		if f.Source != nil {
			texts[filepath.ToSlash(f.Name)] = f.Source.Text()
		}
	}

	return texts
}

// notExistReason returns the reason that the system gives for a path
// that does not exist, such as "no such file or directory".
func notExistReason(t *testing.T) string {
	t.Helper()

	return openReason(t, filepath.Join(t.TempDir(), "missing"))
}

// openReason returns the reason that the system gives when it refuses to
// open the file at name, such as "permission denied".
func openReason(t *testing.T, name string) string {
	t.Helper()

	f, err := os.Open(name) //nolint:gosec // A test file.
	if err == nil {
		require.NoError(t, f.Close())
	}

	var pe *fs.PathError

	require.ErrorAs(t, err, &pe)

	return pe.Err.Error()
}

func TestPath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree  tree
		opts  []input.Option
		input string
		want  []string
		// The text of each file that the read takes in, by its path below the
		// directory of the test, for each file that the test checks.
		text map[string]string
		err  string
		// The path whose open fails with the reason that err names as
		// "{reason}".
		reason string
		is     []error
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
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithExclude("*.example.yaml", "testdata/"),
			},
			want: []string{"config/cafe.yaml", "config/sub/a.yaml"},
		},
		"include patterns": {
			tree: tree{files: map[string]string{
				"config/cafe.conf": cafeDoc,
				"config/x.yaml":    "",
			}},
			opts: []input.Option{input.WithInclude("*.conf")},
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
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithExclude(".git/"),
			},
			want: []string{"config/.hidden.yaml", "config/cafe.yaml", "config/sub/.deploy/e.yaml"},
		},
		"hidden entries without recursion": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":    cafeDoc,
				"config/.hidden.yaml": "",
				"config/.sub/a.yaml":  "",
			}},
			opts: []input.Option{input.WithHidden(true)},
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
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithInclude(".github/workflows/*.yaml", ".cafe.yaml"),
				input.WithExclude(".git/"),
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
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithExclude("..*/"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"exclude pattern for a hidden entry without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":   cafeDoc,
				"config/.git/d.yaml": "",
			}},
			opts: []input.Option{input.WithRecursive(true), input.WithExclude(".git/")},
			want: []string{"config/cafe.yaml"},
		},
		"exclude pattern with a hidden brace alternative without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":   cafeDoc,
				"config/.git/d.yaml": "",
				"config/sub/a.yaml":  "",
			}},
			opts: []input.Option{input.WithRecursive(true), input.WithExclude("{.git,sub}/")},
			want: []string{"config/cafe.yaml"},
		},
		"patterns with a dot element without hidden": {
			tree: tree{files: map[string]string{
				"config/cafe.yaml":  cafeDoc,
				"config/b.yaml":     "",
				"config/sub/a.yaml": "",
				"config/sub/c.yaml": "",
			}},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithInclude("./cafe.yaml", "./b.yaml", "sub/./*.yaml"),
				input.WithExclude("./b.yaml", "sub/./c.yaml"),
			},
			want: []string{"config/cafe.yaml", "config/sub/a.yaml"},
		},
		"include pattern for a hidden file without hidden": {
			tree: tree{files: map[string]string{"config/.cafe.yaml": cafeDoc}},
			opts: []input.Option{input.WithInclude(".cafe.yaml")},
			err: `pattern ".cafe.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".cafe.yaml" matches only those names`,
			is: []error{input.ErrInvalidPattern},
		},
		"include pattern for a hidden directory without hidden": {
			tree: tree{files: map[string]string{"config/.github/workflows/ci.yaml": cafeDoc}},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithInclude("*.yaml", ".github/workflows/*.yaml"),
			},
			err: `pattern ".github/workflows/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".github" matches only those names`,
			is: []error{input.ErrInvalidPattern},
		},
		"include pattern with a hidden brace alternative without hidden": {
			tree: tree{files: map[string]string{"config/config/cafe.yaml": cafeDoc}},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithInclude("{.config,config}/*.yaml"),
			},
			err: `pattern "{.config,config}/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".config" matches only those names`,
			is: []error{input.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names without hidden": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{input.WithInclude("/cafe.yaml", ".store/*.yaml")},
			err: `pattern ".store/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and ".store" matches only those names`,
			is: []error{input.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{
				input.WithHidden(true),
				input.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"link into a hidden directory that no include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{input.WithHidden(true), input.WithInclude("/cafe.yaml")},
			err:  "config/cafe.yaml: links to .store/cafe.yaml, which does not match /cafe.yaml",
			is:   []error{input.ErrOutsideLink},
		},
		"link into a hidden directory under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/.store/cafe.yaml"},
		},
		"link into a hidden directory excluded under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithInclude("/cafe.yaml", ".store/*.yaml"),
				input.WithExclude(".store/"),
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
			opts: []input.Option{input.WithInclude("/*.yaml", "..*/*.yaml")},
			err: `pattern "..*/*.yaml": invalid glob pattern: ` +
				`a walk skips entries whose names start with ".", and "..*" matches only those names`,
			is: []error{input.ErrInvalidPattern},
		},
		"links into the hidden directory of a volume that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/..2026_11_10/cafe.yaml": cafeDoc},
				links: map[string]string{
					"config/..data":    "..2026_11_10",
					"config/cafe.yaml": "..data/cafe.yaml",
				},
			},
			opts: []input.Option{
				input.WithHidden(true),
				input.WithInclude("/*.yaml", "..*/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"named file reads whatever its name": {
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
			opts: []input.Option{input.WithRecursive(true)},
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
			is: []error{input.ErrOutsideLink},
		},
		"link outside the directory allowed": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			},
			opts: []input.Option{input.WithOutsideLinks(true)},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link through a file link outside the directory that leads back": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{
					"config/hours.yaml": "../shared/hours.yaml",
					"shared/hours.yaml": "../config/..data/weekday.yaml",
				},
			},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link through a directory link outside the directory that leads back": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{
					"config/hours.yaml": "../shared/data/weekday.yaml",
					"shared/data":       "../config/..data",
				},
			},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link through a link outside the directory to a file the patterns do not select": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/.env": "SECRET=1\n"},
				links: map[string]string{
					"config/ci.yaml": "../shared/ci.yaml",
					"shared/ci.yaml": "../config/.env",
				},
			},
			err: "config/ci.yaml: links to ../shared/ci.yaml, which does not match *.yaml or *.yml",
			is:  []error{input.ErrOutsideLink},
		},
		"link through a directory link outside the directory and back up": {
			tree: climbTree("../x/../config/..data/weekday.yaml"),
			err:  "config/hours.yaml: links to ../x/../config/..data/weekday.yaml, which leads outside",
			is:   []error{input.ErrOutsideLink},
		},
		"link through a directory link outside the directory and back up allowed": {
			tree: climbTree("../x/../config/..data/weekday.yaml"),
			opts: []input.Option{input.WithOutsideLinks(true)},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
			text: map[string]string{"config/hours.yaml": "# outside\n"},
		},
		"absolute link through a directory link and back up": {
			tree: climbTree("{dir}/x/../config/..data/weekday.yaml"),
			err:  "config/hours.yaml: links to {dir}/x/../config/..data/weekday.yaml, which leads outside",
			is:   []error{input.ErrOutsideLink},
		},
		"absolute link through a directory link and back up allowed": {
			tree: climbTree("{dir}/x/../config/..data/weekday.yaml"),
			opts: []input.Option{input.WithOutsideLinks(true)},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
			text: map[string]string{"config/hours.yaml": "# outside\n"},
		},
		"link through a file and back up": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "..data/weekday.yaml/../weekday.yaml"},
			},
			err:    "config/hours.yaml: {reason}",
			reason: "config/hours.yaml",
		},
		"absolute link inside the directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "{dir}/config/..data/weekday.yaml"},
			},
			want: []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"absolute link outside the directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			},
			err: "{dir}/config/hours.yaml: links to {dir}/shared/hours.yaml, which leads outside {dir}/config",
			is:  []error{input.ErrOutsideLink},
		},
		"link outside the directory to a directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "shared/x.yaml": ""},
				links: map[string]string{"config/shared.yaml": "../shared"},
			},
			want: []string{"config/cafe.yaml"},
		},
		"link outside the directory to a file the system refuses to read": {
			tree: tree{
				files:  map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc},
				links:  map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
				locked: []string{"shared/hours.yaml"},
			},
			opts:   []input.Option{input.WithOutsideLinks(true)},
			err:    "config/hours.yaml: {reason}",
			reason: "shared/hours.yaml",
			is:     []error{fs.ErrPermission},
		},
		"file the system refuses to read": {
			tree: tree{
				files:  map[string]string{"config/cafe.yaml": cafeDoc, "config/hours.yaml": hoursV1Doc},
				locked: []string{"config/hours.yaml"},
			},
			err:    "config/hours.yaml: {reason}",
			reason: "config/hours.yaml",
			is:     []error{fs.ErrPermission},
		},
		"link to a file the system refuses to read": {
			tree: tree{
				files:  map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links:  map[string]string{"config/hours.yaml": "..data/weekday.yaml"},
				locked: []string{"config/..data/weekday.yaml"},
			},
			err:    "config/hours.yaml: {reason}",
			reason: "config/hours.yaml",
			is:     []error{fs.ErrPermission},
		},
		"directory the system refuses to list": {
			tree: tree{
				files:  map[string]string{"config/cafe.yaml": cafeDoc, "config/sub/hours.yaml": hoursV1Doc},
				locked: []string{"config/sub"},
			},
			opts:   []input.Option{input.WithRecursive(true)},
			err:    "config/sub: {reason}",
			reason: "config/sub",
			is:     []error{fs.ErrPermission},
		},
		"directory whose name is not valid UTF-8": {
			tree: tree{
				dirs: []string{"config/caf\xe9"},
				files: map[string]string{
					"config/cafe.yaml":          cafeDoc,
					"config/caf\xe9/hours.yaml": hoursV1Doc,
				},
			},
			opts: []input.Option{input.WithRecursive(true)},
			want: []string{"config/cafe.yaml", "config/caf\xe9/hours.yaml"},
		},
		"link to a file the patterns do not select": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/.env": "SECRET=1\n"},
				links: map[string]string{"config/ci.yaml": ".env"},
			},
			err: "config/ci.yaml: links to .env, which does not match *.yaml or *.yml",
			is:  []error{input.ErrOutsideLink},
		},
		"dangling link": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/old.yaml": "archive/old.yaml"},
			},
			err: "config/old.yaml: links to archive/old.yaml, which does not exist",
			is:  []error{fs.ErrNotExist},
		},
		"dangling link outside the directory": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/old.yaml": "../archive/old.yaml"},
			},
			err: "config/old.yaml: links to ../archive/old.yaml, which does not exist",
			is:  []error{fs.ErrNotExist},
		},
		"dangling link through a directory that does not exist": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "../archive/../config/..data/weekday.yaml"},
			},
			err: "config/hours.yaml: links to ../archive/../config/..data/weekday.yaml, which does not exist",
			is:  []error{fs.ErrNotExist},
		},
		"link to itself": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc},
				links: map[string]string{"config/hours.yaml": "hours.yaml"},
			},
			err:    "config/hours.yaml: {reason}",
			reason: "config/hours.yaml",
		},
		"link through a file": {
			tree: tree{
				files: map[string]string{"config/cafe.yaml": cafeDoc, "config/..data/weekday.yaml": hoursV1Doc},
				links: map[string]string{"config/hours.yaml": "cafe.yaml/../..data/weekday.yaml"},
			},
			err:    "config/hours.yaml: {reason}",
			reason: "config/hours.yaml",
		},
		"input that does not exist": {
			input: "confg",
			err:   "confg: " + notExistReason(t),
			is:    []error{fs.ErrNotExist},
		},
		"bad pattern": {
			tree: tree{dirs: []string{"config"}},
			opts: []input.Option{input.WithExclude("[")},
			err:  `pattern "["`,
			is:   []error{input.ErrInvalidPattern},
		},
		"include pattern with a slash at its end": {
			tree: tree{dirs: []string{"config"}},
			opts: []input.Option{input.WithInclude("sub/")},
			is:   []error{input.ErrInvalidPattern},
		},
		"pattern that climbs out": {
			tree: tree{dirs: []string{"config"}},
			opts: []input.Option{input.WithExclude("../x.yaml")},
			is:   []error{input.ErrInvalidPattern},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tc.tree.write(t, dir)

			p := tc.input
			if p == "" {
				p = "config"
			}

			files, err := readAll(t, input.Path(filepath.Join(dir, p), tc.opts...))

			if tc.err != "" || tc.is != nil {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				if tc.err != "" {
					want := strings.ReplaceAll(tc.err, "{dir}", filepath.ToSlash(dir))

					if tc.reason != "" {
						reason := openReason(t, filepath.Join(dir, filepath.FromSlash(tc.reason)))
						want = strings.ReplaceAll(want, "{reason}", reason)
					}

					assert.Contains(t, filepath.ToSlash(formatAll(err)), want)
				}

				return
			}

			require.NoError(t, err)

			var want []string

			for _, w := range tc.want {
				want = append(want, filepath.ToSlash(filepath.Join(dir, w)))
			}

			assert.Equal(t, want, readNames(files))

			texts := readTexts(files)

			for name, text := range tc.text {
				assert.Equal(t, text, texts[filepath.ToSlash(filepath.Join(dir, name))], name)
			}
		})
	}
}

// climbTree returns a tree whose config/hours.yaml links to target. The
// target climbs through x, a link to the directory q/r outside config,
// and back up. The system thus reads q/config/..data/weekday.yaml, which
// holds "# outside", and not config/..data/weekday.yaml, which holds
// "# inside".
func climbTree(target string) tree {
	return tree{
		dirs: []string{"q/r"},
		files: map[string]string{
			"config/cafe.yaml":             cafeDoc,
			"config/..data/weekday.yaml":   "# inside\n",
			"q/config/..data/weekday.yaml": "# outside\n",
		},
		links: map[string]string{
			"config/hours.yaml": target,
			"x":                 "q/r",
		},
	}
}

// onSourceBuild returns a source option that calls fn each time a read
// builds a Source with it. A source option takes a value of an unexported
// type, and the first argument, which onSourceBuild does not call, lets
// the compiler infer that type.
func onSourceBuild[C any](_ func(C), fn func()) func(C) {
	return func(C) { fn() }
}

func TestPath_DotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// With link leading to real/sub, the OS reads the directory that
	// "link/.." names as real. The read takes the files there, and names
	// and labels each one by its path in real. It does not use the path
	// that cleaning "link/.." as text names.
	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"real/sub/.keep": "",
		"real/a.yaml":    "a: 1\n",
		"b.yaml":         "b: 1\n",
	})
	yamltest.SymlinkOrSkip(t, filepath.Join("real", "sub"), filepath.Join(dir, "link"))

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	want := filepath.Join(physical, "real", "a.yaml")

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path string
	}{
		"file":      {path: dir + "/link/../a.yaml"},
		"directory": {path: dir + "/link/.."},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			files, err := input.Path(tc.path).Files(t.Context())
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.NoError(t, files[0].Err)

			assert.Equal(t, want, files[0].Name)
			assert.Equal(t, want, files[0].Source.FilePath())
			assert.Equal(t, "a: 1\n", files[0].Source.Text())
		})
	}

	// The stat of a missing path fails, and the error names the path that
	// the OS looked for.
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		_, err := input.Path(dir + "/link/../missing.yaml").Files(t.Context())
		require.ErrorIs(t, err, fs.ErrNotExist)

		want := filepath.Join(physical, "real", "missing.yaml") + ": " + notExistReason(t)
		assert.Equal(t, want, err.Error())
	})
}

// TestPath_DotDotAfterRelativeLink reads relative paths in a working
// directory that holds a link named up. The link leads to "..", so the
// OS reads "up/.." as the parent of the parent of the working directory.
// The read names each file by a path that climbs there. The test
// changes the working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestPath_DotDotAfterRelativeLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"d/x.yaml":     "grand: 1\n",
		"p/c/d/x.yaml": "cwd: 1\n",
	})

	cwd := filepath.Join(dir, "p", "c")
	yamltest.SymlinkOrSkip(t, "..", filepath.Join(cwd, "up"))

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	t.Chdir(cwd)

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path string
	}{
		"file":      {path: "up/../d/x.yaml"},
		"directory": {path: "up/../d"},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			files, err := input.Path(tc.path).Files(t.Context())
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.NoError(t, files[0].Err)

			assert.Equal(t, filepath.Join("..", "..", "d", "x.yaml"), files[0].Name)
			assert.Equal(t, filepath.Join(physical, "d", "x.yaml"), files[0].Source.FilePath())
			assert.Equal(t, "grand: 1\n", files[0].Source.Text())
		})
	}
}

// TestPath_SymlinkedWorkDir reads relative paths in a working directory
// that the shell names by cwdlink, a link to real/sub. The OS reads
// "../d" from real, so the read takes real/d. A ".." that follows a
// plain directory keeps the working directory as the shell names it.
//
//nolint:paralleltest // t.Chdir does not work in a parallel test.
func TestPath_SymlinkedWorkDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"real/d/x.yaml":     "real: 1\n",
		"real/sub/a/.keep":  "",
		"real/sub/x.yaml":   "sub: 1\n",
		"real/sub/d/x.yaml": "cwd: 1\n",
	})

	cwd := filepath.Join(dir, "cwdlink")
	yamltest.SymlinkOrSkip(t, filepath.Join("real", "sub"), cwd)

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	// Chdir sets PWD, so os.Getwd returns the name of the link.
	t.Chdir(cwd)

	// The test builds each path as text, because filepath.Join cleans it.
	tcs := map[string]struct {
		path         string
		wantName     string
		wantFilePath string
		wantText     string
	}{
		"file out of the working directory": {
			path:         "../d/x.yaml",
			wantName:     filepath.Join("..", "d", "x.yaml"),
			wantFilePath: filepath.Join(physical, "real", "d", "x.yaml"),
			wantText:     "real: 1\n",
		},
		"directory out of the working directory": {
			path:         "../d",
			wantName:     filepath.Join("..", "d", "x.yaml"),
			wantFilePath: filepath.Join(physical, "real", "d", "x.yaml"),
			wantText:     "real: 1\n",
		},
		"file after a plain directory": {
			path:         "a/../x.yaml",
			wantName:     "x.yaml",
			wantFilePath: filepath.Join(cwd, "x.yaml"),
			wantText:     "sub: 1\n",
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // t.Chdir does not work in a parallel test.
		t.Run(name, func(t *testing.T) {
			files, err := input.Path(tc.path).Files(t.Context())
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.NoError(t, files[0].Err)

			assert.Equal(t, tc.wantName, files[0].Name)
			assert.Equal(t, tc.wantFilePath, files[0].Source.FilePath())
			assert.Equal(t, tc.wantText, files[0].Source.Text())
		})
	}
}

func TestPath_ChangedEntry(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree tree
		opts []input.Option
		// The target of the link that replaces config/b.yaml after the walk
		// lists the directory. Nothing replaces the entry when it is empty.
		link string
		want []string
		// The text of config/b.yaml in a read that takes it in.
		text string
		err  string
		is   []error
	}{
		"link outside the directory": {
			tree: tree{files: map[string]string{"secret.yaml": hoursV1Doc}},
			link: "../secret.yaml",
			err: stringtest.JoinLF(
				"{dir}/config/b.yaml: links to ../secret.yaml, which leads outside {dir}/config",
				"`-- copy the file into {dir}/config, or make {dir}/config itself the link",
			),
			is: []error{input.ErrOutsideLink},
		},
		"link outside the directory allowed": {
			tree: tree{files: map[string]string{"secret.yaml": hoursV1Doc}},
			opts: []input.Option{input.WithOutsideLinks(true)},
			link: "../secret.yaml",
			want: []string{"config/a.yaml", "config/b.yaml"},
			text: hoursV1Doc,
		},
		"link to a file the patterns do not select": {
			tree: tree{files: map[string]string{"config/hours.txt": hoursV1Doc}},
			link: "hours.txt",
			err:  "config/b.yaml: links to hours.txt, which does not match *.yaml or *.yml",
			is:   []error{input.ErrOutsideLink},
		},
		"link to a file the patterns select": {
			tree: tree{files: map[string]string{"config/sub/hours.yaml": hoursV1Doc}},
			link: "sub/hours.yaml",
			want: []string{"config/a.yaml", "config/b.yaml"},
			text: hoursV1Doc,
		},
		"dangling link": {
			link: "archive/b.yaml",
			err:  "config/b.yaml: links to archive/b.yaml, which does not exist",
			is:   []error{fs.ErrNotExist},
		},
		"link to a directory": {
			tree: tree{dirs: []string{"config/sub"}},
			link: "sub",
			want: []string{"config/a.yaml"},
		},
		"link through a link outside the directory that leads back": {
			tree: tree{
				files: map[string]string{"config/sub/hours.yaml": hoursV1Doc},
				links: map[string]string{"shared/hours.yaml": "../config/sub/hours.yaml"},
			},
			link: "../shared/hours.yaml",
			want: []string{"config/a.yaml", "config/b.yaml"},
			text: hoursV1Doc,
		},
		"removed entry": {
			err: "config/b.yaml: " + notExistReason(t),
			is:  []error{fs.ErrNotExist},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tree{files: map[string]string{"config/a.yaml": cafeDoc, "config/b.yaml": ""}}.write(t, dir)
			tc.tree.write(t, dir)

			entry := filepath.Join(dir, "config", "b.yaml")

			// The walk lists the directory before it reads a file, and it
			// reads a.yaml first. So the entry changes after its listing
			// and before its read.
			var once sync.Once

			change := func() {
				once.Do(func() {
					require.NoError(t, os.Remove(entry))

					if tc.link != "" {
						yamltest.SymlinkOrSkip(t, filepath.FromSlash(tc.link), entry)
					}
				})
			}

			opts := slices.Concat(tc.opts, []input.Option{
				input.WithSourceOptions(onSourceBuild(niceyaml.WithName(""), change)),
			})

			files, err := readAll(t, input.Path(filepath.Join(dir, "config"), opts...))
			require.NotNil(t, files)

			texts := readTexts(files)
			entry = filepath.ToSlash(entry)

			if tc.err != "" {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				want := strings.ReplaceAll(tc.err, "{dir}", filepath.ToSlash(dir))
				assert.Contains(t, filepath.ToSlash(formatAll(err)), want)
				assert.NotContains(t, texts, entry)

				return
			}

			require.NoError(t, err)

			var want []string

			for _, w := range tc.want {
				want = append(want, filepath.ToSlash(filepath.Join(dir, w)))
			}

			assert.Equal(t, want, readNames(files))
			assert.Equal(t, tc.text, texts[entry])
		})
	}
}

func TestPath_ChangedDirectory(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The tree that replaces config/sub after the walk lists it, below
		// the directory of the test. A tree with a "sub" link makes
		// config/sub a link.
		replace tree
		want    []string
		// The text of config/sub/b.yaml in a read that takes it in.
		text string
		err  string
		is   []error
	}{
		"another directory": {
			replace: tree{files: map[string]string{"config/sub/b.yaml": cafeDoc}},
			want:    []string{"config/a.yaml", "config/sub/b.yaml"},
			text:    cafeDoc,
		},
		"link outside the directory": {
			replace: tree{links: map[string]string{"config/sub": "../outside"}},
			err: stringtest.JoinLF(
				"{dir}/config/sub/b.yaml: links to ../outside, which leads outside {dir}/config",
				"`-- copy the file into {dir}/config, or make {dir}/config itself the link",
			),
			is: []error{input.ErrOutsideLink},
		},
		"removed directory": {
			err: "config/sub/b.yaml: " + notExistReason(t),
			is:  []error{fs.ErrNotExist},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tree{files: map[string]string{
				"config/a.yaml":     cafeDoc,
				"config/sub/b.yaml": hoursV1Doc,
				"outside/b.yaml":    hoursV1Doc,
			}}.write(t, dir)

			// The walk lists every directory before it reads a file, and it
			// reads a.yaml first. So config/sub changes after its listing
			// and before the read of b.yaml.
			var once sync.Once

			change := func() {
				once.Do(func() {
					require.NoError(t, os.RemoveAll(filepath.Join(dir, "config", "sub")))
					tc.replace.write(t, dir)
				})
			}

			files, err := readAll(t, input.Path(filepath.Join(dir, "config"), input.WithRecursive(true),
				input.WithSourceOptions(onSourceBuild(niceyaml.WithName(""), change))))
			require.NotNil(t, files)

			entry := filepath.ToSlash(filepath.Join(dir, "config", "sub", "b.yaml"))
			texts := readTexts(files)

			if tc.err != "" {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				want := strings.ReplaceAll(tc.err, "{dir}", filepath.ToSlash(dir))
				assert.Contains(t, filepath.ToSlash(formatAll(err)), want)
				assert.NotContains(t, texts, entry)

				return
			}

			require.NoError(t, err)

			var want []string

			for _, w := range tc.want {
				want = append(want, filepath.ToSlash(filepath.Join(dir, w)))
			}

			assert.Equal(t, want, readNames(files))
			assert.Equal(t, tc.text, texts[entry])
		})
	}
}

func TestFS(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree tree
		opts []input.Option
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
			opts: []input.Option{input.WithRecursive(true)},
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
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithExclude(".git/"),
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
			opts: []input.Option{input.WithInclude(".cafe.yaml")},
			err:  `and ".cafe.yaml" matches only those names`,
			is:   []error{input.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names without hidden": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{input.WithInclude("/cafe.yaml", ".store/*.yaml")},
			err:  `and ".store" matches only those names`,
			is:   []error{input.ErrInvalidPattern},
		},
		"link into a hidden directory that an include pattern names": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{
				input.WithHidden(true),
				input.WithInclude("/cafe.yaml", ".store/*.yaml"),
			},
			want: []string{"config/cafe.yaml"},
		},
		"link into a hidden directory excluded under recursion": {
			tree: tree{
				files: map[string]string{"config/.store/cafe.yaml": cafeDoc},
				links: map[string]string{"config/cafe.yaml": ".store/cafe.yaml"},
			},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithHidden(true),
				input.WithInclude("/cafe.yaml", ".store/*.yaml"),
				input.WithExclude(".store/"),
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
			is:  []error{input.ErrOutsideLink},
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
			is:  []error{input.ErrOutsideLink},
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

			files, err := readAll(t, input.FS(os.DirFS(dir), "config", tc.opts...))
			if tc.err != "" {
				require.Error(t, err)

				for _, target := range tc.is {
					require.ErrorIs(t, err, target)
				}

				assert.Contains(t, formatAll(err), tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, readNames(files))
		})
	}
}

func TestFS_UnreadableLinks(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input func(t *testing.T, dir string) input.Input
		links map[string]string
		err   string
		is    []error
	}{
		"absolute link with outside links allowed": {
			input: func(_ *testing.T, dir string) input.Input {
				return input.FS(os.DirFS(dir), "config", input.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			err:   "config/hours.yaml: links to {dir}/shared/hours.yaml, which leads outside the file system",
			is:    []error{input.ErrOutsideLink},
		},
		"link outside in fs.Sub of a file system that reports links": {
			input: func(t *testing.T, dir string) input.Input {
				t.Helper()

				return input.FS(subFS(t, os.DirFS(filepath.Dir(dir)), filepath.Base(dir)), "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: links to ../shared/hours.yaml, which leads outside config",
			is:    []error{input.ErrOutsideLink},
		},
		"link out of fs.Sub of a file system that reports links": {
			input: func(t *testing.T, dir string) input.Input {
				t.Helper()

				return input.FS(subFS(t, os.DirFS(dir), "config"), ".", input.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "hours.yaml: links to ../shared/hours.yaml, which leads outside the file system",
			is:    []error{input.ErrOutsideLink},
		},
		"link that ReadLink cannot read": {
			input: func(_ *testing.T, dir string) input.Input {
				return input.FS(deniedFS{FS: os.DirFS(dir), readLink: true}, "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"absolute link that ReadLink cannot read with outside links allowed": {
			input: func(_ *testing.T, dir string) input.Input {
				return input.FS(deniedFS{FS: os.DirFS(dir), readLink: true}, "config",
					input.WithOutsideLinks(true))
			},
			links: map[string]string{"config/hours.yaml": "{dir}/shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"link that Lstat cannot describe": {
			input: func(_ *testing.T, dir string) input.Input {
				return input.FS(deniedFS{FS: os.DirFS(dir), lstat: true}, "config")
			},
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   "config/hours.yaml: permission denied",
			is:    []error{fs.ErrPermission},
		},
		"link that Lstat cannot describe with outside links allowed": {
			input: func(_ *testing.T, dir string) input.Input {
				return input.FS(deniedFS{FS: os.DirFS(dir), lstat: true}, "config",
					input.WithOutsideLinks(true))
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

			files, err := readAll(t, tc.input(t, dir))
			require.Error(t, err)
			assert.Len(t, readNames(files), 1)

			for _, target := range tc.is {
				require.ErrorIs(t, err, target)
			}

			assert.Contains(t, formatAll(err), strings.ReplaceAll(tc.err, "{dir}", dir))
		})
	}
}

func TestFS_UnreadableFile(t *testing.T) {
	t.Parallel()

	fsys := unreadableFS{
		MapFS:      mapFS(map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursV1Doc}),
		unreadable: "config/hours.yaml",
	}

	files, err := readAll(t, input.FS(fsys, "config"))
	require.ErrorIs(t, err, fs.ErrPermission)
	assert.Equal(t, []string{"config/cafe.yaml"}, readNames(files))

	var failed *input.File

	for i := range files {
		if files[i].Err != nil {
			failed = &files[i]
		}
	}

	require.NotNil(t, failed)
	assert.Equal(t, "config/hours.yaml", failed.Name)
	assert.Equal(t, "config/hours.yaml: "+fs.ErrPermission.Error(), failed.Err.Error())
	assert.False(t, niceyaml.IsInvalid(failed.Err))

	// The binding reports the path of the file in the file system, as the
	// Source of a file that reads does.
	bound := slices.Collect(niceyaml.Bindings(failed.Err))
	require.Len(t, bound, 1)
	assert.Equal(t, "config/hours.yaml", bound[0].Name())
	assert.Equal(t, "config/hours.yaml", bound[0].FilePath())
}

// unreadableFS is a file system whose file at the path unreadable opens,
// and whose every read of that file fails with a [*fs.PathError] that
// matches [fs.ErrPermission], as the read of a file on disk does.
type unreadableFS struct {
	fstest.MapFS

	unreadable string
}

func (f unreadableFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil || name != f.unreadable {
		return file, err //nolint:wrapcheck // The walk reads the error of the file system.
	}

	return unreadableFile{File: file, name: name}, nil
}

// unreadableFile is the file of an [unreadableFS] whose reads fail.
type unreadableFile struct {
	fs.File

	name string
}

func (f unreadableFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: f.name, Err: fs.ErrPermission}
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
		opts  []input.Option
		want  []string
	}{
		// A file reads once for each entry that reaches it, and once at
		// its own path.
		"link to a file inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			want:  []string{"config/cafe.yaml", "config/same.yaml"},
		},
		"link to a file inside with outside links allowed": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			opts:  []input.Option{input.WithOutsideLinks(true)},
			want:  []string{"config/cafe.yaml", "config/same.yaml"},
		},
		"link to a link to a file inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml", "config/chain.yaml": "same.yaml"},
			want:  []string{"config/cafe.yaml", "config/chain.yaml", "config/same.yaml"},
		},
		"link to a directory inside in a recursive walk": {
			files: map[string]string{"config/real/hours.yaml": hoursV1Doc},
			links: map[string]string{"config/sub": "real"},
			opts:  []input.Option{input.WithRecursive(true)},
			want:  []string{"config/cafe.yaml", "config/real/hours.yaml", "config/sub/hours.yaml"},
		},
		"link to a file outside": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link to a file outside with outside links refused": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []input.Option{input.WithOutsideLinks(false)},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link to a directory outside": {
			links: map[string]string{"config/sub": "../shared"},
			want:  []string{"config/cafe.yaml"},
		},
		"link to a directory outside in a recursive walk": {
			links: map[string]string{"config/sub": "../shared"},
			opts:  []input.Option{input.WithRecursive(true)},
			want:  []string{"config/cafe.yaml", "config/sub/hours.yaml"},
		},
		"link to a directory outside in a recursive walk with outside links refused": {
			links: map[string]string{"config/sub": "../shared"},
			opts:  []input.Option{input.WithRecursive(true), input.WithOutsideLinks(false)},
			want:  []string{"config/cafe.yaml", "config/sub/hours.yaml"},
		},
	}

	for fsName, newFS := range fsyss {
		for name, tc := range tcs {
			t.Run(fsName+"/"+name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()

				texts := map[string]string{"config/cafe.yaml": cafeDoc, "shared/hours.yaml": hoursV1Doc}
				maps.Copy(texts, tc.files)

				tree{files: texts, links: tc.links}.write(t, dir)

				// The walk does not ask the file system about an entry that
				// the listing does not name as a link.
				files, err := readAll(t, input.FS(newFS(dir), "config", tc.opts...))
				require.NoError(t, err)
				assert.Equal(t, tc.want, readNames(files))
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
		opts  []input.Option
		want  []string
		err   string
		is    []error
	}{
		"link outside": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			err:   unreported("config/hours.yaml"),
			is:    []error{input.ErrOutsideLink},
		},
		"link outside with outside links refused": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []input.Option{input.WithOutsideLinks(false)},
			err:   unreported("config/hours.yaml"),
			is:    []error{input.ErrOutsideLink},
		},
		"link outside with outside links allowed": {
			links: map[string]string{"config/hours.yaml": "../shared/hours.yaml"},
			opts:  []input.Option{input.WithOutsideLinks(true)},
			want:  []string{"config/cafe.yaml", "config/hours.yaml"},
		},
		"link inside": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			err:   unreported("config/same.yaml"),
			is:    []error{input.ErrOutsideLink},
		},
		"link inside with outside links allowed": {
			links: map[string]string{"config/same.yaml": "cafe.yaml"},
			opts:  []input.Option{input.WithOutsideLinks(true)},
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
			opts:  []input.Option{input.WithOutsideLinks(true)},
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

				files, err := readAll(t, input.FS(newFS(t, dir), "config", tc.opts...))
				if tc.err != "" {
					require.Error(t, err)

					for _, target := range tc.is {
						require.ErrorIs(t, err, target)
					}

					assert.Contains(t, formatAll(err), tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, readNames(files))
			})
		}
	}
}

func TestFS_UncomparableFS(t *testing.T) {
	t.Parallel()

	// The struct type is comparable, but == panics on the map it holds.
	wrapped := struct{ fs.FS }{mapFS(map[string]string{"cafe.yaml": cafeDoc})}
	in := input.FS(wrapped, "config/cafe.yaml")

	files, err := readAll(t, input.Join(in, in))
	require.NoError(t, err)
	assert.Equal(t, []string{"config/cafe.yaml", "config/cafe.yaml"}, readNames(files))
}
