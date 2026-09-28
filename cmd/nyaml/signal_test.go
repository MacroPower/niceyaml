//go:build unix

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

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
