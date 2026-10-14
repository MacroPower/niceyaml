package yamltest

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// MapFS creates an [fstest.MapFS] that holds files, a map from
// slash-separated path to the text of the file at that path.
func MapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}

	return fsys
}

// WriteFile writes content to the file at the slash-separated path name
// under dir, and returns the path of the file. It creates each parent
// directory that the file lacks. The test fails when a write fails.
func WriteFile(tb testing.TB, dir, name, content string) string {
	tb.Helper()

	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(tb, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// WriteFiles writes files, a map from slash-separated path to text, under
// dir through [WriteFile].
func WriteFiles(tb testing.TB, dir string, files map[string]string) {
	tb.Helper()

	for name, text := range files {
		WriteFile(tb, dir, name, text)
	}
}

// ReadFile returns the text of the file at path. The test fails when the
// read fails.
func ReadFile(tb testing.TB, path string) string {
	tb.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // A test reads the files it names.
	require.NoError(tb, err)

	return string(data)
}

// SymlinkOrSkip creates link as a symbolic link to target through
// [os.Symlink]. It skips the test when the call fails, as it does on a
// Windows account that lacks the privilege to create a link.
func SymlinkOrSkip(tb testing.TB, target, link string) {
	tb.Helper()

	err := os.Symlink(target, link)
	if err != nil {
		tb.Skipf("create symlink %s: %v", link, err)
	}
}

// RemoveWorkingDirectory changes the working directory of the test to a
// new directory and removes that directory, so the process runs with no
// working directory. It skips the test when the platform refuses to
// remove the directory or when [os.Getwd] still names it.
//
// The change goes through [testing.TB.Chdir], which restores the working
// directory when the test ends and panics in a parallel test.
func RemoveWorkingDirectory(tb testing.TB) {
	tb.Helper()

	gone := filepath.Join(tb.TempDir(), "gone")
	require.NoError(tb, os.Mkdir(gone, 0o700))
	tb.Chdir(gone)

	err := os.Remove(gone)
	if err != nil {
		tb.Skipf("remove the working directory: %v", err)
	}

	_, err = os.Getwd()
	if err == nil {
		tb.Skip("the platform still names the removed working directory")
	}
}
