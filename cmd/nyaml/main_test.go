package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style/theme"
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

			// An explicit schema keeps validate from fetching the SchemaStore catalog.
			schemaPath := filepath.Join(dir, "schema.json")
			require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

			profilePath := filepath.Join(dir, "out.prof")

			rootCmd, stopProfiler := newRootCmd()
			rootCmd.SetOut(io.Discard)
			rootCmd.SetErr(io.Discard)
			rootCmd.SetArgs([]string{"validate", "--schema", schemaPath, yamlPath, tc.flag, profilePath})

			require.NoError(t, rootCmd.Execute())
			require.NoError(t, stopProfiler())

			assert.FileExists(t, profilePath)
		})
	}
}

// TestRootCmdProfileRates checks that a run samples blocking events only
// when it writes a block profile, and contention events only when it writes
// a mutex profile.
//
//nolint:paralleltest // See above.
func TestRootCmdProfileRates(t *testing.T) {
	tcs := map[string]struct {
		flag      string
		wantMutex int
		wantBlock bool
	}{
		"no profile flags":  {},
		"heap profile only": {flag: "--heap-profile"},
		"block profile":     {flag: "--block-profile", wantBlock: true},
		"mutex profile":     {flag: "--mutex-profile", wantMutex: 1},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			runtime.SetBlockProfileRate(0)
			runtime.SetMutexProfileFraction(0)
			t.Cleanup(func() {
				runtime.SetBlockProfileRate(0)
				runtime.SetMutexProfileFraction(0)
			})

			dir := t.TempDir()

			yamlPath := filepath.Join(dir, "doc.yaml")
			require.NoError(t, os.WriteFile(yamlPath, []byte("name: a\n"), 0o600))

			// An explicit schema keeps validate from fetching the SchemaStore catalog.
			schemaPath := filepath.Join(dir, "schema.json")
			require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

			args := []string{"validate", "--schema", schemaPath, yamlPath}
			if tc.flag != "" {
				args = append(args, tc.flag, filepath.Join(dir, "out.prof"))
			}

			rootCmd, stopProfiler := newRootCmd()
			rootCmd.SetOut(io.Discard)
			rootCmd.SetErr(io.Discard)
			rootCmd.SetArgs(args)

			require.NoError(t, rootCmd.Execute())

			// Given a negative rate, SetMutexProfileFraction reports the
			// current fraction and keeps it.
			gotMutex := runtime.SetMutexProfileFraction(-1)

			// The runtime offers no getter for the block profile rate, so the
			// test waits on a channel once and checks whether the block
			// profile counted the wait. At rate 0 the runtime records no
			// blocking events.
			before := blockEvents()

			// The runtime counts the wait only if the receive parks. In a
			// synctest bubble the sleep ends only once every goroutine in
			// the bubble has durably blocked, so done closes only after the
			// receive has parked on it.
			synctest.Test(t, func(*testing.T) {
				done := make(chan struct{})
				go func() {
					time.Sleep(10 * time.Millisecond)
					close(done)
				}()

				<-done
			})

			gotBlock := blockEvents() > before

			require.NoError(t, stopProfiler())
			assert.Equal(t, tc.wantMutex, gotMutex)
			assert.Equal(t, tc.wantBlock, gotBlock)
		})
	}
}

// blockEvents returns the number of blocking events the block profile has
// counted since the process started.
func blockEvents() int64 {
	n, _ := runtime.BlockProfile(nil)

	for {
		// Leave room for records that other goroutines add between calls.
		records := make([]runtime.BlockProfileRecord, n+16)

		var ok bool

		n, ok = runtime.BlockProfile(records)
		if !ok {
			continue
		}

		var count int64

		for i := range records[:n] {
			count += records[i].Count
		}

		return count
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	// Each case runs nyaml validate in a directory of its own that holds
	// schema.json, the schema of nameSchema. An argument that names no
	// flag names a path in that directory.
	tcs := map[string]struct {
		// Contents of the files to write, by name.
		files    map[string]string
		args     []string
		canceled bool
		want     int
	}{
		"valid document": {
			files: map[string]string{"a.yaml": "name: a\n"},
			args:  []string{"--schema", "schema.json", "a.yaml"},
			want:  0,
		},
		"valid document of many aliases to a small mapping": {
			files: map[string]string{
				"a.yaml": "name: a\nbase: &b {os: linux, arch: amd64}\nmatrix:\n" + strings.Repeat("  - *b\n", 400),
			},
			args: []string{"--schema", "schema.json", "a.yaml"},
			want: 0,
		},
		"schema violation": {
			files: map[string]string{"a.yaml": "value: 1\n"},
			args:  []string{"--schema", "schema.json", "a.yaml"},
			want:  exitInvalid,
		},
		"syntax error": {
			files: map[string]string{"a.yaml": "name: [\n"},
			args:  []string{"--schema", "schema.json", "a.yaml"},
			want:  exitInvalid,
		},
		"invalid documents in two files": {
			files: map[string]string{"a.yaml": "value: 1\n", "b.yaml": "name: [\n"},
			args:  []string{"--schema", "schema.json", "a.yaml", "b.yaml"},
			want:  exitInvalid,
		},
		"file that does not read": {
			args: []string{"--schema", "schema.json", "missing.yaml"},
			want: exitFailure,
		},
		"invalid document beside a file that does not read": {
			files: map[string]string{"a.yaml": "value: 1\n"},
			args:  []string{"--schema", "schema.json", "a.yaml", "missing.yaml"},
			want:  exitFailure,
		},
		"schema that does not load": {
			files: map[string]string{"a.yaml": "name: a\n"},
			args:  []string{"--schema", "missing.json", "a.yaml"},
			want:  exitFailure,
		},
		"invalid document beside a directive that does not load": {
			files: map[string]string{
				"a.yaml": "# yaml-language-server: $schema=./schema.json\nvalue: 1\n",
				"b.yaml": "# yaml-language-server: $schema=./missing.json\nname: b\n",
			},
			args: []string{"a.yaml", "b.yaml"},
			want: exitFailure,
		},
		"no file argument": {
			args: []string{"--schema", "schema.json"},
			want: exitFailure,
		},
		"canceled run": {
			files:    map[string]string{"a.yaml": "value: 1\n"},
			args:     []string{"--schema", "schema.json", "a.yaml"},
			canceled: true,
			want:     exitFailure,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.json"), nameSchema, 0o600))

			for file, content := range tc.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600))
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				if !strings.HasPrefix(arg, "-") {
					arg = filepath.Join(dir, arg)
				}

				args = append(args, arg)
			}

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			if tc.canceled {
				cancel()
			}

			cmd := validateCmd()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(args)

			assert.Equal(t, tc.want, exitCode(cmd.ExecuteContext(ctx)))
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

func TestHelpColorScheme(t *testing.T) {
	t.Parallel()

	light, ok := theme.Builtin().Get(lightHelpTheme)
	require.True(t, ok)
	require.Equal(t, theme.Light, light.Mode)

	csFunc := helpColorScheme()

	tcs := map[string]struct {
		isDark bool
		want   fang.ColorScheme
	}{
		"light terminal": {
			isDark: false,
			want:   fangs.ColorScheme(light),
		},
		"dark terminal": {
			isDark: true,
			want:   fangs.ColorScheme(theme.Charm),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, csFunc(lipgloss.LightDark(tc.isDark)))
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
