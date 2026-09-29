package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainsGlobChars(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"asterisk": {
			input: "*.yaml",
			want:  true,
		},
		"question mark": {
			input: "file?.yaml",
			want:  true,
		},
		"bracket": {
			input: "file[0-9].yaml",
			want:  true,
		},
		"multiple globs": {
			input: "**/[a-z]*.yaml",
			want:  true,
		},
		"brace alternation": {
			input: "{a,b}.yaml",
			want:  true,
		},
		"no glob chars": {
			input: "file.yaml",
			want:  false,
		},
		"empty string": {
			input: "",
			want:  false,
		},
		"path without globs": {
			input: "/path/to/file.yaml",
			want:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := containsGlobChars(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()

	// Create a temporary directory with test files.
	tmpDir := t.TempDir()

	// Create test files with predictable names for sorting.
	files := []string{"002.yaml", "000.yaml", "001.yaml"}
	for _, name := range files {
		err := os.WriteFile(filepath.Join(tmpDir, name), []byte("test"), 0o644)
		require.NoError(t, err)
	}

	// Create a subdirectory with a file for recursive glob testing, and
	// one whose path sorts between the files above it, which a directory
	// walk reports after them.
	subdir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "003.yaml"), []byte("test"), 0o644))

	earlyDir := filepath.Join(tmpDir, "000dir")
	require.NoError(t, os.MkdirAll(earlyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(earlyDir, "x.yaml"), []byte("test"), 0o644))

	// Create a file whose name contains a glob metacharacter, and one its
	// name matches when read as a pattern.
	bracketFile := filepath.Join(tmpDir, "cfg[1].txt")
	require.NoError(t, os.WriteFile(bracketFile, []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "cfg1.txt"), []byte("test"), 0o644))

	// Create a directory whose name contains a glob metacharacter.
	bracketDir := filepath.Join(tmpDir, "data[1]")
	require.NoError(t, os.MkdirAll(bracketDir, 0o755))

	// Create a file whose name is no valid pattern, out of the way of the
	// wildcard cases.
	strayFile := filepath.Join(tmpDir, "stray", "report[2024.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(strayFile), 0o755))
	require.NoError(t, os.WriteFile(strayFile, []byte("test"), 0o644))

	tests := map[string]struct {
		args      []string
		wantNames []string
		err       error
	}{
		"single file": {
			args:      []string{filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml"},
		},
		"multiple explicit files keep their order": {
			args:      []string{filepath.Join(tmpDir, "002.yaml"), filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"002.yaml", "000.yaml"},
		},
		"repeated file appears once": {
			args:      []string{filepath.Join(tmpDir, "000.yaml"), filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml"},
		},
		"invalid pattern names an existing file": {
			args:      []string{strayFile},
			wantNames: []string{"report[2024.txt"},
		},
		"literal directory is an error": {
			args: []string{subdir},
			err:  errIsDirectory,
		},
		"overlapping globs name each file once": {
			args:      []string{filepath.Join(tmpDir, "00[01].yaml"), filepath.Join(tmpDir, "*.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"explicit file before a glob keeps its place": {
			args:      []string{filepath.Join(tmpDir, "002.yaml"), filepath.Join(tmpDir, "00[01].yaml")},
			wantNames: []string{"002.yaml", "000.yaml", "001.yaml"},
		},
		"glob pattern": {
			args:      []string{filepath.Join(tmpDir, "*.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"glob with question mark": {
			args:      []string{filepath.Join(tmpDir, "00?.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"glob with bracket": {
			args:      []string{filepath.Join(tmpDir, "00[01].yaml")},
			wantNames: []string{"000.yaml", "001.yaml"},
		},
		"mixed glob and explicit": {
			args:      []string{filepath.Join(tmpDir, "00[01].yaml"), filepath.Join(tmpDir, "002.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"recursive glob": {
			args:      []string{tmpDir + "/**/*.yaml"},
			wantNames: []string{"000.yaml", "x.yaml", "001.yaml", "002.yaml", "003.yaml"},
		},
		"wildcard skips directories": {
			args:      []string{filepath.Join(tmpDir, "*")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml", "cfg1.txt", "cfg[1].txt"},
		},
		"literal name with metacharacter": {
			// The name also matches cfg1.txt as a pattern, and the file
			// the user named wins.
			args:      []string{bracketFile},
			wantNames: []string{"cfg[1].txt"},
		},
		"glob with brace alternation": {
			args:      []string{filepath.Join(tmpDir, "{000,002}.yaml")},
			wantNames: []string{"000.yaml", "002.yaml"},
		},
		"no matches": {
			args: []string{filepath.Join(tmpDir, "*.json")},
			err:  errNoMatch,
		},
		"no matches among explicit files": {
			args: []string{filepath.Join(tmpDir, "000.yaml"), filepath.Join(tmpDir, "*.json")},
			err:  errNoMatch,
		},
		"only directories match": {
			args: []string{filepath.Join(tmpDir, "sub*")},
			err:  errNoMatch,
		},
		"literal directory with metacharacter": {
			args: []string{bracketDir},
			err:  errIsDirectory,
		},
		"nonexistent file passes": {
			// Glob expansion does not check file existence, so expandPaths
			// accepts a path with no file behind it.
			args:      []string{filepath.Join(tmpDir, "nonexistent.yaml")},
			wantNames: []string{"nonexistent.yaml"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			paths, err := expandPaths(tc.args...)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			require.Len(t, paths, len(tc.wantNames))

			for i, path := range paths {
				assert.Equal(t, tc.wantNames[i], filepath.Base(path))
			}
		})
	}
}

func TestExpandPathsSymlinks(t *testing.T) {
	t.Parallel()

	// Create a directory structure with symlinks:
	// tmpDir/
	//   sub/
	//     deep/
	//     x.yaml
	//   x.yaml
	//   link.yaml -> sub/x.yaml
	//   ldir -> sub
	//   deeplink -> sub/deep
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "sub")
	target := filepath.Join(subdir, "x.yaml")
	top := filepath.Join(tmpDir, "x.yaml")
	link := filepath.Join(tmpDir, "link.yaml")
	throughDir := filepath.Join(tmpDir, "ldir", "x.yaml")

	// The OS steps up from the directory deeplink leads to, so this name
	// reaches sub/x.yaml. Joining it with filepath.Join would clean it to
	// x.yaml.
	dotDot := tmpDir + "/deeplink/../x.yaml"

	require.NoError(t, os.MkdirAll(filepath.Join(subdir, "deep"), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(top, []byte("top"), 0o644))

	err := os.Symlink(filepath.Join("sub", "x.yaml"), link)
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	require.NoError(t, os.Symlink("sub", filepath.Join(tmpDir, "ldir")))
	require.NoError(t, os.Symlink(filepath.Join("sub", "deep"), filepath.Join(tmpDir, "deeplink")))

	tcs := map[string]struct {
		args []string
		want []string
	}{
		"recursive glob with symlinked file": {
			args: []string{tmpDir + "/**/*.yaml"},
			want: []string{link, top},
		},
		"symlink and target named explicitly": {
			args: []string{link, target},
			want: []string{link},
		},
		"target and symlink named explicitly": {
			args: []string{target, link},
			want: []string{target},
		},
		"file through symlinked directory and directly": {
			args: []string{throughDir, target},
			want: []string{throughDir},
		},
		"dot-dot after a symlinked directory and another file": {
			args: []string{dotDot, top},
			want: []string{dotDot, top},
		},
		"dot-dot after a symlinked directory and the file it reaches": {
			args: []string{dotDot, target},
			want: []string{dotDot},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			paths, err := expandPaths(tc.args...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, paths)
		})
	}
}

func TestGlob(t *testing.T) {
	t.Parallel()

	// Create temporary directory structure for testing.
	tmpDir := t.TempDir()

	// Create test directory structure:
	// tmpDir/
	//   a.yaml
	//   b.yml
	//   subdir/
	//     c.yaml
	//     deep/
	//       d.yaml
	//   k8s/
	//     deploy.yaml
	subdir := filepath.Join(tmpDir, "subdir")
	subdirDeep := filepath.Join(subdir, "deep")
	k8sDir := filepath.Join(tmpDir, "k8s")

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.yaml"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.yml"), []byte("b"), 0o644))
	require.NoError(t, os.MkdirAll(subdirDeep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "c.yaml"), []byte("c"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subdirDeep, "d.yaml"), []byte("d"), 0o644))
	require.NoError(t, os.MkdirAll(k8sDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(k8sDir, "deploy.yaml"), []byte("k8s"), 0o644))

	tcs := map[string]struct {
		pattern   string
		wantFiles []string
		err       string
	}{
		"simple wildcard": {
			pattern:   filepath.Join(tmpDir, "*.yaml"),
			wantFiles: []string{filepath.Join(tmpDir, "a.yaml")},
		},
		"simple wildcard yml": {
			pattern:   filepath.Join(tmpDir, "*.yml"),
			wantFiles: []string{filepath.Join(tmpDir, "b.yml")},
		},
		"recursive yaml": {
			pattern: tmpDir + "/**/*.yaml",
			wantFiles: []string{
				filepath.Join(tmpDir, "a.yaml"),
				filepath.Join(k8sDir, "deploy.yaml"),
				filepath.Join(subdir, "c.yaml"),
				filepath.Join(subdirDeep, "d.yaml"),
			},
		},
		"subdir only": {
			pattern:   subdir + "/*.yaml",
			wantFiles: []string{filepath.Join(subdir, "c.yaml")},
		},
		"subdir recursive": {
			pattern: subdir + "/**/*.yaml",
			wantFiles: []string{
				filepath.Join(subdir, "c.yaml"),
				filepath.Join(subdirDeep, "d.yaml"),
			},
		},
		"k8s specific": {
			pattern:   k8sDir + "/*.yaml",
			wantFiles: []string{filepath.Join(k8sDir, "deploy.yaml")},
		},
		"double star k8s": {
			pattern:   tmpDir + "/**/k8s/*.yaml",
			wantFiles: []string{filepath.Join(k8sDir, "deploy.yaml")},
		},
		"no matches": {
			pattern:   filepath.Join(tmpDir, "*.json"),
			wantFiles: []string{},
		},
		"directories excluded": {
			pattern: filepath.Join(tmpDir, "*"),
			wantFiles: []string{
				filepath.Join(tmpDir, "a.yaml"),
				filepath.Join(tmpDir, "b.yml"),
			},
		},
		"recursive wildcard excludes directories": {
			pattern: tmpDir + "/**",
			wantFiles: []string{
				filepath.Join(tmpDir, "a.yaml"),
				filepath.Join(tmpDir, "b.yml"),
				filepath.Join(k8sDir, "deploy.yaml"),
				filepath.Join(subdir, "c.yaml"),
				filepath.Join(subdirDeep, "d.yaml"),
			},
		},
		"brace alternatives with dot-dot and dot": {
			pattern: subdir + "/{../k8s,.}/*.yaml",
			wantFiles: []string{
				subdir + "/../k8s/deploy.yaml",
				subdir + "/./c.yaml",
			},
		},
		"brace alternative with a leading dot": {
			pattern: tmpDir + "/{./a,k8s/deploy}.yaml",
			wantFiles: []string{
				tmpDir + "/./a.yaml",
				filepath.Join(k8sDir, "deploy.yaml"),
			},
		},
		"separator before the wildcard is not doubled": {
			pattern:   tmpDir + "//*.yaml",
			wantFiles: []string{filepath.Join(tmpDir, "a.yaml")},
		},
		"dot and empty elements after a wildcard": {
			pattern:   tmpDir + "/*//./c.yaml",
			wantFiles: []string{filepath.Join(subdir, "c.yaml")},
		},
		"dot-dot after a wildcard": {
			pattern: tmpDir + "/sub*/../a.yaml",
			err:     errDotDotAfterMeta.Error(),
		},
		"brace alternatives matching one file": {
			pattern:   tmpDir + "/{a,[a]}.yaml",
			wantFiles: []string{filepath.Join(tmpDir, "a.yaml")},
		},
		"invalid pattern": {
			pattern: "[",
			err:     "glob",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			matches, err := glob(tc.pattern)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantFiles, matches)
		})
	}
}

func TestGlobSymlinks(t *testing.T) {
	t.Parallel()

	// Create a directory structure with symlinks:
	// tmpDir/
	//   sub/
	//     deep/
	//     x.yaml
	//     up -> ..
	//     up2 -> ..
	//   deeplink -> sub/deep
	//   dir.yaml -> sub
	//   file.yaml -> sub/x.yaml
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "sub")

	require.NoError(t, os.MkdirAll(filepath.Join(subdir, "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "x.yaml"), []byte("x"), 0o644))

	err := os.Symlink("..", filepath.Join(subdir, "up"))
	if err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	require.NoError(t, os.Symlink("..", filepath.Join(subdir, "up2")))
	require.NoError(t, os.Symlink("sub", filepath.Join(tmpDir, "dir.yaml")))
	require.NoError(t, os.Symlink(filepath.Join("sub", "x.yaml"), filepath.Join(tmpDir, "file.yaml")))
	require.NoError(t, os.Symlink(filepath.Join("sub", "deep"), filepath.Join(tmpDir, "deeplink")))

	tcs := map[string]struct {
		pattern   string
		wantFiles []string
	}{
		"recursive glob skips symlinked directories": {
			pattern: tmpDir + "/**/*.yaml",
			wantFiles: []string{
				filepath.Join(tmpDir, "file.yaml"),
				filepath.Join(subdir, "x.yaml"),
			},
		},
		"wildcard skips symlinked directories": {
			pattern:   filepath.Join(tmpDir, "*.yaml"),
			wantFiles: []string{filepath.Join(tmpDir, "file.yaml")},
		},
		"dot-dot after a symlinked directory": {
			// The OS steps up from the directory deeplink leads to, so the
			// pattern matches sub/x.yaml rather than file.yaml.
			pattern:   tmpDir + "/deeplink/../*.yaml",
			wantFiles: []string{tmpDir + "/deeplink/../x.yaml"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			matches, err := glob(tc.pattern)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.wantFiles, matches)
		})
	}
}
