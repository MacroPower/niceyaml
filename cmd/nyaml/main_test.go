package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The profiler configures process-wide runtime settings, so these tests do
// not run in parallel.
//
//nolint:paralleltest // See above.
func TestRootCmdProfileFlags(t *testing.T) {
	tcs := map[string]struct {
		flag string
	}{
		"heap profile":   {flag: "--heap-profile"},
		"allocs profile": {flag: "--allocs-profile"},
		"cpu profile":    {flag: "--cpu-profile"},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			yamlPath := filepath.Join(dir, "doc.yaml")
			require.NoError(t, os.WriteFile(yamlPath, []byte("name: a\n"), 0o600))

			profilePath := filepath.Join(dir, "out.prof")

			rootCmd, stopProfiler := newRootCmd()
			rootCmd.SetOut(io.Discard)
			rootCmd.SetErr(io.Discard)
			rootCmd.SetArgs([]string{"validate", yamlPath, tc.flag, profilePath})

			require.NoError(t, rootCmd.Execute())
			require.NoError(t, stopProfiler())

			assert.FileExists(t, profilePath)
		})
	}
}

// TestRootCmdStopWithoutStart covers a command that never reaches
// PersistentPreRunE, such as --help or a flag parse error, and so leaves no
// profiler behind.
//
//nolint:paralleltest // See above.
func TestRootCmdStopWithoutStart(t *testing.T) {
	_, stopProfiler := newRootCmd()

	require.NoError(t, stopProfiler())
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
