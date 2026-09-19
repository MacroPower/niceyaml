package schema

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrEmptyPath reports an empty reference given to [FileOrURL], which names
// no file.
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
// File is for a path the program knows, as [Loadable] is for a key it
// knows, so it panics when path is empty or the working directory cannot
// be read to make it absolute. A path read from a directive or a command
// line goes through [FileOrURL], which returns those as errors.
//
// The file path is used directly without validation. Callers should ensure
// paths come from trusted sources or are validated before use to prevent
// path traversal attacks.
//
//	r := schema.File("./schemas/config.json")
func File(path string) Ref {
	ref, err := file(path)
	if err != nil {
		panic("schema.File: " + err.Error())
	}

	return ref
}

// file is [File] that reports an empty path as [ErrEmptyPath] and a
// working directory it cannot read as an error, for a path from input.
func file(path string) (Ref, error) {
	if path == "" {
		return Ref{}, ErrEmptyPath
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Ref{}, fmt.Errorf("resolve %s: %w", path, err)
	}

	return Loadable(fileURL(abs), func(_ context.Context) ([]byte, error) {
		data, err := os.ReadFile(abs) //nolint:gosec // User-provided file paths are intentional.
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", abs, err)
		}

		return data, nil
	}), nil
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
