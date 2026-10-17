package schema

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrURLNotAllowed indicates that the registry refused to fetch a URL
// that its [Access] does not allow, and did not send a request for it.
// The program set the Access, so the document that names the URL is not
// at fault, and [niceyaml.IsInvalid] reports false for the error.
var ErrURLNotAllowed = errors.New("URL not under an allowed prefix")

// AllowURLPrefixes is an [AccessOption] that allows the URLs under each
// of prefixes:
//
//	access, err := schema.NewAccess(
//	    schema.AllowURLPrefixes("https://schemas.example.com/"),
//	)
//
// An [Access] without this option or [AllowAnyURL] does not allow any
// URL. The prefixes cover every fetch of a registry that holds the
// Access:
//
//   - The schema that a [Ref] from [URL] names, whether a resolver of
//     the program holds the Ref or [Directive] builds it from a document.
//   - Each document that a $ref of a schema names.
//   - Each hop of a redirect.
//
// A refused URL fails to load with an error wrapping [ErrURLNotAllowed].
// The registry fetches with a copy of the client that [WithHTTPClient]
// gave it. The copy keeps the Timeout, Transport, and Jar of that client.
// Its CheckRedirect refuses a hop outside the prefixes before it calls
// the CheckRedirect of the client.
//
// The prefixes apply to every resolver of the registry. A program that
// gives such an Access to a registry that holds a
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] must pass the
// hosts of the catalog's schemas to AllowURLPrefixes, or those schemas
// fail to load. Those hosts include https://json.schemastore.org/,
// https://www.schemastore.org/, and https://raw.githubusercontent.com/. A
// program that confines only the schemas that its documents name gives
// the Store a registry of its own, as [Registry.Resolve] describes. The
// Store fetches the catalog with a client of its own, so the prefixes do
// not reach the catalog.
//
// # Matching
//
// Each prefix is an http or https URL with a host and without userinfo,
// a query, or a fragment. A URL falls under a prefix when its scheme,
// host, and port match those of the prefix. Its path must also start
// with the path of the prefix at a segment boundary. The registry parses
// both URLs and compares the parts, not the text:
//
//   - The host matches in any letter case. A URL without a port stands
//     for the default port of its scheme, 80 for http and 443 for https.
//   - The prefix https://schemas.example.com/v1 covers /v1 and
//     /v1/app.json, but not /v10/app.json. With a trailing slash, as in
//     https://schemas.example.com/v1/, it leaves out /v1 itself.
//   - The path must fall under the prefix as written and with its dot
//     segments removed. The path /v1/../admin does not fall under /v1,
//     and /admin/../v1/app.json does not either. An escaped slash, as in
//     /v1%2F..%2Fadmin, does not end a segment.
//   - The prefix https://schemas.example.com does not cover a URL on the
//     host schemas.example.com.evil.net. It does not cover a URL whose
//     host follows an "@" either, as in
//     https://schemas.example.com@evil.net/.
//
// Matching ignores the query and the fragment of a URL.
//
// A prefix names a host, not an address. A host whose DNS answer points
// at an internal address still receives the request, as in DNS
// rebinding. A program that must keep the fetches off its own network
// checks each address in the Dialer of the client that it gives
// [WithHTTPClient]. The Untrusted Documents section of the package
// documentation pairs the Access with the other options that confine a
// registry.
//
// Given more than once, each call adds its prefixes to those of the
// calls before it.
//
// [NewAccess] returns an error for a prefix that does not parse, is not
// an http or https URL with a host, or holds userinfo, a query, or a
// fragment. The error hides the password of the prefix. NewAccess also
// returns an error for a prefix beside [AllowAnyURL], because the
// prefixes do not confine any URL when the Access allows every URL.
func AllowURLPrefixes(prefixes ...string) AccessOption {
	return func(c *accessConfig) error {
		if c.anyURL && len(prefixes) > 0 {
			return errors.New("schema.AllowURLPrefixes: the access allows every URL already")
		}

		var errs []error

		for _, prefix := range prefixes {
			p, err := parseURLPrefix(prefix)
			if err != nil {
				errs = append(errs, fmt.Errorf("schema.AllowURLPrefixes: %q: %w", httpfetch.Redacted(prefix), err))

				continue
			}

			c.prefixes = append(c.prefixes, p)
		}

		return errors.Join(errs...)
	}
}

// allowsURL reports whether c lets the registry fetch rawURL.
func (c *accessConfig) allowsURL(rawURL string) bool {
	if c.anyURL {
		return true
	}

	u, ok := parseHTTPURL(rawURL)

	return ok && slices.ContainsFunc(c.prefixes, func(prefix urlPrefix) bool {
		return prefix.covers(u)
	})
}

// checkURL returns nil when c lets the registry fetch rawURL, and an
// error wrapping [ErrURLNotAllowed] that names rawURL otherwise.
func (c *accessConfig) checkURL(rawURL string) error {
	if c.allowsURL(rawURL) {
		return nil
	}

	return fmt.Errorf("%w: %q", ErrURLNotAllowed, httpfetch.Redacted(rawURL))
}

// guard returns a copy of client whose CheckRedirect refuses a redirect
// to a URL that c does not allow. The copy passes any other redirect to
// the CheckRedirect of client. When client does not have one, the copy
// stops after ten redirects, as the default policy of [http.Client] does.
// For an Access that allows every URL, guard returns client itself.
func (c *accessConfig) guard(client *http.Client) *http.Client {
	if c.anyURL {
		return client
	}

	guarded := *client
	next := client.CheckRedirect

	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		err := c.checkURL(req.URL.String())
		if err != nil {
			return fmt.Errorf("redirect: %w", err)
		}

		if next != nil {
			return next(req, via)
		}

		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}

		return nil
	}

	return &guarded
}

// maxRedirects is the number of redirects that a client without a
// CheckRedirect of its own follows, as the default policy of
// [http.Client] does.
const maxRedirects = 10

// A urlPrefix is a prefix of [AllowURLPrefixes], or a URL to compare with
// one, split into the parts that the registry compares.
type urlPrefix struct {
	// The scheme, "http" or "https".
	scheme string
	// The host in lower case, without the brackets of an IPv6 address.
	host string
	// The port in decimal without leading zeros. It holds the default
	// port of the scheme when the URL does not name a port.
	port string
	// The escaped path as written, with each escape in the form that
	// [canonicalEscapes] gives it, and "/" for an empty path.
	path string
	// The path with its dot segments removed.
	clean string
}

// parseURLPrefix returns the parts of prefix, or an error when prefix is
// not a prefix that [AllowURLPrefixes] takes.
func parseURLPrefix(prefix string) (urlPrefix, error) {
	if strings.ContainsAny(prefix, "?#") {
		return urlPrefix{}, errors.New("prefix holds a query or a fragment")
	}

	u, err := url.Parse(prefix)
	if err != nil {
		return urlPrefix{}, errors.New("prefix does not parse")
	}

	if u.User != nil {
		return urlPrefix{}, errors.New("prefix holds userinfo")
	}

	p, ok := httpURLParts(u)
	if !ok {
		return urlPrefix{}, errors.New("prefix is not an http or https URL with a host and a valid port")
	}

	// The path of a URL must fall under the prefix both ways, so the
	// prefix takes the form without dot segments for both.
	p.path = p.clean

	return p, nil
}

// parseHTTPURL returns the parts of rawURL. It returns false when rawURL
// does not parse or is not an http or https URL with a host and a valid
// port.
func parseHTTPURL(rawURL string) (urlPrefix, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return urlPrefix{}, false
	}

	return httpURLParts(u)
}

// httpURLParts returns the parts of u, and false when u is not an http
// or https URL with a host and a valid port.
func httpURLParts(u *url.URL) (urlPrefix, bool) {
	scheme := strings.ToLower(u.Scheme)

	var defaultPort uint64

	switch scheme {
	case "http":
		defaultPort = 80
	case "https":
		defaultPort = 443
	default:
		return urlPrefix{}, false
	}

	host := strings.ToLower(u.Hostname())
	if u.Opaque != "" || host == "" {
		return urlPrefix{}, false
	}

	port := defaultPort

	if written := u.Port(); written != "" {
		n, err := strconv.ParseUint(written, 10, 16)
		if err != nil {
			return urlPrefix{}, false
		}

		port = n
	}

	path := canonicalEscapes(u.EscapedPath())
	if path == "" {
		path = "/"
	}

	return urlPrefix{
		scheme: scheme,
		host:   host,
		port:   strconv.FormatUint(port, 10),
		path:   path,
		clean:  removeDotSegments(path),
	}, true
}

// covers reports whether u, the parts of a URL, falls under p.
func (p urlPrefix) covers(u urlPrefix) bool {
	return u.scheme == p.scheme && u.host == p.host && u.port == p.port &&
		pathUnder(p.path, u.path) && pathUnder(p.path, u.clean)
}

// pathUnder reports whether path starts with prefix at a segment
// boundary. The boundary holds when path equals prefix, when prefix ends
// in a slash, or when a slash follows prefix in path.
func pathUnder(prefix, path string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}

	return len(path) == len(prefix) || strings.HasSuffix(prefix, "/") || path[len(prefix)] == '/'
}

// removeDotSegments returns path, an escaped path that starts with a
// slash, without its "." and ".." segments, as RFC 3986 section 5.2.4
// removes them. An escaped dot counts as a dot only after
// [canonicalEscapes] has decoded it, and an escaped slash does not end a
// segment.
func removeDotSegments(path string) string {
	segments := strings.Split(path, "/")[1:]
	kept := make([]string, 0, len(segments))

	for i, segment := range segments {
		last := i == len(segments)-1

		switch segment {
		case ".":
		case "..":
			if len(kept) > 0 {
				kept = kept[:len(kept)-1]
			}

		default:
			kept = append(kept, segment)

			continue
		}

		// A dot segment at the end leaves the path ending in a slash.
		if last {
			kept = append(kept, "")
		}
	}

	return "/" + strings.Join(kept, "/")
}
