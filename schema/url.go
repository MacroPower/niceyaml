package schema

import (
	"strings"
)

// URL creates a [Ref] that names a schema by its HTTP or HTTPS URL. The Ref
// is a [Resolver] that names the URL for every document, and a [Registry]
// fetches it once with the client [WithHTTPClient] gave it and reuses the
// compiled schema for every document that names it.
//
// The scheme of schemaURL is lowercased, and the rest of it left as
// written, so one schema spelled with different scheme case is fetched and
// compiled once rather than once per spelling. URL is for a URL written in
// the program, so it panics on an empty schemaURL, as [Loadable] panics on
// an empty key. A reference read from a directive or a command line, which
// may be empty or a file path, goes through [FileOrURL], which returns an
// error instead. The result is the shape a [Resolver] returns, so a
// resolver that builds the URL from the document hands it back beside a
// nil error.
//
// A registry rejects a response body over 10 MB. The client's Timeout and
// the context of the lookup bound each fetch. [Ref.Load] fetches the URL
// with the client given to it, for a caller that loads schemas without a
// registry.
func URL(schemaURL string) Ref {
	if schemaURL == "" {
		panic("schema.URL: schema URL is empty")
	}

	return Ref{key: normalizeScheme(schemaURL), url: true}
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
