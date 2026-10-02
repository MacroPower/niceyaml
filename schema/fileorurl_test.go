package schema_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	t.Run("relative path", func(t *testing.T) {
		t.Parallel()

		// Create temp schema file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		url, data, err := load(t, fileOrURL(t, tmpDir, "schema.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
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

	t.Run("absolute path", func(t *testing.T) {
		t.Parallel()

		// Create temp schema file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		// An absolute path ignores baseDir.
		url, data, err := load(t, fileOrURL(t, "/some/other/dir", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("absolute path without base directory", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		url, data, err := load(t, fileOrURL(t, "", schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("URL schema", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := server.URL + "/schema.json"

		// A URL ignores baseDir.
		url, data, err := load(t, fileOrURL(t, "/some/dir", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("URL without base directory", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := server.URL + "/schema.json"

		url, data, err := load(t, fileOrURL(t, "", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, schemaURL, url)
	})

	t.Run("URL scheme in upper case", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		schemaURL := "HTTP://" + strings.TrimPrefix(server.URL, "http://") + "/schema.json"

		// The resolved URL keys the registry cache, so the scheme comes
		// back in lower case however the reference spelled it.
		url, data, err := load(t, fileOrURL(t, "/some/dir", schemaURL))
		require.NoError(t, err)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, server.URL+"/schema.json", url)
	})

	t.Run("file URL", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		// A file URL names a local path, so it ignores baseDir.
		url, data, err := load(t, fileOrURL(t, "/some/other/dir", "file://"+schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("file URL without authority", func(t *testing.T) {
		t.Parallel()

		// RFC 8089 allows file:/path alongside file:///path, and both name
		// the same absolute path rather than a path relative to baseDir.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		require.NoError(t, os.WriteFile(schemaPath, schemaData, 0o600))

		url, data, err := load(t, fileOrURL(t, "/some/other/dir", "file:"+filepath.ToSlash(schemaPath)))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("file URL scheme in upper case", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		_, data, err := load(t, fileOrURL(t, "/some/other/dir", "FILE://"+schemaPath))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
	})

	t.Run("file URL with percent-encoded path", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "my schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		encoded := "file://" + filepath.Join(tmpDir, "my%20schema.json")

		url, data, err := load(t, fileOrURL(t, "/some/other/dir", encoded))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
	})

	t.Run("file URL with fragment", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"definitions": {"Foo": {"type": "object"}}}`)
		require.NoError(t, os.WriteFile(schemaPath, schemaData, 0o600))

		// The key keeps the fragment that names the subschema, and the
		// registry loads the whole file.
		ref := "file://" + filepath.ToSlash(schemaPath) + "#/definitions/Foo"

		url, data, err := load(t, fileOrURL(t, "/some/other/dir", ref))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath)+"#/definitions/Foo", url)
	})

	t.Run("URL with custom client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		var requests atomic.Int32

		r := fileOrURL(t, "/dir", server.URL+"/schema.json")

		// The registry fetches the URL with its own client.
		err := lookup(t, countingClient(&requests), r)
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

	t.Run("colon in a relative path", func(t *testing.T) {
		t.Parallel()

		// A colon is a legal character in a POSIX file name, and only a
		// separator behind it makes a drive letter, so this path joins
		// baseDir like any other relative one.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "a:b.json")
		schemaData := []byte(`{"type": "object"}`)
		require.NoError(t, os.WriteFile(schemaPath, schemaData, 0o600))

		url, data, err := load(t, fileOrURL(t, tmpDir, "a:b.json"))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, schemaPath), url)
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
