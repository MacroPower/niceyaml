package schema

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrEmptyURL reports an empty URL given to [URL], which names no schema.
var ErrEmptyURL = errors.New("schema URL is empty")

// HTTPOption configures HTTP client settings for loaders that fetch schemas
// over HTTP.
//
// Used by [URL] and [FileOrURL].
//
// Available options:
//   - [WithHTTPClient]
type HTTPOption func(*httpConfig)

// httpConfig holds HTTP configuration shared by the URL and FileOrURL
// loaders.
type httpConfig struct {
	client *http.Client
}

// WithHTTPClient is an [HTTPOption] that sets a custom HTTP client. A nil
// client keeps the default, [http.DefaultClient].
//
// Applies to [URL] and [FileOrURL] loaders.
func WithHTTPClient(client *http.Client) HTTPOption {
	return func(cfg *httpConfig) {
		if client != nil {
			cfg.client = client
		}
	}
}

// URL creates a [Resolver] that fetches schema data from an
// HTTP/HTTPS URL. The registry fetches once per URL and reuses the compiled
// validator for every document that names it.
//
// The scheme of schemaURL is lowercased, and the rest of it left as
// written, so one schema spelled with different scheme case is fetched and
// compiled once rather than once per spelling. An empty schemaURL reports
// [ErrEmptyURL] from Resolve.
//
// By default, the loader uses [http.DefaultClient] which has no explicit
// request timeout. Timeouts are controlled via the context passed to Load.
// Use [WithHTTPClient] to provide a client with custom timeout settings. The
// loader rejects a response body over 10 MB.
//
//	r := schema.URL("https://example.com/schema.json")
func URL(schemaURL string, opts ...HTTPOption) Resolver {
	cfg := &httpConfig{client: http.DefaultClient}
	for _, opt := range opts {
		opt(cfg)
	}

	schemaURL = normalizeScheme(schemaURL)

	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		if schemaURL == "" {
			return Ref{}, ErrEmptyURL
		}

		return Loadable(schemaURL, func(ctx context.Context) ([]byte, error) {
			return httpfetch.Get(ctx, cfg.client, schemaURL)
		}), nil
	})
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
