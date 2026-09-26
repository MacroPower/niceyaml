package schema_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestFile(t *testing.T) {
	t.Parallel()

	t.Run("existing file", func(t *testing.T) {
		t.Parallel()

		// Create temp file with schema.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		_, data, err := load(t, schema.File(schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		// Resolve names the file without touching it; only Load reads it.
		url, _, err := load(t, schema.File("/nonexistent/path/schema.json"))
		assert.Equal(t, "file:///nonexistent/path/schema.json", url)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /nonexistent/path/schema.json")
	})

	t.Run("oversize file", func(t *testing.T) {
		t.Parallel()

		const maxSchemaSize = 10 * 1024 * 1024 // Must match httpfetch.MaxSize.

		// Truncate grows the file without writing its bytes.
		path := filepath.Join(t.TempDir(), "schema.json")
		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, f.Truncate(maxSchemaSize+1))
		require.NoError(t, f.Close())

		_, data, err := load(t, schema.File(path))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "exceeds")
		assert.Nil(t, data)
	})

	t.Run("device file", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("os.DevNull is a reserved device name on Windows")
		}

		// The null device reads as empty, but a device such as /dev/zero
		// never ends, so the loader refuses every file that is not regular.
		_, data, err := load(t, schema.File(os.DevNull))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)
		assert.Nil(t, data)
	})

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()

		// An empty path must not resolve to the working directory.
		assert.PanicsWithValue(t, "schema.File: "+schema.ErrEmptyPath.Error(), func() {
			schema.File("")
		})
	})
}

func TestFile_DriveLetter(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("a drive letter names a drive on Windows")
	}

	// Here a drive letter is an ordinary directory name, so a read would
	// resolve "C:" against the working directory while the key names the
	// drive. The loader refuses rather than read a file per directory, so
	// the failure is not a missing file.
	key, data, err := load(t, schema.File("C:/schemas/config.json"))
	assert.Equal(t, "file:///C:/schemas/config.json", key)
	assert.Nil(t, data)
	require.ErrorContains(t, err, "read C:/schemas/config.json")
	require.NotErrorIs(t, err, os.ErrNotExist)
}

func TestFile_DirectoryNamedLikeADrive(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("a rooted path has no drive on Windows")
	}

	// Here /C: is an ordinary directory at the root, and a different file
	// from the drive path C:/x, so the two must not share a cache entry.
	assert.NotEqual(t, schema.File("C:/x").Key(), schema.File("/C:/x").Key())
}

func TestFile_URL(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		want string
	}{
		"absolute path": {
			path: "/schemas/config.json",
			want: "file:///schemas/config.json",
		},
		"unclean absolute path": {
			path: "/schemas/../schemas/./config.json",
			want: "file:///schemas/config.json",
		},
		"unclean drive-letter path": {
			path: "C:/schemas/../schemas/./config.json",
			want: "file:///C:/schemas/config.json",
		},
		"dot-dot above the drive root": {
			path: "C:/../schemas/config.json",
			want: "file:///C:/schemas/config.json",
		},
		"backslash drive-letter path": {
			path: `C:\schemas\config.json`,
			want: "file:///C:/schemas/config.json",
		},
		"rooted directory named like a drive": {
			path: "/C:/schemas/config.json",
			want: "file:///C%3A/schemas/config.json",
		},
		"space in path": {
			path: "/schemas/my config.json",
			want: "file:///schemas/my%20config.json",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, fileURL(t, tt.path))
		})
	}
}

func TestFile_RelativePath(t *testing.T) {
	t.Parallel()

	// A relative path names the same schema as the absolute path it resolves
	// to against the working directory.
	wd, err := os.Getwd()
	require.NoError(t, err)

	want := fileURL(t, filepath.Join(wd, "schemas", "config.json"))

	for _, path := range []string{"schemas/config.json", "./schemas/config.json"} {
		assert.Equal(t, want, fileURL(t, path), path)
	}
}

func TestReadFile_ReadsTheAbsolutePath(t *testing.T) {
	t.Parallel()

	// A read from the working directory uses the path File made absolute
	// to build the key, not the relative path made absolute again at read
	// time, so the bytes under a key do not depend on the working
	// directory at the time of the read.
	dir := t.TempDir()
	abs := filepath.Join(dir, "s.json")
	require.NoError(t, os.WriteFile(abs, []byte(`{"type": "object"}`), 0o600))

	data, err := schema.ReadFile(nil, "elsewhere/s.json", abs, "")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "object"}`, string(data))
}

func TestReadFile_FSReadsAgainstTheRecordedDirectory(t *testing.T) {
	t.Parallel()

	// The root of the file system stands for the working directory File
	// recorded, not the one at the time of the read. A $ref that resolves
	// to an absolute path under the recorded directory keeps reading after
	// the program changes directory.
	base := filepath.Join(t.TempDir(), "recorded")

	cwd, err := os.Getwd()
	require.NoError(t, err)

	fsys := fstest.MapFS{
		"schemas/defs.json": &fstest.MapFile{Data: []byte(`{"type": "string"}`)},
	}

	tests := map[string]struct {
		err  error
		name string
		wd   string
	}{
		"under the recorded directory": {
			name: filepath.Join(base, "schemas", "defs.json"),
			wd:   base,
		},
		"outside the recorded directory": {
			name: filepath.Join(filepath.Dir(base), "schemas", "defs.json"),
			wd:   base,
			err:  fs.ErrInvalid,
		},
		"no recorded directory": {
			name: filepath.Join(cwd, "schemas", "defs.json"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := schema.ReadFile(fsys, tt.name, tt.name, tt.wd)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				assert.Nil(t, data)

				return
			}

			require.NoError(t, err)
			assert.JSONEq(t, `{"type": "string"}`, string(data))
		})
	}
}
