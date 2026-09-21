package schema

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrEmptyPath reports an empty path given to [File] or [FileOrURL], which
// names no file.
var ErrEmptyPath = errors.New("schema file path is empty")

// File creates a [Ref] that reads schema data from a local file. The Ref
// is a [Resolver] that names the file for every document.
//
// File makes path absolute against the working directory and names the
// schema by the file:// URL of that absolute path, such as
// file:///srv/schemas/config.json. A schema that [Embedded] or another
// resolver names by a bare path such as "schemas/config.json" therefore
// never shares a cache entry with the file. Relative spellings of one path,
// such as "schemas/config.json" and "./schemas/config.json", resolve to the
// same URL, so the registry reads the file once and reuses the compiled
// validator for every document that names it. The file is read when the
// Ref loads, not when File runs.
//
// An empty path returns [ErrEmptyPath], and a working directory that
// cannot be read to make the path absolute returns that error. The result
// is the shape a [Resolver] returns, so a resolver that builds the path
// from the document hands it back as it is:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
//	    kind, err := doc.Get[string](ctx, kindPath)
//	    if err != nil {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    return schema.File("schemas/" + kind + ".json")
//	})
//
// [MustFile] panics instead, for a path written in the program. A
// reference that may be a URL, such as one read from a directive or a
// command line, goes through [FileOrURL].
//
// The file path is used directly without validation. Callers should ensure
// paths come from trusted sources or are validated before use to prevent
// path traversal attacks.
func File(path string) (Ref, error) {
	if path == "" {
		return Ref{}, ErrEmptyPath
	}

	abs := path

	// A drive-letter path is absolute wherever it is read, but filepath.Abs
	// on a POSIX platform treats it as relative and puts the working
	// directory in front of it. Keep it as written, so the drive survives
	// into the URL and the read error.
	if !hasDriveLetter(path) {
		var err error

		abs, err = filepath.Abs(path)
		if err != nil {
			return Ref{}, fmt.Errorf("resolve %s: %w", path, err)
		}
	}

	return Loadable(fileURL(abs), func(_ context.Context) ([]byte, error) {
		// Off Windows, a drive letter is an ordinary directory name, so
		// os.ReadFile would read the path against the working directory
		// while the key stays the cwd-independent drive URL. One key would
		// then name different bytes per directory, so refuse the read.
		if hasDriveLetter(abs) && runtime.GOOS != "windows" {
			return nil, fmt.Errorf("read %s: a drive letter names no file on %s", abs, runtime.GOOS)
		}

		data, err := os.ReadFile(abs) //nolint:gosec // User-provided file paths are intentional.
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", abs, err)
		}

		return data, nil
	}), nil
}

// MustFile is [File] that panics when path is empty or the working
// directory cannot be read, for a path written in the program, as
// [MustCompile] is for a schema known valid at build time:
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.When(matcher.MustFilePath("*.deploy.yaml"), schema.MustFile("schemas/deploy.json")),
//	))
func MustFile(path string) Ref {
	ref, err := File(path)
	if err != nil {
		panic("schema.MustFile: " + err.Error())
	}

	return ref
}

// fileURL returns the file:// URL that names the absolute path abs.
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)

	// A Windows path starts with a drive letter, which needs a slash in front
	// of it in a URL path, as in file:///C:/schemas/config.json.
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return (&url.URL{Scheme: "file", Path: p}).String()
}
