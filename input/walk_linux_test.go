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

	"go.jacobcolvin.com/niceyaml"
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

// TestPath_ExchangedEntry reads a directory while another goroutine keeps
// exchanging an entry, or a directory on its path, with another entry. The
// entry holds hoursV1Doc, or it leads to a file that the walk must not read
// under its name, or to a directory that the walk skips. A read must never
// take in another text under the name of the entry.
//
// The checks that these reads exercise catch an entry that changes
// between two steps of the walk, so only a race reaches them. The test
// pins what each check prevents, a read of another file or a read of a
// directory. It also pins that the walk skips a directory that became a
// link to a directory inside the walk, because a retry reports that link.
// It cannot tell a retry apart from the skip of a link that leads out of
// the walk, so it does not pin that skip, or how many times the walk
// tries an entry.
func TestPath_ExchangedEntry(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree tree
		opts []input.Option
		// The two paths that the goroutine exchanges, below the directory of
		// the test.
		exchange [2]string
		// The entry that the read names, below the directory of the test.
		entry string
		// The reason in each problem of a read. The walk can find a change
		// in each of its tries, so a read can fail even when the entry leads
		// to no file that the walk refuses. A case without a reason lets a
		// read fail for any reason, because its entry also leads to such a
		// file.
		reason string
		// The entry whose problems carry the reason, below the directory of
		// the test. A case without it holds every problem to the reason.
		problem string
	}{
		"file and a link to a file the patterns do not select": {
			tree: tree{
				files: map[string]string{"config/b.yaml": hoursV1Doc, "config/secret.txt": cafeDoc},
				links: map[string]string{"config/.swap": "secret.txt"},
			},
			exchange: [2]string{"config/b.yaml", "config/.swap"},
			entry:    "config/b.yaml",
		},
		"file and a directory": {
			tree: tree{
				files: map[string]string{"config/b.yaml": hoursV1Doc},
				dirs:  []string{"config/.swap"},
			},
			exchange: [2]string{"config/b.yaml", "config/.swap"},
			entry:    "config/b.yaml",
			reason:   "changed during the read",
		},
		"file outside the directory and a directory": {
			tree: tree{
				files: map[string]string{"outside/b.yaml": hoursV1Doc},
				links: map[string]string{"config/b.yaml": "../outside/b.yaml"},
				dirs:  []string{"outside/swap"},
			},
			opts:     []input.Option{input.WithOutsideLinks(true)},
			exchange: [2]string{"outside/b.yaml", "outside/swap"},
			entry:    "config/b.yaml",
			reason:   "changed during the read",
		},
		"directory and a link to a directory the patterns do not select": {
			tree: tree{
				files: map[string]string{"config/sub/b.yaml": hoursV1Doc, "config/other/b.yaml": cafeDoc},
				links: map[string]string{"config/.swap": "other"},
			},
			opts: []input.Option{
				input.WithRecursive(true),
				input.WithInclude("a.yaml", "sub/*.yaml"),
			},
			exchange: [2]string{"config/sub", "config/.swap"},
			entry:    "config/sub/b.yaml",
			reason:   "changed during the read",
			problem:  "config/sub",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tree{files: map[string]string{"config/a.yaml": cafeDoc}}.write(t, dir)
			tc.tree.write(t, dir)

			a := filepath.Join(dir, filepath.FromSlash(tc.exchange[0]))
			b := filepath.Join(dir, filepath.FromSlash(tc.exchange[1]))

			err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
			if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
				t.Skip("the file system does not exchange two entries")
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

					assert.NoError(t, unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE))
				}
			})

			t.Cleanup(func() {
				close(stop)
				wg.Wait()
			})

			in := input.Path(filepath.Join(dir, "config"), tc.opts...)
			entry := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(tc.entry)))
			problem := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(tc.problem)))

			for range 1000 {
				files, err := readAll(t, in)
				require.NotNil(t, files)

				if tc.reason != "" && err != nil {
					for _, f := range files {
						if f.Err != nil && (tc.problem == "" || f.Name == problem) {
							require.Equal(t, f.Name+": "+tc.reason, niceyaml.FormatError(f.Err))
						}
					}
				}

				text, ok := readTexts(files)[entry]
				if ok {
					require.Equal(t, hoursV1Doc, text)
				}
			}
		})
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
