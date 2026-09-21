package schema

import (
	"errors"
	"strings"
)

// ErrEmptyURL reports an empty URL given to [URL], which names no schema.
var ErrEmptyURL = errors.New("schema URL is empty")

// URL creates a [Ref] that names a schema by its HTTP or HTTPS URL. The Ref
// is a [Resolver] that names the URL for every document, and a [Registry]
// fetches it once with the client [WithHTTPClient] gave it and reuses the
// compiled schema for every document that names it.
//
// The scheme of schemaURL is lowercased, and the rest of it left as
// written, so one schema spelled with different scheme case is fetched and
// compiled once rather than once per spelling. An empty schemaURL returns
// [ErrEmptyURL]. The result is the shape a [Resolver] returns, so a
// resolver that builds the URL from the document hands it back as it is.
// [MustURL] panics instead, for a URL written in the program, and a
// reference that may be a file path, such as one read from a directive or
// a command line, goes through [FileOrURL].
//
// A registry rejects a response body over 10 MB. The client's Timeout and
// the context of the lookup bound each fetch. [Ref.Load] fetches the URL
// with [http.DefaultClient] for a caller that loads schemas without a
// registry.
func URL(schemaURL string) (Ref, error) {
	if schemaURL == "" {
		return Ref{}, ErrEmptyURL
	}

	return Ref{key: normalizeScheme(schemaURL), url: true}, nil
}

// MustURL is [URL] that panics when schemaURL is empty, for a URL written
// in the program, as [MustCompile] is for a schema known valid at build
// time:
//
//	r := schema.MustURL("https://example.com/schema.json")
func MustURL(schemaURL string) Ref {
	ref, err := URL(schemaURL)
	if err != nil {
		panic("schema.MustURL: " + err.Error())
	}

	return ref
}

// normalizeScheme lowercases the scheme of ref and leaves the rest of it
// as written, since a URL path is case-sensitive. The registry keys its
// cache on the URL, so one spelling means one fetch and one compile.
func normalizeScheme(ref string) string {
	const sep = "://"

	i := strings.Index(ref, sep)
	if i < 0 {
		return ref
	}

	return strings.ToLower(ref[:i]) + ref[i:]
}
