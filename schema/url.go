package schema

import (
	"context"
	"net/http"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

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

	return ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (Ref, error) {
		return Ref{
			URL: schemaURL,
			Load: func(ctx context.Context) ([]byte, error) {
				return httpfetch.Get(ctx, cfg.client, schemaURL)
			},
		}, nil
	})
}
