package schema_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// fileURL returns the key that [schema.File] names for path, which is its
// file URL.
func fileURL(t *testing.T, path string) string {
	t.Helper()

	return schema.File(path).Key()
}

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

		// Resolve names the file without touching it, and only Load reads it.
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
	require.ErrorIs(t, err, schema.ErrLoad)
	require.ErrorIs(t, err, fs.ErrInvalid)
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

// A '#' in a file name is part of the name. The key escapes it, so it
// opens no fragment, and every registry reads the file.
func TestFile_HashInName(t *testing.T) {
	t.Parallel()

	const data = `{"type": "string"}`

	dir := t.TempDir()
	path := filepath.Join(dir, "a#b.json")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	tcs := map[string]struct {
		ref  schema.Ref
		opts []schema.RegistryOption
	}{
		"on disk": {
			ref: schema.File(path),
		},
		"in a file system": {
			ref: schema.FileFS(fstest.MapFS{"a#b.json": &fstest.MapFile{Data: []byte(data)}}, "a#b.json"),
		},
		"under WithFSAt": {
			ref:  schema.File(path),
			opts: []schema.RegistryOption{schema.WithFSAt(dir, os.DirFS(dir))},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ref := tc.ref
			assert.True(t, strings.HasSuffix(ref.Key(), "/a%23b.json"), ref.Key())

			s, err := schema.NewRegistry(tc.opts...).Schema(t.Context(), ref)
			require.NoError(t, err)

			require.NoError(t, s.ValidateValue(t.Context(), "x"))
			require.Error(t, s.ValidateValue(t.Context(), 5))
		})
	}
}

// File has no working directory to make a relative path absolute against
// once the directory is gone, and it builds a Ref all the same. A
// registry under WithFS reads the path as written, so the Ref loads
// there and nowhere else. The test removes the process's working
// directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFile_NoWorkingDirectory(t *testing.T) {
	schemaPath := filepath.Join(t.TempDir(), "s.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

	elsewhere := t.TempDir()

	bundle := fstest.MapFS{
		"configs/app.yaml": &fstest.MapFile{
			Data: []byte("# yaml-language-server: $schema=../schemas/root.json\nname: 5\n"),
		},
		"schemas/root.json": &fstest.MapFile{Data: []byte(`{"properties": {"name": {"$ref": "defs.json"}}}`)},
		"schemas/defs.json": &fstest.MapFile{Data: []byte(`{"type": "string"}`)},
	}

	gone := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.Mkdir(gone, 0o700))
	t.Chdir(gone)

	err := os.Remove(gone)
	if err != nil {
		t.Skipf("remove the working directory: %v", err)
	}

	_, err = os.Getwd()
	if err == nil {
		t.Skip("the platform still names the removed working directory")
	}

	//nolint:paralleltest // See above.
	t.Run("a relative path builds a Ref", func(t *testing.T) {
		var ref schema.Ref

		require.NotPanics(t, func() { ref = schema.File("schemas/root.json") })
		assert.Equal(t, "file:///schemas/root.json", ref.Key())
	})

	//nolint:paralleltest // See above.
	t.Run("a relative path loads from a file system", func(t *testing.T) {
		reg := schema.NewRegistry(schema.WithResolvers(schema.FileFS(bundle, "schemas/root.json")))

		require.NoError(t, reg.Check(t.Context(), yamltest.FirstDocument(t, "name: cafe\n")))

		err := reg.Check(t.Context(), yamltest.FirstDocument(t, "name: 5\n"))
		require.ErrorContains(t, err, `$.name: expected "string", got "integer"`)
	})

	//nolint:paralleltest // See above.
	t.Run("a directive resolves in the file system of its document", func(t *testing.T) {
		source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
		require.NoError(t, err)

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		err = source.ValidateDocuments(t.Context(), reg)
		require.ErrorContains(t, err, `$.name: expected "string", got "integer"`)
	})

	//nolint:paralleltest // See above.
	t.Run("a relative path loads nowhere else", func(t *testing.T) {
		tcs := map[string]struct {
			opts []schema.RegistryOption
		}{
			"on disk": {},
			"under WithFSAt": {
				opts: []schema.RegistryOption{schema.WithFSAt(elsewhere, bundle)},
			},
		}

		for name, tc := range tcs {
			//nolint:paralleltest // See above.
			t.Run(name, func(t *testing.T) {
				reg := schema.NewRegistry(tc.opts...)
				ref := schema.File("schemas/root.json")

				_, err := reg.Load(t.Context(), ref)
				require.ErrorIs(t, err, schema.ErrLoad)
				require.ErrorContains(t, err, "read schemas/root.json: no absolute path")

				_, err = reg.Schema(t.Context(), ref)
				require.ErrorIs(t, err, schema.ErrLoad)
			})
		}
	})

	//nolint:paralleltest // See above.
	t.Run("a relative path never takes the schema of the path under the root", func(t *testing.T) {
		// The two Refs share a key, and only the absolute one names a file.
		rooted := schema.Loadable(schema.File("/schemas/root.json").Key(), func(context.Context) ([]byte, error) {
			return []byte(`{"type": "object"}`), nil
		})

		reg := schema.NewRegistry()

		_, err := reg.Schema(t.Context(), rooted)
		require.NoError(t, err)

		_, err = reg.Schema(t.Context(), schema.File("schemas/root.json"))
		require.ErrorIs(t, err, schema.ErrLoad)
	})

	//nolint:paralleltest // See above.
	t.Run("an absolute path loads", func(t *testing.T) {
		_, data, err := load(t, schema.File(schemaPath))
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
	})

	//nolint:paralleltest // See above.
	t.Run("a relative directory under WithFSAt names none", func(t *testing.T) {
		reg := schema.NewRegistry(schema.WithFSAt(".", bundle))

		_, err := reg.Load(t.Context(), schema.File(schemaPath))
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorContains(t, err, "no absolute path for directory .")
	})
}

// A read from the working directory uses the path File made absolute to
// build the key, so the bytes under a key stay the same whatever the
// working directory is at the time of the read. The test changes the
// process's working directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestFile_ReadsAfterChdir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s.json"), []byte(`{"type": "object"}`), 0o600))

	t.Chdir(dir)

	ref := schema.File("s.json")

	t.Chdir(t.TempDir())

	_, data, err := load(t, ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "object"}`, string(data))
}
