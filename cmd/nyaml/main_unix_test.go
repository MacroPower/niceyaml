//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"SIGINT": {
			sig: syscall.SIGINT,
		},
		"SIGTERM": {
			sig: syscall.SIGTERM,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "a.yaml")
			require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))

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
