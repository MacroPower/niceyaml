package loader

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Ref creates a [Loader] for a schema reference (file path or URL).
//
// This is a convenience wrapper that routes to [URL] for HTTP/HTTPS references
// or [File] for file paths. Use [URL] or [File] directly when the reference
// type is known at construction time.
//
// Schemes match case-insensitively. A file:// URL is read as the local path
// it names. A file:// URL with a host other than localhost names no local
// path, so Ref treats the whole reference as a relative file path and joins
// it to baseDir, where the read fails. The baseDir is used to resolve
// relative file paths. If schemaRef is an absolute path or an HTTP/HTTPS
// URL, baseDir is ignored. HTTPOptions are used when schemaRef is an
// HTTP/HTTPS URL; ignored for file paths.
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

	path := schemaRef
	if hasScheme(schemaRef, "file") {
		path = fileURLPath(schemaRef)
	}

	// Resolve relative path against baseDir.
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}

	return File(path)
}

// isHTTPURL reports whether ref starts with http:// or https://, in any
// letter case.
func isHTTPURL(ref string) bool {
	return hasScheme(ref, "http") || hasScheme(ref, "https")
}

// hasScheme reports whether ref starts with scheme followed by "://",
// compared case-insensitively.
func hasScheme(ref, scheme string) bool {
	prefix := scheme + "://"

	return len(ref) >= len(prefix) && strings.EqualFold(ref[:len(prefix)], prefix)
}

// fileURLPath returns the local path a file:// URL names. A URL that does
// not parse, names a host other than localhost, or names no path is returned
// unchanged so the file read reports it.
func fileURLPath(ref string) string {
	u, err := url.Parse(ref)
	if err != nil || u.Path == "" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return ref
	}

	return u.Path
}
