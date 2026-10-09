package matcher_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestFilePath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern  string
		filePath string
		want     bool
	}{
		"exact match": {
			pattern:  "config.yaml",
			filePath: "config.yaml",
			want:     true,
		},
		"exact no match": {
			pattern:  "config.yaml",
			filePath: "settings.yaml",
			want:     false,
		},
		"match in root": {
			pattern:  "*.yaml",
			filePath: "config.yaml",
			want:     true,
		},
		"root pattern does not match deep path": {
			pattern:  "*.yaml",
			filePath: "deep/path/config.yaml",
			want:     false,
		},
		"root pattern matches dot slash prefix": {
			pattern:  "*.yaml",
			filePath: "./config.yaml",
			want:     true,
		},
		"pattern with a dot element": {
			pattern:  "k8s/./*.yaml",
			filePath: "deploy/../k8s/app.yaml",
			want:     true,
		},
		"pattern no match": {
			pattern:  "**/*.json",
			filePath: "config.yaml",
			want:     false,
		},
		"k8s directory match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "deploy/k8s/app.yaml",
			want:     true,
		},
		"k8s deep match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "a/b/c/k8s/app.yaml",
			want:     true,
		},
		"k8s no match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "deploy/other/app.yaml",
			want:     false,
		},
		"any yaml file": {
			pattern:  "**/*.yaml",
			filePath: "any/path/file.yaml",
			want:     true,
		},
		"any yaml file in root": {
			pattern:  "**/*.yaml",
			filePath: "file.yaml",
			want:     true,
		},
		"parent after double star": {
			pattern:  "**/../x.yaml",
			filePath: "../x.yaml",
			want:     true,
		},
		"empty file path": {
			pattern:  "**/*.yaml",
			filePath: "",
			want:     false,
		},
		"empty file path with dot pattern": {
			pattern:  ".",
			filePath: "",
			want:     false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.MustFilePath(tc.pattern)
			doc := yamltest.FirstDocumentWithPath(t, "kind: Test", tc.filePath)

			got := match(t, m, doc)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFilePath_InvalidPattern(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern string
	}{
		"bad syntax": {
			pattern: "[",
		},
		"parent after star": {
			pattern: "configs/*/../x.yaml",
		},
		"empty": {
			pattern: "",
		},
		"empty brace group": {
			pattern: "{}",
		},
		"empty alternatives": {
			pattern: "{,}",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := matcher.FilePath(tc.pattern)
			require.ErrorIs(t, err, matcher.ErrInvalidPattern)

			assert.Panics(t, func() {
				matcher.MustFilePath(tc.pattern)
			})
		})
	}
}

func TestFilePath_BaseDir(t *testing.T) {
	t.Parallel()

	// Match compares paths as text, so no path here names a file. The
	// temporary directory gives each a root that is absolute on every
	// platform.
	root := t.TempDir()
	base := filepath.Join(root, "repo")
	globBase := filepath.Join(root, "proj[1]{a,b}")

	tcs := map[string]struct {
		pattern  string
		base     string
		filePath string
		want     bool
	}{
		"relative pattern under the base": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: filepath.Join(base, "configs", "app.yaml"),
			want:     true,
		},
		"relative pattern deeper under the base": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: filepath.Join(base, "sub", "configs", "app.yaml"),
			want:     false,
		},
		"relative pattern outside the base": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: filepath.Join(root, "other", "configs", "app.yaml"),
			want:     false,
		},
		"root pattern in the base": {
			pattern:  "*.yaml",
			base:     base,
			filePath: filepath.Join(base, "app.yaml"),
			want:     true,
		},
		"root pattern below the base": {
			pattern:  "*.yaml",
			base:     base,
			filePath: filepath.Join(base, "configs", "app.yaml"),
			want:     false,
		},
		"double star outside the base": {
			pattern:  "**/configs/*.yaml",
			base:     base,
			filePath: filepath.Join(root, "other", "configs", "app.yaml"),
			want:     true,
		},
		"parent pattern beside the base": {
			pattern:  "../shared/*.yaml",
			base:     base,
			filePath: filepath.Join(root, "shared", "app.yaml"),
			want:     true,
		},
		"parent pattern under the base": {
			pattern:  "../shared/*.yaml",
			base:     base,
			filePath: filepath.Join(base, "shared", "app.yaml"),
			want:     false,
		},
		"path with dot elements": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: base + "/sub/../configs/./app.yaml",
			want:     true,
		},
		"base with dot elements": {
			pattern:  "configs/*.yaml",
			base:     base + "/sub/..",
			filePath: filepath.Join(base, "configs", "app.yaml"),
			want:     true,
		},
		"base with glob characters": {
			pattern:  "configs/*.yaml",
			base:     globBase,
			filePath: filepath.Join(globBase, "configs", "app.yaml"),
			want:     true,
		},
		"absolute pattern outside the base": {
			pattern:  filepath.ToSlash(filepath.Join(root, "other")) + "/*.yaml",
			base:     base,
			filePath: filepath.Join(root, "other", "app.yaml"),
			want:     true,
		},
		"absolute pattern that names another directory": {
			pattern:  filepath.ToSlash(filepath.Join(root, "other")) + "/*.yaml",
			base:     base,
			filePath: filepath.Join(base, "app.yaml"),
			want:     false,
		},
		"relative path matches as written": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: "configs/app.yaml",
			want:     true,
		},
		"relative path that the pattern does not match": {
			pattern:  "configs/*.yaml",
			base:     base,
			filePath: "other/app.yaml",
			want:     false,
		},
		"empty file path": {
			pattern:  "*.yaml",
			base:     base,
			filePath: "",
			want:     false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.MustFilePath(tc.pattern, matcher.WithBaseDir(tc.base))
			doc := yamltest.FirstDocumentWithPath(t, "kind: Test", tc.filePath)

			assert.Equal(t, tc.want, match(t, m, doc))
		})
	}
}

func TestWithBaseDir(t *testing.T) {
	t.Parallel()

	t.Run("the last option wins", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		first, last := filepath.Join(root, "first"), filepath.Join(root, "last")

		m, err := matcher.FilePath("*.yaml", matcher.WithBaseDir(first), matcher.WithBaseDir(last))
		require.NoError(t, err)

		assert.True(t, match(t, m, yamltest.FirstDocumentWithPath(t, "kind: Test", filepath.Join(last, "app.yaml"))))
		assert.False(t, match(t, m, yamltest.FirstDocumentWithPath(t, "kind: Test", filepath.Join(first, "app.yaml"))))
	})

	t.Run("panics on an empty directory", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.WithBaseDir: dir is empty", func() {
			matcher.WithBaseDir("")
		})
	})
}

// The base directory is the working directory when FilePath runs, and a
// relative directory from WithBaseDir resolves against it then too. A
// later change of directory moves neither. The test changes the
// process's working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFilePath_WorkingDirectory(t *testing.T) {
	at, after := t.TempDir(), t.TempDir()

	t.Chdir(at)

	matchers := map[string]matcher.Matcher{
		"default base":  matcher.MustFilePath("configs/*.yaml"),
		"relative base": matcher.MustFilePath("*.yaml", matcher.WithBaseDir("configs")),
	}

	t.Chdir(after)

	for name, m := range matchers {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			under := yamltest.FirstDocumentWithPath(t, "kind: Test", filepath.Join(at, "configs", "app.yaml"))
			assert.True(t, match(t, m, under))

			moved := yamltest.FirstDocumentWithPath(t, "kind: Test", filepath.Join(after, "configs", "app.yaml"))
			assert.False(t, match(t, m, moved))
		})
	}
}

// A pattern matches a file on disk however the caller spelled its path
// and whatever directory the read ran in, since NewSourceFromFile gives
// the document the absolute path of the file. The test changes the
// process's working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFilePath_FileOnDisk(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	file := filepath.Join(proj, "configs", "app.yaml")

	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte("kind: Test\n"), 0o600))

	tcs := map[string]struct {
		// The working directory of the read.
		dir  string
		path string
	}{
		"from the base": {
			dir:  proj,
			path: "configs/app.yaml",
		},
		"with a dot element": {
			dir:  proj,
			path: "./configs/app.yaml",
		},
		"absolute": {
			dir:  proj,
			path: file,
		},
		"from the directory of the file": {
			dir:  filepath.Dir(file),
			path: "app.yaml",
		},
		"through the parent of the directory": {
			dir:  filepath.Dir(file),
			path: "../configs/app.yaml",
		},
		"from above the base": {
			dir:  root,
			path: "proj/configs/app.yaml",
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			t.Chdir(proj)

			under := matcher.MustFilePath("configs/*.yaml")
			anyDepth := matcher.MustFilePath("**/configs/*.yaml")
			elsewhere := matcher.MustFilePath("other/*.yaml")

			t.Chdir(tc.dir)

			source, err := niceyaml.NewSourceFromFile(tc.path)
			require.NoError(t, err)

			doc, err := source.Document()
			require.NoError(t, err)

			assert.True(t, match(t, under, doc))
			assert.True(t, match(t, anyDepth, doc))
			assert.False(t, match(t, elsewhere, doc))
		})
	}
}

// In a process whose working directory is gone, a relative base has no
// absolute path. A matcher then cannot decide for an absolute path that
// its relative pattern does not match as written, and it decides every
// other case as it does with a base. The test removes the process's
// working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFilePath_NoWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "configs", "app.yaml")

	gone := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.Mkdir(gone, 0o700))
	t.Chdir(gone)

	err := os.Remove(gone)
	if err != nil {
		t.Skipf("remove the working directory: %v", err)
	}

	_, err = os.Getwd()
	if err == nil {
		t.Skip("the platform still names the removed working directory")
	}

	tcs := map[string]struct {
		err      error
		pattern  string
		filePath string
		opts     []matcher.FilePathOption
		want     bool
	}{
		"relative pattern and absolute path": {
			pattern:  "configs/*.yaml",
			filePath: file,
			err:      matcher.ErrNoBaseDir,
		},
		"relative base": {
			pattern:  "*.yaml",
			opts:     []matcher.FilePathOption{matcher.WithBaseDir("configs")},
			filePath: file,
			err:      matcher.ErrNoBaseDir,
		},
		"absolute base": {
			pattern:  "configs/*.yaml",
			opts:     []matcher.FilePathOption{matcher.WithBaseDir(base)},
			filePath: file,
			want:     true,
		},
		"absolute path the pattern matches as written": {
			pattern:  "**/configs/*.yaml",
			filePath: file,
			want:     true,
		},
		"rooted pattern": {
			pattern:  filepath.ToSlash(filepath.Join(base, "other")) + "/*.yaml",
			filePath: file,
			want:     false,
		},
		"relative path": {
			pattern:  "configs/*.yaml",
			filePath: "configs/app.yaml",
			want:     true,
		},
		"relative path that the pattern does not match": {
			pattern:  "configs/*.yaml",
			filePath: "other/app.yaml",
			want:     false,
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			m, err := matcher.FilePath(tc.pattern, tc.opts...)
			require.NoError(t, err)

			doc := yamltest.FirstDocumentWithPath(t, "kind: Test", tc.filePath)

			got, err := m.Match(t.Context(), doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
