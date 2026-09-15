package loader

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
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

// maxSchemaSize is the maximum size of a schema response body.
const maxSchemaSize = 10 * 1024 * 1024 // 10 MB.

// WithHTTPClient is an [HTTPOption] that sets a custom HTTP client.
//
// Applies to [URL] and [FileOrURL] loaders.
func WithHTTPClient(client *http.Client) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.client = client
	}
}

// URL creates a [schema.Resolver] that fetches schema data from an
// HTTP/HTTPS URL. The registry fetches once per URL and reuses the compiled
// validator for every document that names it.
//
// By default, the loader uses [http.DefaultClient] which has no explicit
// request timeout. Timeouts are controlled via the context passed to Load.
// Use [WithHTTPClient] to provide a client with custom timeout settings. The
// loader rejects a response body over 10 MB.
//
//	r := loader.URL("https://example.com/schema.json")
func URL(schemaURL string, opts ...HTTPOption) schema.Resolver {
	cfg := &httpConfig{client: http.DefaultClient}
	for _, opt := range opts {
		opt(cfg)
	}

	return schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
		return schema.Ref{
			URL: schemaURL,
			Load: func(ctx context.Context) ([]byte, error) {
				return fetch(ctx, cfg.client, schemaURL)
			},
		}, nil
	})
}

// fetch performs an HTTP GET for schemaURL and returns the body, rejecting
// non-200 responses and bodies over maxSchemaSize.
func fetch(ctx context.Context, client *http.Client, schemaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, schemaURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", schemaURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", schemaURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", schemaURL, resp.StatusCode)
	}

	// Read one byte past the limit so an over-size response reads as
	// maxSchemaSize+1 bytes; anything at or under the limit is the whole body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSchemaSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", schemaURL, err)
	}

	if int64(len(data)) > maxSchemaSize {
		return nil, fmt.Errorf("fetch %s: schema exceeds %d bytes", schemaURL, maxSchemaSize)
	}

	return data, nil
}
