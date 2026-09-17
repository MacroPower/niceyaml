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

	// Create a subdirectory with a file for recursive glob testing.
	subdir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "003.yaml"), []byte("test"), 0o644))

	// Create a file whose name contains a glob metacharacter.
	bracketFile := filepath.Join(tmpDir, "cfg[1].txt")
	require.NoError(t, os.WriteFile(bracketFile, []byte("test"), 0o644))

	tests := map[string]struct {
		args      []string
		wantNames []string
		err       error
	}{
		"single file": {
			args:      []string{filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml"},
		},
		"multiple explicit files": {
			args:      []string{filepath.Join(tmpDir, "002.yaml"), filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml", "002.yaml"},
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
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml", "003.yaml"},
		},
		"wildcard skips directories": {
			args:      []string{filepath.Join(tmpDir, "*")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml", "cfg[1].txt"},
		},
		"literal name with metacharacter": {
			args:      []string{bracketFile},
			wantNames: []string{"cfg[1].txt"},
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
		"nonexistent file passes": {
			// ExpandPaths does not check file existence, only glob expansion.
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
