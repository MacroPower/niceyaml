//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package schema_test

import (
	"io/fs"
	"path/filepath"
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
