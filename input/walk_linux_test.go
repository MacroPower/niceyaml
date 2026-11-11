package input_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"go.jacobcolvin.com/niceyaml/input"
)

// TestPath_ExchangedDirectory reads a directory while another goroutine
// keeps exchanging its subdirectory with a directory outside it. The
// subdirectory always holds a file, so each read must either take in that
// file or fail. A read must never drop the file without a problem.
func TestPath_ExchangedDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{files: map[string]string{
		"config/a.yaml":     cafeDoc,
		"config/sub/s.yaml": hoursV1Doc,
		"alt/s.yaml":        hoursV1Doc,
	}}.write(t, dir)

	sub := filepath.Join(dir, "config", "sub")
	alt := filepath.Join(dir, "alt")

	err := unix.Renameat2(unix.AT_FDCWD, sub, unix.AT_FDCWD, alt, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		t.Skip("the file system does not exchange two directories")
	}

	require.NoError(t, err)

	stop := make(chan struct{})

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			assert.NoError(t, unix.Renameat2(unix.AT_FDCWD, sub, unix.AT_FDCWD, alt, unix.RENAME_EXCHANGE))
		}
	})

	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})

	in := input.Path(filepath.Join(dir, "config"), input.WithRecursive(true))
	want := filepath.ToSlash(filepath.Join(dir, "config", "sub", "s.yaml"))

	for range 500 {
		files, err := readAll(t, in)
		require.NotNil(t, files)

		if err == nil {
			require.Contains(t, readNames(files), want)
		}
	}
}

// volumeRootEnv names the directory that [TestPath_VolumeRoot] reads as
// the volume root, in the process that it starts.
const volumeRootEnv = "NICEYAML_TEST_VOLUME_ROOT"

// noChrootExit is the exit status of the process that
// [TestPath_VolumeRoot] starts when the system refuses it the change of
// root. Some systems create the user namespace but give its process no
// capability, as AppArmor does on Ubuntu 23.10 and later.
const noChrootExit = 3

// TestPath_VolumeRoot reads a directory that is the volume root, through
// links that name it by an absolute path and by "..". The test starts
// itself again in a user namespace, which changes its root to the
// directory before the read.
func TestPath_VolumeRoot(t *testing.T) {
	t.Parallel()

	if dir := os.Getenv(volumeRootEnv); dir != "" {
		err := unix.Chroot(dir)
		if errors.Is(err, unix.EPERM) {
			fmt.Println("the system refuses the change of root:", err)
			os.Exit(noChrootExit)
		}

		require.NoError(t, err)

		files, err := readAll(t, input.Path("/"))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"/a.yaml", "/c.yaml"}, readNames(files))

		return
	}

	dir := t.TempDir()
	tree{
		files: map[string]string{
			"sub/b.yaml": cafeDoc,
			"sub/d.yaml": hoursV1Doc,
		},
		links: map[string]string{
			"a.yaml": "/sub/b.yaml",
			"c.yaml": "../sub/d.yaml",
		},
	}.write(t, dir)

	exe, err := os.Executable()
	require.NoError(t, err)

	var out bytes.Buffer

	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestPath_VolumeRoot$", "-test.v")

	cmd.Env = append(os.Environ(), volumeRootEnv+"="+dir)
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}

	err = cmd.Start()
	if err != nil {
		t.Skipf("the system refuses a user namespace: %v", err)
	}

	err = cmd.Wait()

	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if ok && exitErr.ExitCode() == noChrootExit {
		t.Skipf("the system refuses a change of root in a user namespace: %s", out.String())
	}

	require.NoError(t, err, out.String())
}
