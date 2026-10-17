package schema_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

		schemaData := []byte(`{"type": "object"}`)
		schemaPath := yamltest.WriteFile(t, t.TempDir(), "schema.json", string(schemaData))

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

		// Truncate grows the file without writing its bytes.
		path := filepath.Join(t.TempDir(), "schema.json")
		f, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, f.Truncate(schema.DefaultLoadLimit+1))
		require.NoError(t, f.Close())

		_, data, err := load(t, schema.File(path))
		requireLoadError(t, err, niceyaml.ErrLimit)
		require.ErrorContains(t, err, "read "+path+": file is larger than 10 MiB")
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
		requireLoadError(t, err, fs.ErrInvalid)
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

func TestFile_ErrorDrawsControlCharacters(t *testing.T) {
	t.Parallel()

	// A document names the path of its schema through a directive, so the
	// path holds whatever the author of the document wrote.
	const name = "a\x1b[2J\nb.json"

	fsys := yamltest.MapFS(map[string]string{"dir\x1b[2J/x.json": "{}"})

	// The LoadError quotes the name of the schema with Go escapes, and the
	// name of a file on disk is its file URL.
	tcs := map[string]struct {
		ref    schema.Ref
		err    error
		quoted string
		want   string
	}{
		"missing file on disk": {
			ref:    schema.File(filepath.Join(t.TempDir(), name)),
			quoted: `/a%1B%5B2J%0Ab.json": `,
			want:   "a␛[2J␊b.json",
		},
		"missing file in a file system": {
			ref:    schema.FileFS(fsys, name),
			err:    fs.ErrNotExist,
			quoted: `"a\x1b[2J\nb.json": `,
			want:   "read a␛[2J␊b.json: ",
		},
		"directory in a file system": {
			ref:    schema.FileFS(fsys, "dir\x1b[2J"),
			err:    fs.ErrInvalid,
			quoted: `"dir\x1b[2J": `,
			want:   "read dir␛[2J: ",
		},
		"absolute path in a file system": {
			ref:    schema.FileFS(fsys, "/"+name),
			err:    schema.ErrFileNotAllowed,
			quoted: `"/a\x1b[2J\nb.json": `,
			want:   "read /a␛[2J␊b.json: ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := schema.NewRegistry().Schema(t.Context(), tc.ref)
			require.ErrorIs(t, err, schema.ErrLoad)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}

			msg := err.Error()
			assert.NotContains(t, msg, "\x1b")
			assert.NotContains(t, msg, "\n")
			assert.Contains(t, msg, tc.quoted)
			assert.Contains(t, msg, tc.want)
		})
	}
}

// The cause keeps the path as the file system wrote it, for a caller that
// reads the path and not the message.
func TestFile_ErrorKeepsCause(t *testing.T) {
	t.Parallel()

	const name = "a\x1b[2Jb.json"

	_, err := schema.NewRegistry().Schema(t.Context(), schema.FileFS(yamltest.MapFS(nil), name))

	var pathErr *fs.PathError

	require.ErrorAs(t, err, &pathErr)
	assert.Equal(t, name, pathErr.Path)
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
	requireLoadError(t, err, fs.ErrInvalid)
	require.NotErrorIs(t, err, os.ErrNotExist)
}

func TestFile_URL(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
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
		"drive-letter path": {
			path: "C:/schemas/config.json",
			want: "file:///C:/schemas/config.json",
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
		// Off Windows, /C: is an ordinary directory at the root, and a
		// different file from the drive path C:/schemas/config.json, so the
		// two must not share a cache entry.
		"rooted directory named like a drive": {
			path: "/C:/schemas/config.json",
			want: "file:///C%3A/schemas/config.json",
		},
		"space in path": {
			path: "/schemas/my config.json",
			want: "file:///schemas/my%20config.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, fileURL(t, tc.path))
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

	path := yamltest.WriteFile(t, t.TempDir(), "a#b.json", data)
	dir := filepath.Dir(path)

	tcs := map[string]struct {
		ref  schema.Ref
		opts []schema.RegistryOption
	}{
		"on disk": {
			ref: schema.File(path),
		},
		"in a file system": {
			ref: schema.FileFS(yamltest.MapFS(map[string]string{"a#b.json": data}), "a#b.json"),
		},
		"under AllowRoot": {
			ref:  schema.File(path),
			opts: []schema.RegistryOption{withAccess(t, allowRoot(t, dir))},
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
	schemaPath := yamltest.WriteFile(t, t.TempDir(), "s.json", `{"type": "object"}`)

	elsewhere := t.TempDir()

	bundle := yamltest.MapFS(map[string]string{
		"configs/app.yaml":  yamltest.SchemaDirective("../schemas/root.json", "name: 5\n"),
		"schemas/root.json": `{"properties": {"name": {"$ref": "defs.json"}}}`,
		"schemas/defs.json": `{"type": "string"}`,
	})

	yamltest.RemoveWorkingDirectory(t)

	//nolint:paralleltest // See above.
	t.Run("a relative path builds a Ref", func(t *testing.T) {
		var ref schema.Ref

		require.NotPanics(t, func() { ref = schema.File("schemas/root.json") })
		assert.Equal(t, "file:///schemas/root.json", ref.Key())
	})

	//nolint:paralleltest // See above.
	t.Run("a relative path loads from a file system", func(t *testing.T) {
		reg := schema.NewRegistry(schema.WithResolvers(schema.FileFS(bundle, "schemas/root.json")))

		requireAccepts(t, reg, "name: cafe\n")

		requireRejects(t, reg, "name: 5\n", `$.name: expected "string", got "integer"`)
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
			"under AllowRoot": {
				opts: []schema.RegistryOption{withAccess(t, allowRoot(t, elsewhere))},
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
		rooted := loading(fileURL(t, "/schemas/root.json"), []byte(`{"type": "object"}`), nil)

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
	t.Run("a root on a relative name has no directory", func(t *testing.T) {
		root, err := os.OpenRoot(".")
		if err != nil {
			t.Skipf("open the removed working directory: %v", err)
		}

		defer root.Close() //nolint:errcheck // Best-effort close.

		access, err := schema.NewAccess(schema.AllowRoot(root))
		require.ErrorContains(t, err, "no absolute path for directory .")
		assert.Nil(t, access)
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
	yamltest.WriteFile(t, dir, "s.json", `{"type": "object"}`)
	t.Chdir(dir)

	ref := schema.File("s.json")

	t.Chdir(t.TempDir())

	_, data, err := load(t, ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "object"}`, string(data))
}
