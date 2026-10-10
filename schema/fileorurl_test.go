package schema_test

import (
	"cmp"
	"context"
	"io/fs"
	"net/http"
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

				schemaPath := writeSchema(t, cmp.Or(tc.file, "schema.json"), schemaData)
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

	t.Run("URL with custom client", func(t *testing.T) {
		t.Parallel()

		base := serve(t, map[string]string{"/schema.json": `{"type": "object"}`})

		client, requests := yamltest.CountingClient(http.DefaultTransport)

		// The registry fetches the URL with its own client.
		err := lookup(t, client, fileOrURL(t, "/dir", base+"/schema.json"))
		require.NoError(t, err)
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("rooted file URL on any platform", func(t *testing.T) {
		t.Parallel()

		// A file URL names its path on every platform, so it never joins
		// baseDir, even where the platform reads a rooted path carrying no
		// volume as relative.
		url, _, err := load(t, fileOrURL(t, "/configs", "file:///schemas/config.json"))
		require.Error(t, err)
		assert.Equal(t, "file:///schemas/config.json", url)

		// The same reference needs no base directory to resolve.
		_, err = schema.FileOrURL("", "file:///schemas/config.json")
		require.NoError(t, err)
	})

	t.Run("rooted path on any platform", func(t *testing.T) {
		t.Parallel()

		// A rooted path never joins baseDir, as the rooted file URL above
		// does not. Windows reads a rooted path that carries no volume as
		// relative, so there the path would otherwise land under baseDir,
		// and an empty baseDir would report ErrNoBaseDir. The key names
		// the path as File does, on the current drive on Windows.
		url, _, err := load(t, fileOrURL(t, "/configs", "/schemas/config.json"))
		require.Error(t, err)
		assert.Equal(t, schema.File("/schemas/config.json").Key(), url)

		_, err = schema.FileOrURL("", "/schemas/config.json")
		require.NoError(t, err)
	})

	t.Run("windows file URL on any platform", func(t *testing.T) {
		t.Parallel()

		// A drive-letter path is absolute on Windows and names nothing a
		// POSIX base directory can resolve, so FileOrURL must keep the
		// reference intact rather than rewrite it against baseDir.
		url, _, err := load(t, fileOrURL(t, "/configs", "file:///C:/schemas/config.json"))
		require.Error(t, err)
		assert.Equal(t, "file:///C:/schemas/config.json", url)

		// The bare drive-letter path names the same schema, and neither
		// form picks up the working directory.
		url, _, err = load(t, fileOrURL(t, "/configs", "C:/schemas/config.json"))
		require.Error(t, err)
		assert.Equal(t, "file:///C:/schemas/config.json", url)

		// A file URL keys the cleaned path, as File keys a drive-letter
		// path, so both spellings share one registry entry.
		ref := fileOrURL(t, "/configs", "file:///C:/schemas/../schemas/config.json")
		assert.Equal(t, "file:///C:/schemas/config.json", ref.Key())
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

func TestFileOrURL_DriveLetter(t *testing.T) {
	t.Parallel()

	// A path that starts with a drive letter is absolute wherever a
	// program reads it, so it never joins baseDir and names the schema
	// File names for it. A colon anywhere else is part of a file name,
	// and the path joins baseDir like any other relative one.
	baseDir := t.TempDir()

	tcs := map[string]struct {
		path  string
		drive bool
	}{
		"upper-case drive":        {path: "C:/schemas/config.json", drive: true},
		"lower-case drive":        {path: "c:/schemas/config.json", drive: true},
		"drive alone":             {path: "C:", drive: true},
		"backslash separator":     {path: `C:\schemas\config.json`, drive: true},
		"colon without separator": {path: "a:b.json"},
		"digit before the colon":  {path: "1:/schemas/config.json"},
		"longer first segment":    {path: "ab:/schemas/config.json"},
		"single letter":           {path: "C"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := fileURL(t, filepath.Join(baseDir, tc.path))
			if tc.drive {
				want = fileURL(t, tc.path)
			}

			assert.Equal(t, want, fileOrURL(t, baseDir, tc.path).Key())
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

func TestFileOrURL_FileURLWithoutLocalPath(t *testing.T) {
	t.Parallel()

	// A file URL with a remote host, or with no path, names no local
	// path, so the whole reference is a relative file path.
	baseDir := t.TempDir()

	tcs := map[string]struct {
		ref string
	}{
		"remote host": {ref: "file://host/C:/schemas/config.json"},
		"empty path":  {ref: "file://"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := schema.FileOrURL("", tc.ref)
			require.ErrorIs(t, err, schema.ErrNoBaseDir)

			got := fileOrURL(t, baseDir, tc.ref)
			assert.Equal(t, fileURL(t, filepath.Join(baseDir, tc.ref)), got.Key())
		})
	}
}

func TestRefBeside(t *testing.T) {
	t.Parallel()

	const schemaData = `{"type": "object", "required": ["kind"]}`

	bundle := mapFS(map[string]string{
		"configs/schema.json": schemaData,
	})

	dir := filepath.Dir(writeSchema(t, "schema.json", []byte(schemaData)))

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
		assert.Equal(t, schema.File(filepath.Join(dir, "schema.json")).Key(), ref.Key())

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
		require.ErrorIs(t, err, schema.ErrLoad)
		require.ErrorIs(t, err, fs.ErrInvalid)
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

		for _, written := range []string{abs, "file://" + filepath.ToSlash(abs)} {
			ref, err := schema.RefBeside(inBundle, written)
			require.NoError(t, err)

			_, err = reg.Load(t.Context(), ref)
			require.ErrorIs(t, err, schema.ErrLoad)
			require.ErrorIs(t, err, fs.ErrInvalid)

			// The same reference beside a document on disk reads the file.
			ref, err = schema.RefBeside(onDisk, written)
			require.NoError(t, err)

			_, err = reg.Load(t.Context(), ref)
			require.NoError(t, err)
		}
	})

	t.Run("the origin of a layered value names a file beside its layer", func(t *testing.T) {
		t.Parallel()

		layered := mapFS(map[string]string{
			"configs/prod/schema.json": schemaData,
		})

		base := niceyaml.NewSourceFromString("kind: App\n",
			niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(layered))
		prod := niceyaml.NewSourceFromString("schema: ./schema.json\n",
			niceyaml.WithFilePath("configs/prod/app.yaml"), niceyaml.WithFS(layered))

		doc, err := yamltest.Layers(t, base, prod).Document()
		require.NoError(t, err)

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
