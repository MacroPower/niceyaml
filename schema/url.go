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
// compiled once rather than once per spelling. URL is for a URL the
// program knows, as [Loadable] is for a key it knows, so it panics when
// schemaURL is empty. A reference read from a directive or a command line
// goes through [FileOrURL].
//
// A registry rejects a response body over 10 MB. The client's Timeout and
// the context of the lookup bound each fetch. [Ref.Load] fetches the URL
// with [http.DefaultClient] for a caller that loads schemas without a
// registry.
//
//	r := schema.URL("https://example.com/schema.json")
func URL(schemaURL string) Ref {
	if schemaURL == "" {
		panic("schema.URL: url is empty")
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
