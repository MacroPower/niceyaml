package schema_test

import (
	"cmp"
	"context"
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
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// fileOrURL is [schema.FileOrURL] for a reference the test knows names a
// schema.
func fileOrURL(t *testing.T, baseDir, ref string) schema.Ref {
	t.Helper()

	r, err := schema.FileOrURL(baseDir, ref)
	require.NoError(t, err)

	return r
}

// plainFileURL returns the file URL that spells path with no escapes, as
// a document or a schema writes one by hand.
func plainFileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func TestFileOrURL(t *testing.T) {
	t.Parallel()

	t.Run("file", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		// Each case writes the schema to file in a temporary directory, or
		// to schema.json when it names no file.
		// In baseDir and ref, "{dir}" stands for that directory, "{path}"
		// for the path of the file, and "{slash}" for that path with
		// forward slashes. The key is the URL of the file and then
		// fragment.
		tcs := map[string]struct {
			file     string
			baseDir  string
			ref      string
			fragment string
		}{
			"relative path": {
				baseDir: "{dir}",
				ref:     "schema.json",
			},
			"absolute path ignores baseDir": {
				baseDir: "/some/other/dir",
				ref:     "{path}",
			},
			"absolute path without base directory": {
				ref: "{path}",
			},
			// A file URL names a local path, so it ignores baseDir.
			"file URL": {
				baseDir: "/some/other/dir",
				ref:     "file://{path}",
			},
			// RFC 8089 allows file:/path alongside file:///path, and both
			// name the same absolute path rather than a path relative to
			// baseDir.
			"file URL without authority": {
				baseDir: "/some/other/dir",
				ref:     "file:{slash}",
			},
			"file URL scheme in upper case": {
				baseDir: "/some/other/dir",
				ref:     "FILE://{path}",
			},
			"file URL with percent-encoded path": {
				file:    "my schema.json",
				baseDir: "/some/other/dir",
				ref:     "file://" + filepath.Join("{dir}", "my%20schema.json"),
			},
			// The key keeps the fragment that names the subschema, and the
			// registry loads the whole file.
			"file URL with fragment": {
				baseDir:  "/some/other/dir",
				ref:      "file://{slash}#/definitions/Foo",
				fragment: "#/definitions/Foo",
			},
			// A colon is a legal character in a POSIX file name, and only a
			// separator behind it makes a drive letter, so this path joins
			// baseDir like any other relative one.
			"colon in a relative path": {
				file:    "a:b.json",
				baseDir: "{dir}",
				ref:     "a:b.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				schemaPath := yamltest.WriteFile(t, t.TempDir(), cmp.Or(tc.file, "schema.json"), string(schemaData))
				fill := strings.NewReplacer(
					"{dir}", filepath.Dir(schemaPath),
					"{path}", schemaPath,
					"{slash}", filepath.ToSlash(schemaPath),
				)

				url, data, err := load(t, fileOrURL(t, fill.Replace(tc.baseDir), fill.Replace(tc.ref)))
				require.NoError(t, err)
				assert.Equal(t, schemaData, data)
				assert.Equal(t, fileURL(t, schemaPath)+tc.fragment, url)
			})
		}
	})

	t.Run("relative path without base directory", func(t *testing.T) {
		t.Parallel()

		_, err := schema.FileOrURL("", "schema.json")
		require.ErrorIs(t, err, schema.ErrNoBaseDir)
		require.ErrorContains(t, err, `"schema.json"`)
	})

	t.Run("empty reference", func(t *testing.T) {
		t.Parallel()

		// An empty reference must not resolve to the base directory itself,
		// and FileOrURL reports the empty path rather than the missing
		// base directory.
		for _, baseDir := range []string{"/some/dir", ""} {
			_, err := schema.FileOrURL(baseDir, "")
			require.ErrorIs(t, err, schema.ErrEmptyPath, "baseDir %q", baseDir)
		}
	})

	t.Run("URL schema", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)

		// A URL ignores baseDir and needs none.
		tcs := map[string]struct {
			baseDir string
			scheme  string
		}{
			"with a base directory": {
				baseDir: "/some/dir",
				scheme:  "http",
			},
			"without a base directory": {
				scheme: "http",
			},
			// The resolved URL keys the registry cache, so the scheme comes
			// back in lower case however the reference spelled it.
			"scheme in upper case": {
				baseDir: "/some/dir",
				scheme:  "HTTP",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				schemaURL := serve(t, map[string]string{"/schema.json": string(schemaData)}) + "/schema.json"
				ref := tc.scheme + strings.TrimPrefix(schemaURL, "http")

				url, data, err := load(t, fileOrURL(t, tc.baseDir, ref))
				require.NoError(t, err)
				assert.Equal(t, schemaData, data)
				assert.Equal(t, schemaURL, url)
			})
		}
	})

	t.Run("rooted reference on any platform", func(t *testing.T) {
		t.Parallel()

		// A rooted reference never joins baseDir and needs none to resolve,
		// and none picks up the working directory.
		tcs := map[string]struct {
			ref  string
			want string
		}{
			// A file URL names its path on every platform, even where the
			// platform reads a rooted path carrying no volume as relative.
			"rooted file URL": {
				ref:  "file:///schemas/config.json",
				want: "file:///schemas/config.json",
			},
			// Windows reads a rooted path that carries no volume as relative,
			// so there the path would otherwise land under baseDir, and an
			// empty baseDir would report ErrNoBaseDir. The key names the path
			// as File does, on the current drive on Windows.
			"rooted path": {
				ref:  "/schemas/config.json",
				want: fileURL(t, "/schemas/config.json"),
			},
			// A drive-letter path is absolute on Windows and names nothing a
			// POSIX base directory can resolve, so FileOrURL must keep the
			// reference intact rather than rewrite it against baseDir.
			"windows file URL": {
				ref:  "file:///C:/schemas/config.json",
				want: "file:///C:/schemas/config.json",
			},
			// A file URL keys the cleaned path, as File keys a drive-letter
			// path, so both spellings share one registry entry.
			"windows file URL with dot segments": {
				ref:  "file:///C:/schemas/../schemas/config.json",
				want: "file:///C:/schemas/config.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				url, _, err := load(t, fileOrURL(t, "/configs", tc.ref))
				require.Error(t, err)
				assert.Equal(t, tc.want, url)

				_, err = schema.FileOrURL("", tc.ref)
				require.NoError(t, err)
			})
		}
	})

	t.Run("escaped colon in a file URL", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("an escaped colon names the drive on Windows")
		}

		// The escaped colon names the directory /C: at the root, the form
		// File keys it by, so the read looks for the file there rather
		// than refusing a drive path.
		url, _, err := load(t, fileOrURL(t, "/configs", "file:///C%3A/schemas/config.json"))
		assert.Equal(t, "file:///C%3A/schemas/config.json", url)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /C:/schemas/config.json")
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		_, _, err := load(t, fileOrURL(t, "/some/dir", "nonexistent.json"))
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read /some/dir/nonexistent.json")
	})
}

func TestFileOrURL_BaseDir(t *testing.T) {
	t.Parallel()

	// A path that starts with a drive letter is absolute wherever a
	// program reads it, so it never joins baseDir, needs none, and names
	// the schema File names for it. A colon anywhere else is part of a file
	// name, and the path joins baseDir like any other relative one. A file
	// URL with a remote host, or with no path, names no local path, so the
	// whole reference is a relative file path too.
	baseDir := t.TempDir()

	tcs := map[string]struct {
		ref   string
		drive bool
	}{
		"upper-case drive":            {ref: "C:/schemas/config.json", drive: true},
		"lower-case drive":            {ref: "c:/schemas/config.json", drive: true},
		"drive alone":                 {ref: "C:", drive: true},
		"backslash separator":         {ref: `C:\schemas\config.json`, drive: true},
		"digit before the colon":      {ref: "1:/schemas/config.json"},
		"longer first segment":        {ref: "ab:/schemas/config.json"},
		"single letter":               {ref: "C"},
		"file URL with a remote host": {ref: "file://host/C:/schemas/config.json"},
		"file URL with an empty path": {ref: "file://"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := fileURL(t, filepath.Join(baseDir, tc.ref))
			if tc.drive {
				want = fileURL(t, tc.ref)
			}

			assert.Equal(t, want, fileOrURL(t, baseDir, tc.ref).Key())

			_, err := schema.FileOrURL("", tc.ref)
			if tc.drive {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, schema.ErrNoBaseDir)
			}
		})
	}
}

func TestFileOrURL_FileURL(t *testing.T) {
	t.Parallel()

	// A file URL names the schema File names for the local path the URL
	// holds. A Windows-shaped URL holds C:/schemas/config.json, with the
	// drive letter first, so filepath.IsAbs reports the path absolute on
	// Windows.
	//
	// Off Windows, a colon escaped as %3A names a directory at the root,
	// which keeps its leading slash. Windows has no such directory, so
	// there the escaped colon names the drive.
	escapedRoot := "/C:"
	if runtime.GOOS == "windows" {
		escapedRoot = "C:"
	}

	tcs := map[string]struct {
		ref  string
		path string
	}{
		"unix path": {
			ref:  "file:///srv/schemas/config.json",
			path: "/srv/schemas/config.json",
		},
		"windows drive letter": {
			ref:  "file:///C:/schemas/config.json",
			path: "C:/schemas/config.json",
		},
		"windows drive letter in lower case": {
			ref:  "file:///c:/schemas/config.json",
			path: "c:/schemas/config.json",
		},
		"windows drive letter with localhost": {
			ref:  "file://localhost/C:/schemas/config.json",
			path: "C:/schemas/config.json",
		},
		"drive letter alone": {
			ref:  "file:///C:",
			path: "C:",
		},
		"percent-encoded drive letter": {
			ref:  "file:///%43:/schemas/config.json",
			path: "C:/schemas/config.json",
		},
		"escaped colon": {
			ref:  "file:///C%3A/schemas/config.json",
			path: escapedRoot + "/schemas/config.json",
		},
		"escaped colon beside a literal non-ascii letter": {
			ref:  "file:///C%3A/sch\u00e9mas/config.json",
			path: escapedRoot + "/sch\u00e9mas/config.json",
		},
		"escaped colon beside a literal pipe": {
			ref:  "file:///C%3A/a|b/config.json",
			path: escapedRoot + "/a|b/config.json",
		},
		"colon after a digit is not a drive": {
			ref:  "file:///1:/schemas/config.json",
			path: "/1:/schemas/config.json",
		},
		"colon in a longer first segment is not a drive": {
			ref:  "file:///ab:/schemas/config.json",
			path: "/ab:/schemas/config.json",
		},
		"percent-encoded path": {
			ref:  "file:///srv/my%20schemas/config.json",
			path: "/srv/my schemas/config.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The path a file URL names needs no base directory.
			got := fileOrURL(t, "", tc.ref)
			assert.Equal(t, fileURL(t, filepath.FromSlash(tc.path)), got.Key())
		})
	}
}

func TestRefBeside(t *testing.T) {
	t.Parallel()

	const schemaData = `{"type": "object", "required": ["kind"]}`

	bundle := yamltest.MapFS(map[string]string{
		"configs/schema.json": schemaData,
	})

	dir := t.TempDir()
	yamltest.WriteFile(t, dir, "schema.json", schemaData)

	inBundle := yamltest.FirstDocument(t, "kind: App\n",
		niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(bundle))
	onDisk := yamltest.FirstDocumentWithPath(t, "kind: App\n", filepath.Join(dir, "app.yaml"))
	noPath := yamltest.FirstDocument(t, "kind: App\n")

	reg := schema.NewRegistry()

	t.Run("a relative path names a file beside a document in a file system", func(t *testing.T) {
		t.Parallel()

		ref, err := schema.RefBeside(inBundle, "./schema.json")
		require.NoError(t, err)
		assert.Equal(t, "file:///configs/schema.json", ref.Key())

		data, err := reg.Load(t.Context(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, schemaData, string(data))
	})

	t.Run("a relative path names a file beside a document on disk", func(t *testing.T) {
		t.Parallel()

		ref, err := schema.RefBeside(onDisk, "schema.json")
		require.NoError(t, err)
		assert.Equal(t, fileURL(t, filepath.Join(dir, "schema.json")), ref.Key())

		data, err := reg.Load(t.Context(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, schemaData, string(data))
	})

	t.Run("a path through the parent stays in the file system", func(t *testing.T) {
		t.Parallel()

		ref, err := schema.RefBeside(inBundle, "../configs/schema.json")
		require.NoError(t, err)
		assert.Equal(t, "file:///configs/schema.json", ref.Key())

		out, err := schema.RefBeside(inBundle, "../../schema.json")
		require.NoError(t, err)

		_, err = reg.Load(t.Context(), out)
		requireLoadError(t, err, fs.ErrInvalid)
	})

	t.Run("a URL names a URL wherever the document lives", func(t *testing.T) {
		t.Parallel()

		for _, doc := range []*niceyaml.Node{inBundle, onDisk, noPath} {
			ref, err := schema.RefBeside(doc, "https://example.com/schema.json")
			require.NoError(t, err)
			assert.Equal(t, "https://example.com/schema.json", ref.Key())
		}
	})

	t.Run("an absolute path names no file in a file system", func(t *testing.T) {
		t.Parallel()

		abs := filepath.Join(dir, "schema.json")

		for _, written := range []string{abs, plainFileURL(abs)} {
			ref, err := schema.RefBeside(inBundle, written)
			require.NoError(t, err)

			_, err = reg.Load(t.Context(), ref)
			requireLoadError(t, err, fs.ErrInvalid)

			// The same reference beside a document on disk reads the file.
			ref, err = schema.RefBeside(onDisk, written)
			require.NoError(t, err)

			_, err = reg.Load(t.Context(), ref)
			require.NoError(t, err)
		}
	})

	t.Run("the origin of a layered value names a file beside its layer", func(t *testing.T) {
		t.Parallel()

		layered := yamltest.MapFS(map[string]string{
			"configs/prod/schema.json": schemaData,
		})

		base := niceyaml.NewSourceFromString("kind: App\n",
			niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(layered))
		prod := niceyaml.NewSourceFromString("schema: ./schema.json\n",
			niceyaml.WithFilePath("configs/prod/app.yaml"), niceyaml.WithFS(layered))

		doc := yamltest.Document(t, yamltest.Layers(t, base, prod))

		field := yamltest.At(t, doc, paths.Doc().Child("schema"))

		// The Node of the merged document has the file path of the lowest
		// layer.
		ref, err := schema.RefBeside(field, "./schema.json")
		require.NoError(t, err)
		assert.Equal(t, "file:///configs/schema.json", ref.Key())

		origin, err := field.Origin()
		require.NoError(t, err)

		ref, err = schema.RefBeside(origin, "./schema.json")
		require.NoError(t, err)
		assert.Equal(t, "file:///configs/prod/schema.json", ref.Key())

		data, err := reg.Load(t.Context(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, schemaData, string(data))
	})

	t.Run("a relative path needs the path of the document", func(t *testing.T) {
		t.Parallel()

		_, err := schema.RefBeside(noPath, "./schema.json")
		require.ErrorIs(t, err, schema.ErrNoBaseDir)

		// A file system alone gives the document no directory either.
		orphan := yamltest.FirstDocument(t, "kind: App\n", niceyaml.WithFS(bundle))

		_, err = schema.RefBeside(orphan, "./schema.json")
		require.ErrorIs(t, err, schema.ErrNoBaseDir)
	})

	t.Run("a relative path beside a relative file path needs a file system", func(t *testing.T) {
		t.Parallel()

		relative := yamltest.FirstDocumentWithPath(t, "kind: App\n", "configs/app.yaml")

		tcs := map[string]struct {
			ref string
		}{
			"beside the document": {ref: "./schema.json"},
			"bare name":           {ref: "schema.json"},
			"through the parent":  {ref: "../outside/schema.json"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := schema.RefBeside(relative, tc.ref)
				require.ErrorIs(t, err, schema.ErrNoBaseDir)
				assert.Equal(t, `relative schema path has no base directory: "`+tc.ref+
					`": the document path "configs/app.yaml" is relative and names no file system`, err.Error())
			})
		}
	})

	t.Run("a URL or an absolute path beside a relative file path names its target", func(t *testing.T) {
		t.Parallel()

		relative := yamltest.FirstDocumentWithPath(t, "kind: App\n", "configs/app.yaml")

		ref, err := schema.RefBeside(relative, "https://example.com/schema.json")
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/schema.json", ref.Key())

		abs := filepath.Join(dir, "schema.json")

		ref, err = schema.RefBeside(relative, abs)
		require.NoError(t, err)
		assert.Equal(t, schema.File(abs).Key(), ref.Key())

		data, err := reg.Load(t.Context(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, schemaData, string(data))
	})

	t.Run("an empty reference names no schema", func(t *testing.T) {
		t.Parallel()

		_, err := schema.RefBeside(inBundle, "")
		require.ErrorIs(t, err, schema.ErrEmptyPath)
	})

	t.Run("a resolver returns the result as it is", func(t *testing.T) {
		t.Parallel()

		beside := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			return schema.RefBeside(doc, "schema.json")
		})

		byName := schema.NewRegistry(schema.WithResolvers(beside))

		require.NoError(t, byName.Check(t.Context(), inBundle))
		require.NoError(t, byName.Check(t.Context(), onDisk))

		invalid := yamltest.FirstDocument(t, "name: x\n",
			niceyaml.WithFilePath("configs/other.yaml"), niceyaml.WithFS(bundle))

		err := byName.Check(t.Context(), invalid)
		require.ErrorContains(t, err, `missing required property "kind"`)
	})
}

// A relative file path with no file system names a file on disk only
// against the working directory, which the author of the document does
// not pick. A relative reference beside such a path resolves nowhere,
// even when the working directory holds the file it names.
// [niceyaml.NewSourceFromFile] makes its path absolute, so its documents
// still resolve. The test changes the process's working directory, so it
// does not run in parallel.
//
//nolint:paralleltest // The test changes the working directory.
func TestRefBeside_WorkingDirectory(t *testing.T) {
	wd := t.TempDir()
	path := filepath.Join("configs", "app.yaml")

	yamltest.WriteFiles(t, wd, map[string]string{
		"configs/schema.json": `{"properties": {"name": {"type": "string"}}}`,
		"configs/app.yaml":    yamltest.SchemaDirective("./schema.json", "name: 5\n"),
	})

	t.Chdir(wd)

	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	fromBytes := niceyaml.NewSourceFromBytes(data, niceyaml.WithFilePath(path))

	err = fromBytes.ValidateDocuments(t.Context(), reg)
	require.ErrorIs(t, err, schema.ErrNoBaseDir)

	fromFile, err := niceyaml.NewSourceFromFile(path)
	require.NoError(t, err)

	err = fromFile.ValidateDocuments(t.Context(), reg)
	require.ErrorContains(t, err, `expected "string", got "integer"`)
}
