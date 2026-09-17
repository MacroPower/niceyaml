package schema

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// ErrEmptyPath reports an empty file path given to [File] or [FileOrURL],
// which names no file.
var ErrEmptyPath = errors.New("schema file path is empty")

// File creates a [Resolver] that reads schema data from a local file.
//
// Resolve makes path absolute against the working directory and names the
// schema by the file:// URL of that absolute path, such as
// file:///srv/schemas/config.json. A schema that [Embedded] or another
// resolver names by a bare path such as "schemas/config.json" therefore
// never shares a cache entry with the file. Relative spellings of one path,
// such as "schemas/config.json" and "./schemas/config.json", resolve to the
// same URL, so the registry reads the file once and reuses the compiled
// validator for every document that names it. An empty path reports
// [ErrEmptyPath] from Resolve.
//
// The file path is used directly without validation. Callers should ensure
// paths come from trusted sources or are validated before use to prevent
// path traversal attacks.
//
//	r := schema.File("./schemas/config.json")
func File(path string) Resolver {
	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		if path == "" {
			return Ref{}, ErrEmptyPath
		}

		abs, err := filepath.Abs(path)
		if err != nil {
			return Ref{}, fmt.Errorf("resolve %s: %w", path, err)
		}

		return Ref{
			URL: fileURL(abs),
			Load: func(_ context.Context) ([]byte, error) {
				data, err := os.ReadFile(abs) //nolint:gosec // User-provided file paths are intentional.
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", abs, err)
				}

				return data, nil
			},
		}, nil
	})
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
