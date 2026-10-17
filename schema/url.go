package schema

import (
	"strings"
)

// URL creates a [Ref] that names a schema by its HTTP or HTTPS URL. The
// Ref is a [Resolver] that names the URL for every document. A [Registry]
// fetches the URL once with the client that [WithHTTPClient] gave it, and
// reuses the compiled schema for every document that names it.
//
// URL lowercases the scheme of schemaURL and leaves the rest of it as
// written. A registry thus fetches and compiles a schema once, whatever
// the letter case of its scheme, rather than once per spelling.
//
// URL is for a URL written in the program, so it panics on an empty
// schemaURL, as [Loadable] panics on an empty key. A reference read from
// a directive or a command line might be empty or a file path. Such a
// reference goes through [FileOrURL], which returns an error instead. URL
// returns a [Ref], the type that a [Resolver] returns. A resolver that
// builds the URL from the document thus returns the Ref beside a nil
// error.
//
// A registry fetches only HTTP and HTTPS URLs. For a URL with any other
// scheme, such as a file:// URL, [Registry.Schema] and [Registry.Load]
// return an error wrapping [ErrLoad], whatever the registry has cached
// under the same key. A local file goes through [File].
//
// A fragment, such as the JSON pointer #/$defs/tasks or an anchor name,
// selects that subschema of the document that the URL names, as a $ref
// with the same fragment does. The registry caches each fragment apart, so
// it fetches the document once for each fragment that names it. An empty
// fragment names the whole document, so URL drops a trailing '#'.
//
// A $ref in the schema resolves against the URL. The registry fetches
// each HTTP or HTTPS URL that a reference names with the same client. A
// reference to a file:// URL does not resolve, so a remote schema cannot
// read the local disk. A schema with a reference that does not resolve
// fails to load, as [Registry.Schema] describes. The registry sends the
// userinfo of the URL, such as a password, with each fetch of a URL that
// a reference names at the same scheme and host. It keeps the password
// out of its errors.
//
// A registry rejects a response body of more than the limit of
// [WithLoadLimit], [DefaultLoadLimit] by default, and a response with any
// status other than 200 OK. Either one fails to load with a [*LoadError]
// that holds a [*FetchError]. The client's Timeout and the context of the
// lookup bound each fetch. [Registry.Load] fetches the URL and returns
// the bytes of the whole document, whatever its fragment, for a caller
// that wants them rather than the compiled schema.
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
// follows it. It leaves the rest of ref as written, because a URL path is
// case-sensitive. The registry keys its cache on the URL, so one spelling
// means one fetch and one compile. A reference that does not start with a
// scheme and "://" comes back as written. Such references include one
// without a scheme that holds "://" in its query, and an opaque mailto:
// URL.
func normalizeScheme(ref string) string {
	const sep = "://"

	i := strings.Index(ref, sep)
	if i < 0 || !isScheme(ref[:i]) {
		return ref
	}

	return strings.ToLower(ref[:i]) + ref[i:]
}

// isScheme reports whether s is a URL scheme as RFC 3986 defines one. Such
// a scheme is an ASCII letter followed by letters, digits, '+', '-', or
// '.'. A scheme does not hold a ':', so when a reference starts with a
// scheme and "://", the first "://" in it follows the scheme.
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
