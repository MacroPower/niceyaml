package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestLoadSources(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// Files to load, relative to a temporary directory.
		files []string
		// The file among files that the test leaves unwritten, or empty
		// when it writes every file.
		missing string
		err     error
		// Makes the missing file a dangling symlink, as a glob can match.
		dangling bool
	}{
		"distinct base names": {files: []string{"a/one.yaml", "b/two.yaml"}},
		"shared base name":    {files: []string{"a/config.yaml", "b/config.yaml"}},
		"missing file": {
			files:   []string{"a/config.yaml", "b/config.yaml"},
			missing: "b/config.yaml",
			err:     fs.ErrNotExist,
		},
		"dangling symlink with a newline in the name": {
			files:    []string{"a/config.yaml", "b/evil\n└── other.yaml"},
			missing:  "b/evil\n└── other.yaml",
			dangling: true,
			err:      fs.ErrNotExist,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			paths := make([]string, len(tc.files))
			for i, file := range tc.files {
				paths[i] = filepath.Join(dir, file)

				if file == tc.missing {
					// Only the file is missing, not its directory.
					require.NoError(t, os.MkdirAll(filepath.Dir(paths[i]), 0o750))

					if tc.dangling {
						yamltest.SymlinkOrSkip(t, filepath.Join(dir, "nowhere.yaml"), paths[i])
					}

					continue
				}

				yamltest.WriteFile(t, dir, file, "a: 1\n")
			}

			sources, err := loadSources(paths)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				// The read error names the path with its control
				// characters escaped, and loadSources adds no second copy.
				missing := escape.Line(filepath.Join(dir, tc.missing))
				assert.Equal(t, 1, strings.Count(err.Error(), missing), err.Error())

				return
			}

			require.NoError(t, err)
			require.Len(t, sources, len(paths))

			labels := revisionLabels(paths)
			for i, source := range sources {
				assert.Equal(t, labels[i], source.Name())
				assert.Equal(t, paths[i], source.FilePath())
			}

			m := newModel(&modelOptions{sources: sources})
			assert.Equal(t, labels, m.viewport.RevisionNames())
		})
	}
}

func TestViewCmdLineNumbersFlag(t *testing.T) {
	t.Parallel()

	// A boolean shorthand takes no value, so a shorthand on a flag that
	// defaults to true could only repeat the default. The long flag carries
	// the value that turns the line numbers off.
	flag := viewCmd().Flags().Lookup("line-numbers")
	require.NotNil(t, flag)
	assert.Empty(t, flag.Shorthand)
	assert.Equal(t, "true", flag.DefValue)

	cmd := viewCmd()
	require.NoError(t, cmd.Flags().Parse([]string{"--line-numbers=false"}))

	lineNumbers, err := cmd.Flags().GetBool("line-numbers")
	require.NoError(t, err)
	assert.False(t, lineNumbers)
}

func TestViewCmdLimitFlags(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		msg  string
		args []string
	}{
		"a negative byte limit": {
			args: []string{"--byte-limit", "-1"},
			msg:  "--byte-limit -1: limit is negative",
		},
		"a negative document byte limit": {
			args: []string{"--document-byte-limit", "-1"},
			msg:  "--document-byte-limit -1: limit is negative",
		},
		"a negative depth limit": {
			args: []string{"--depth-limit", "-1"},
			msg:  "--depth-limit -1: limit is negative",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The file does not exist, so the flag error shows that the
			// command checks the flags before it reads any file.
			cmd := viewCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs(append(tc.args, filepath.Join(t.TempDir(), "missing.yaml")))

			err := cmd.Execute()
			require.ErrorIs(t, err, errNegativeLimit)
			require.NotErrorIs(t, err, fs.ErrNotExist)
			assert.EqualError(t, err, tc.msg)
		})
	}
}
