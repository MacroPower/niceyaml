package schema

import (
	"strings"
)

// URL creates a [Ref] that names a schema by its HTTP or HTTPS URL. The Ref
// is a [Resolver] that names the URL for every document, and a [Registry]
// fetches it once with the client [WithHTTPClient] gave it and reuses the
// compiled schema for every document that names it.
//
// URL lowercases the scheme of schemaURL and leaves the rest of it as
// written, so a registry fetches and compiles a schema spelled with
// different scheme case once rather than once per spelling. URL is for a
// URL written in the program, so it panics on an empty schemaURL, as
// [Loadable] panics on an empty key. A reference read from a directive
// or a command line, which may be empty or a file path, goes through
// [FileOrURL], which returns an error instead. The result is the shape a
// [Resolver] returns, so a resolver that builds the URL from the document
// hands it back beside a nil error.
//
// A fragment, such as the JSON pointer #/$defs/tasks or an anchor name,
// selects that subschema of the document the URL names, as a $ref with
// the same fragment would. The registry caches each fragment apart, so
// it fetches the document once for each fragment that names it. An empty
// fragment names the whole document, so URL drops a trailing '#'.
//
// A $ref in the schema resolves against the URL, and the registry fetches
// each HTTP or HTTPS URL a reference names with the same client. A
// reference to a file:// URL does not resolve, so a remote schema cannot
// read the local disk.
//
// A registry rejects a response body over 10 MB. The client's Timeout and
// the context of the lookup bound each fetch. [Registry.Load] fetches the
// URL and returns the bytes of the whole document, whatever its fragment,
// for a caller that wants them rather than the compiled schema.
func URL(schemaURL string) Ref {
	if schemaURL == "" {
		panic("schema.URL: schema URL is empty")
	}

	key := normalizeScheme(schemaURL)
	if base, fragment, ok := strings.Cut(key, "#"); ok && fragment == "" {
		key = base
	}

	return Ref{key: key, url: true}
}

// normalizeScheme lowercases the scheme at the start of ref when "://"
// follows it, and leaves the rest of ref as written, since a URL path is
// case-sensitive. The registry keys its cache on the URL, so one spelling
// means one fetch and one compile. A reference that does not start with a
// scheme and "://" comes back as written, such as one with no scheme and
// "://" in its query, or an opaque mailto: URL.
func normalizeScheme(ref string) string {
	const sep = "://"

	i := strings.Index(ref, sep)
	if i < 0 || !isScheme(ref[:i]) {
		return ref
	}

	return strings.ToLower(ref[:i]) + ref[i:]
}

// isScheme reports whether s is a URL scheme as RFC 3986 defines one: an
// ASCII letter followed by letters, digits, '+', '-' or '.'. A scheme
// holds no ':', so when a reference starts with a scheme and "://", the
// first "://" in it follows the scheme.
func isScheme(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		c := s[i]

		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		case i > 0 && ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}

	return true
}
