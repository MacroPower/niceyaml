package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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

// printFixtures are the files that the print tests of view and diff read,
// by their names in a temporary directory.
var printFixtures = map[string]string{
	"a.yaml":     stringtest.LinesLF("a: 1", "b: [1, 2]"),
	"b.yaml":     stringtest.LinesLF("a: 1", "b: [1, 3]", "c: 3"),
	"copy.yaml":  stringtest.LinesLF("a: 1", "b: [1, 2]"),
	"empty.yaml": "",
}

// runPrint runs cmd with ctx and args in a directory that holds
// [printFixtures], with stdout in a buffer, which is not a terminal. Each
// arg that names a fixture becomes the path of that fixture, and "DIR/" in
// want becomes the directory. It returns the output, want, and the error.
func runPrint(
	ctx context.Context,
	t *testing.T,
	cmd *cobra.Command,
	args []string,
	want string,
) (string, string, error) {
	t.Helper()

	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, printFixtures)

	for i, arg := range args {
		if _, ok := printFixtures[arg]; ok || strings.ContainsAny(arg, "*[") {
			args[i] = filepath.Join(dir, arg)
		}
	}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)

	return out.String(), strings.ReplaceAll(want, "DIR/", dir+string(filepath.Separator)), err
}

func TestViewCmdPrint(t *testing.T) {
	t.Parallel()

	// Stdout is not a terminal, so view prints each file and exits rather
	// than open the interactive viewer.
	tcs := map[string]struct {
		err  error
		want string
		args []string
		// Runs the command with a context that has ended.
		canceled bool
	}{
		"one file": {
			args: []string{"a.yaml"},
			want: stringtest.LinesLF(
				"   1  a: 1",
				"   2  b: [1, 2]",
			),
		},
		"canceled": {
			args:     []string{"a.yaml", "b.yaml"},
			canceled: true,
			err:      context.Canceled,
		},
		"several files": {
			args: []string{"a.yaml", "empty.yaml", "b.yaml"},
			want: stringtest.LinesLF(
				"==> DIR/a.yaml <==",
				"   1  a: 1",
				"   2  b: [1, 2]",
				"",
				"==> DIR/empty.yaml <==",
				"",
				"==> DIR/b.yaml <==",
				"   1  a: 1",
				"   2  b: [1, 3]",
				"   3  c: 3",
			),
		},
		"empty file": {
			args: []string{"empty.yaml"},
			want: "",
		},
		"without line numbers": {
			args: []string{"--line-numbers=false", "a.yaml"},
			want: stringtest.LinesLF(
				" a: 1",
				" b: [1, 2]",
			),
		},
		"print flag and search term": {
			args: []string{"--print", "--search", "b", "a.yaml"},
			want: stringtest.LinesLF(
				"   1  a: 1",
				"   2  b: [1, 2]",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.canceled {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			got, want, err := runPrint(ctx, t, viewCmd(), tc.args, tc.want)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}
