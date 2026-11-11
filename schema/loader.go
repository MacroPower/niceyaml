package schema

import (
	"context"
	"fmt"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// LoaderFunc returns the bytes of the schema document at uri, an HTTP or
// HTTPS URL without a fragment. [WithLoader] gives a [Registry] one for
// the URLs under a prefix, and describes the form of uri.
type LoaderFunc func(ctx context.Context, uri string) ([]byte, error)

// WithLoader is a [RegistryOption] that serves the URLs under prefix with
// load in place of the HTTP client of the registry. The registry calls
// load for each document under prefix that it loads. Such a document is
// the schema that a [Ref] from [URL] names, or a document that a $ref
// names. The registry resolves the $refs of a schema from [File],
// [FileFS], or [URL]. A schema from [Embedded] or [Loadable] has no
// resolver of the registry, as [Registry.Schema] describes, so load
// never sees its $refs. A program thus bundles a set of schemas whose
// $id is the URL where their author publishes them. It names the root
// with FileFS and validates offline:
//
//	//go:embed schemas
//	var bundle embed.FS
//
//	offline, err := schema.NewAccess()
//	if err != nil {
//	    return err
//	}
//
//	reg := schema.NewRegistry(
//	    schema.WithAccess(offline),
//	    schema.WithLoader("https://schemas.example.com/v1/",
//	        func(_ context.Context, uri string) ([]byte, error) {
//	            return bundle.ReadFile("schemas/" + path.Base(uri))
//	        }),
//	)
//
//	root, err := reg.Schema(ctx, schema.FileFS(bundle, "schemas/root.json"))
//
// A relative $ref in root.json resolves against its $id, so the
// registry loads the documents beside it through load.
//
// # Loaded Documents
//
// The registry treats the bytes that load returns as it treats the body
// of a fetch. The limit of [WithLoadLimit] covers them. The registry
// keeps each document that a $ref names. So load runs once for each URL
// until [WithCacheLimit] or a release such as [Registry.Invalidate]
// drops the document. A schema whose $ref load does not serve fails to
// load with [ErrLoad], unless [WithRequireRefs] is false. The registry
// remembers a failed load for the interval of [WithRetryAfter].
// [Registry.Load] calls load on every call, as it fetches on every call.
//
// The error of a document that load did not serve is a [*FetchError].
// It names the URL and wraps the error that load returned. A loader
// should return an error that wraps [fs.ErrNotExist] for a URL that it
// does not hold. A caller then tells a missing document apart from any
// other failure. The answer of load is final, and the registry does not
// fetch the URL when load fails.
//
// # Loader Arguments
//
// The registry passes load the context of the request and the URL
// without its fragment. The URL keeps the spelling that the [Ref] gave
// it, or that the compiler gave a $ref when it resolved the $ref against
// its base. The registry matches the prefix against the parsed URL, but
// it does not rewrite uri in the form of the prefix. So the host of uri
// might hold upper-case letters, uri might name the default port, and
// its path might hold dot segments.
//
// A loader therefore reads the parts of uri that it needs, such as with
// [net/url.Parse] or [path.Base]. A loader that compares the text of uri
// with the prefix, such as with [strings.TrimPrefix] or as a key of a
// map, misses URLs that the prefix covers.
//
// A URL that holds userinfo, such as one from [URL] with a password,
// keeps that userinfo. The registry hides the password in the URL that a
// [*FetchError] names, but it quotes the error of load as it is. A loader
// must not put the password of uri into its error, or the error of the
// registry shows it. The registry might call load from several
// goroutines at once.
//
// # Matching
//
// A prefix covers a URL as a prefix of [AllowURLPrefixes] does, and
// WithLoader takes the prefixes that AllowURLPrefixes takes. When the
// prefixes of several loaders cover a URL, the loader of the longest
// prefix serves it. A later WithLoader for a prefix replaces the loader
// that an earlier one gave for it. Every URL that no prefix covers goes
// to the HTTP client of the registry, under its [Access].
//
// # Access
//
// A loader is code of the program. So the [Access] of the registry does
// not apply to the URLs that it serves, as the Access does not apply to
// a Ref from [FileFS]. A registry whose Access does not allow any URL
// still loads each URL under the prefix, and that includes a URL that a
// [Directive] names. A document can thus pick any URL under the prefix.
// A loader that builds a path from the URL must keep that path inside
// the store that it reads.
//
// WithLoader panics if load is nil, or if prefix is not a prefix that
// AllowURLPrefixes takes.
func WithLoader(prefix string, load LoaderFunc) RegistryOption {
	p, err := parseURLPrefix(prefix)
	if err != nil {
		panic(fmt.Sprintf("schema.WithLoader: %q: %v", httpfetch.Redacted(prefix), err))
	}

	if load == nil {
		panic("schema.WithLoader: load is nil")
	}

	return func(c *registryConfig) {
		c.loaders = append(c.loaders, urlLoader{prefix: p, load: load})
	}
}

// A urlLoader is the loader that [WithLoader] gives a registry for the
// URLs under its prefix.
type urlLoader struct {
	load   LoaderFunc
	prefix urlPrefix
}

// loaderFor returns the loader that serves rawURL, or nil when no prefix
// covers rawURL. Among the prefixes that cover rawURL, the longest wins,
// and the later of two equal prefixes wins.
func (c *registryConfig) loaderFor(rawURL string) LoaderFunc {
	u, ok := parseHTTPURL(rawURL)
	if !ok {
		return nil
	}

	var (
		best   LoaderFunc
		length = -1
	)

	for _, l := range c.loaders {
		if l.prefix.covers(u) && len(l.prefix.path) >= length {
			best, length = l.load, len(l.prefix.path)
		}
	}

	return best
}

// checkFetch returns nil when the registry can load rawURL, an HTTP or
// HTTPS URL, and an error wrapping [ErrURLNotAllowed] that names rawURL
// otherwise. A loader from [WithLoader] serves the URLs under its prefix
// whatever the [Access] allows.
func (c *registryConfig) checkFetch(rawURL string) error {
	if c.loaderFor(rawURL) != nil {
		return nil
	}

	return c.access.checkURL(rawURL)
}

// loadURL returns the bytes that load returns for rawURL without its
// fragment, within the load limit of the registry. Each error is a
// [*FetchError] that names the URL.
func (c *registryConfig) loadURL(ctx context.Context, load LoaderFunc, rawURL string) ([]byte, error) {
	uri, _, _ := strings.Cut(rawURL, "#")

	data, err := load(ctx, uri)
	if err != nil {
		return nil, &FetchError{URL: httpfetch.Redacted(uri), Err: err}
	}

	if len(data) > c.loadLimit {
		return nil, &FetchError{URL: httpfetch.Redacted(uri), Err: httpfetch.TooLarge("document", c.loadLimit)}
	}

	return data, nil
}
