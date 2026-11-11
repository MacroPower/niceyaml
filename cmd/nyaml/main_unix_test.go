//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// notifyHelperEnv tells the test binary to run [runNotifyHelper] in place
// of [TestNotifyContext], so the signals the test sends reach a child
// process rather than the test binary itself.
const notifyHelperEnv = "NYAML_NOTIFY_CONTEXT_HELPER"

func TestNotifyContext(t *testing.T) {
	t.Parallel()

	if os.Getenv(notifyHelperEnv) == "1" {
		runNotifyHelper()

		return
	}

	// The first SIGINT cancels the context, and the next one kills the
	// process, so a run stuck in work that ignores the context still ends.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNotifyContext$")

	cmd.Env = append(os.Environ(), notifyHelperEnv+"=1")

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	lines := bufio.NewScanner(stdout)

	waitForLine(t, lines, "ready")
	require.NoError(t, cmd.Process.Signal(syscall.SIGINT))
	waitForLine(t, lines, "canceled")

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	// The helper gives SIGINT its default action back on a goroutine of
	// its own, so the test repeats the signal until the process ends.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	timeout := time.After(30 * time.Second)

	for {
		select {
		case err := <-done:
			var exitErr *exec.ExitError

			require.ErrorAs(t, err, &exitErr)

			status, ok := exitErr.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			assert.True(t, status.Signaled(), "helper exited with %v", err)
			assert.Equal(t, syscall.SIGINT, status.Signal())

			return

		case <-tick.C:
			// The process may end between ticks, which fails the send.
			_ = cmd.Process.Signal(syscall.SIGINT) //nolint:errcheck // See above.

		case <-timeout:
			t.Fatal("a second SIGINT did not end the helper")
		}
	}
}

// runNotifyHelper reports on stdout when [notifyContext] is ready and when
// a signal cancels its context, then waits for a second signal to end the
// process.
func runNotifyHelper() {
	ctx, stop := notifyContext(context.Background())
	defer stop()

	fmt.Println("ready")

	<-ctx.Done()

	fmt.Println("canceled")

	time.Sleep(time.Minute)
}

// waitForLine reads lines until one equals want.
func waitForLine(t *testing.T, lines *bufio.Scanner, want string) {
	t.Helper()

	for lines.Scan() {
		if lines.Text() == want {
			return
		}
	}

	t.Fatalf("helper output ended before %q: %v", want, lines.Err())
}

// mainArgsEnv holds, as a JSON array, the arguments that a test binary
// started by [startOnTerminal] runs main with in place of its test.
const mainArgsEnv = "NYAML_MAIN_ARGS"

// terminalTimeout bounds each wait of a test for a process that
// [startOnTerminal] started.
const terminalTimeout = 30 * time.Second

// runMainHelper runs main with the arguments in [mainArgsEnv] and reports
// whether the variable was set. A test that [startOnTerminal] names
// returns when runMainHelper reports true.
func runMainHelper(t *testing.T) bool {
	t.Helper()

	raw := os.Getenv(mainArgsEnv)
	if raw == "" {
		return false
	}

	var args []string

	require.NoError(t, json.Unmarshal([]byte(raw), &args))

	os.Args = append([]string{"nyaml"}, args...)

	main()

	return true
}

// terminalRun is a run of nyaml on a pseudo-terminal that
// [startOnTerminal] starts.
type terminalRun struct {
	// The controlling side of the terminal. A write types into the
	// terminal, and the process reads what it writes.
	ptmx *os.File
	// Each chunk that the process writes to the terminal, closed when the
	// terminal ends.
	chunks chan []byte
	// The result of the wait for the process.
	done chan error
	// Everything that the process wrote to the terminal so far, as the
	// reads of the test collect it.
	output []byte
}

// startOnTerminal starts the test binary on an 80 by 24 pseudo-terminal,
// where it runs main with args in place of the test named test. The test
// must call [runMainHelper] first. The process gets the environment of
// the test with TERM=xterm-256color and the variables of env, but without
// the variables that pick a color profile.
func startOnTerminal(t *testing.T, test string, env []string, args ...string) *terminalRun {
	t.Helper()

	raw, err := json.Marshal(args)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+test+"$")

	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "NO_COLOR", "COLORTERM", "CLICOLOR", "CLICOLOR_FORCE":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}

	cmd.Env = append(cmd.Env, "TERM=xterm-256color", mainArgsEnv+"="+string(raw))
	cmd.Env = append(cmd.Env, env...)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = ptmx.Close() //nolint:errcheck // Best effort.
	})

	run := &terminalRun{
		ptmx:   ptmx,
		chunks: make(chan []byte, 64),
		done:   make(chan error, 1),
	}

	// The process blocks when the terminal buffer fills, so a goroutine
	// reads everything it writes. The read ends with an error when the
	// process closes the terminal.
	go func() {
		defer close(run.chunks)

		for {
			buf := make([]byte, 4096)

			n, err := ptmx.Read(buf)
			if n > 0 {
				run.chunks <- buf[:n]
			}

			if err != nil {
				return
			}
		}
	}()

	go func() {
		run.done <- cmd.Wait()
	}()

	return run
}

// readUntil reads the terminal until match reports true for everything
// that the process wrote to it so far.
func (r *terminalRun) readUntil(t *testing.T, match func(output string) bool) {
	t.Helper()

	timeout := time.After(terminalTimeout)

	for !match(string(r.output)) {
		select {
		case chunk, ok := <-r.chunks:
			if !ok {
				t.Fatalf("the terminal ended before the expected output: %q", r.output)
			}

			r.output = append(r.output, chunk...)

		case <-timeout:
			t.Fatalf("the expected output did not appear: %q", r.output)
		}
	}
}

// wait reads the terminal until it ends, waits for the process to exit,
// and returns everything that the process wrote to the terminal and the
// result of the wait.
func (r *terminalRun) wait(t *testing.T) (string, error) {
	t.Helper()

	timeout := time.After(terminalTimeout)

	for {
		select {
		case chunk, ok := <-r.chunks:
			if !ok {
				r.chunks = nil

				continue
			}

			r.output = append(r.output, chunk...)

		case err := <-r.done:
			// The process has exited, so the reader goroutine reads the
			// rest of the output and then closes chunks.
			if r.chunks != nil {
				for chunk := range r.chunks {
					r.output = append(r.output, chunk...)
				}
			}

			return string(r.output), err

		case <-timeout:
			t.Fatalf("the process did not exit: %q", r.output)
		}
	}
}

// viewHelperEnv names the file that the test binary views with nyaml view
// in place of running [TestViewSignal], so the signals the test sends
// reach a child process rather than the test binary itself.
const viewHelperEnv = "NYAML_VIEW_SIGNAL_HELPER"

func TestViewSignal(t *testing.T) {
	t.Parallel()

	if path := os.Getenv(viewHelperEnv); path != "" {
		os.Args = []string{"nyaml", "view", path}

		main()

		return
	}

	// Either signal cancels the context of the run, and the viewer ends
	// with an error that reports the cancellation.
	tcs := map[string]struct {
		sig syscall.Signal
	}{
		"SIGINT":  {sig: syscall.SIGINT},
		"SIGTERM": {sig: syscall.SIGTERM},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := yamltest.WriteFile(t, t.TempDir(), "a.yaml", "a: 1\n")

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestViewSignal$")

			cmd.Env = append(os.Environ(), viewHelperEnv+"="+path, "TERM=xterm-256color")

			// The viewer needs a terminal for its input and output. A pipe
			// takes stderr, so the report carries no terminal styling.
			var stderr bytes.Buffer

			cmd.Stderr = &stderr

			ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
			require.NoError(t, err)

			t.Cleanup(func() {
				_ = ptmx.Close() //nolint:errcheck // Best effort.
			})

			// The viewer blocks once the terminal buffer fills, so the test
			// reads everything it draws. The first output shows that the
			// viewer has started.
			drawn := make(chan struct{})

			go func() {
				buf := make([]byte, 4096)

				n, err := ptmx.Read(buf)
				if n > 0 {
					close(drawn)
				}

				for err == nil {
					_, err = ptmx.Read(buf)
				}
			}()

			select {
			case <-drawn:
			case <-time.After(30 * time.Second):
				t.Fatal("the viewer drew nothing")
			}

			require.NoError(t, cmd.Process.Signal(tc.sig))

			done := make(chan error, 1)

			go func() {
				done <- cmd.Wait()
			}()

			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("%s did not end the viewer", name)
			}

			var exitErr *exec.ExitError

			// A canceled run is no fault of the document, so it exits with
			// the status of a failure.
			require.ErrorAs(t, err, &exitErr, "the viewer exited with status 0")
			assert.Equal(t, exitFailure, exitErr.ExitCode())
			assert.Contains(t, stderr.String(), context.Canceled.Error())
		})
	}
}
