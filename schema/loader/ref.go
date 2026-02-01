package loader

import (
	"path/filepath"
	"strings"
)

// Ref creates a [Loader] for a schema reference (file path or URL).
//
// This is a convenience wrapper that routes to [URL] for HTTP/HTTPS references
// or [File] for file paths. Use [URL] or [File] directly when the reference
// type is known at construction time.
//
// The baseDir is used to resolve relative file paths. If schemaRef is an
// absolute path or URL (http/https), baseDir is ignored. HTTPOptions are used
// when schemaRef is a URL; ignored for file paths.
//
//	// Relative path resolved against baseDir.
//	l := loader.Ref("/configs", "schema.json")
//
//	// Absolute path used directly.
//	l := loader.Ref("/configs", "/schemas/config.json")
//
//	// URL fetched directly.
//	l := loader.Ref("/configs", "https://example.com/schema.json")
func Ref(baseDir, schemaRef string, opts ...HTTPOption) Loader {
	// Check for HTTP/HTTPS URL using string prefix to avoid URL parsing errors
	// that could cause malformed URLs to be treated as file paths.
	if isHTTPURL(schemaRef) {
		return URL(schemaRef, opts...)
	}

	// Resolve relative path against baseDir.
	path := schemaRef
	if !filepath.IsAbs(schemaRef) {
		path = filepath.Join(baseDir, schemaRef)
	}

	return File(path)
}

// isHTTPURL reports whether ref starts with http:// or https://.
func isHTTPURL(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}
