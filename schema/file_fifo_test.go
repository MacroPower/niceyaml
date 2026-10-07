//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package schema_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestFile_FIFO(t *testing.T) {
	t.Parallel()

	// Opening a FIFO blocks until a writer opens the other end, which no
	// one does here, so the loader must refuse the path before it opens it.
	path := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, syscall.Mkfifo(path, 0o600))

	errc := make(chan error, 1)

	go func() {
		_, err := schema.NewRegistry().Load(t.Context(), schema.File(path))
		errc <- err
	}()

	select {
	case err := <-errc:
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)

	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO")
	}
}

func TestFileFS_FIFO(t *testing.T) {
	t.Parallel()

	// A file system that implements fs.StatFS stats the path without
	// opening it, so the loader refuses a FIFO there before the open.
	tcs := map[string]struct {
		fsys func(t *testing.T, dir string) fs.FS
	}{
		"os.DirFS": {
			fsys: func(_ *testing.T, dir string) fs.FS {
				return os.DirFS(dir)
			},
		},
		"os.Root.FS": {
			fsys: func(t *testing.T, dir string) fs.FS {
				t.Helper()

				root, err := os.OpenRoot(dir)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, root.Close()) })

				return root.FS()
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "schema.json"), 0o600))

			reg := schema.NewRegistry()
			ref := schema.FileFS(tc.fsys(t, dir), "schema.json")
			errc := make(chan error, 1)

			go func() {
				_, err := reg.Load(t.Context(), ref)
				errc <- err
			}()

			select {
			case err := <-errc:
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorIs(t, err, fs.ErrInvalid)
				require.ErrorContains(t, err, "not a regular file")

			case <-time.After(5 * time.Second):
				t.Fatal("Load blocked on a FIFO")
			}
		})
	}
}

func TestFile_FIFOSwappedAfterStat(t *testing.T) {
	t.Parallel()

	// A FIFO that replaces the file between the loader's Stat and its open
	// must not block the open either. One goroutine swaps a regular file
	// and a FIFO at the path while the loop loads it.
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	regular := filepath.Join(dir, "regular.json")
	fifo := filepath.Join(dir, "fifo")

	require.NoError(t, os.WriteFile(path, []byte(`{"type":"object"}`), 0o600))

	var (
		stop atomic.Bool
		wg   sync.WaitGroup
	)

	wg.Go(func() {
		//nolint:errcheck // A failed step only skips one swap.
		for !stop.Load() {
			os.WriteFile(regular, []byte(`{"type":"object"}`), 0o600)
			os.Rename(regular, path)
			syscall.Mkfifo(fifo, 0o600)
			os.Rename(fifo, path)
		}
	})

	t.Cleanup(func() {
		stop.Store(true)
		wg.Wait()
	})

	for i := range 5000 {
		done := make(chan struct{})

		go func() {
			//nolint:errcheck // Either outcome is fine; only a block fails.
			schema.NewRegistry().Load(t.Context(), schema.File(path))
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: Load blocked on a FIFO swapped in after the Stat", i)
		}
	}
}
