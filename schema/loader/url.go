package loader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"jacobcolvin.com/niceyaml"
)

// HTTPOption configures HTTP client settings for loaders that fetch schemas
// over HTTP.
//
// Used by [URL] and [Ref].
//
// Available options:
//   - [WithHTTPClient]
type HTTPOption func(*httpConfig)

// httpConfig holds HTTP configuration shared by URL and Ref loaders.
type httpConfig struct {
	client *http.Client
}

// maxSchemaSize is the maximum size of a schema response body.
const maxSchemaSize = 10 * 1024 * 1024 // 10 MB.

// WithHTTPClient is an [HTTPOption] that sets a custom HTTP client.
//
// Applies to [URL] and [Ref] loaders.
func WithHTTPClient(client *http.Client) HTTPOption {
	return func(cfg *httpConfig) {
		cfg.client = client
	}
}

// URL creates a [Loader] that fetches schema data from an HTTP/HTTPS URL.
//
// By default, the loader uses [http.DefaultClient] which has no explicit
// request timeout. Timeouts are controlled via the context passed to Load.
// Use [WithHTTPClient] to provide a client with custom timeout settings.
//
//	l := loader.URL("https://example.com/schema.json")
func URL(schemaURL string, opts ...HTTPOption) Loader {
	cfg := &httpConfig{client: http.DefaultClient}
	for _, opt := range opts {
		opt(cfg)
	}

	return Func(func(ctx context.Context, _ *niceyaml.DocumentDecoder) (Result, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, schemaURL, http.NoBody)
		if err != nil {
			return Result{}, fmt.Errorf("create request for %s: %w", schemaURL, err)
		}

		resp, err := cfg.client.Do(req)
		if err != nil {
			return Result{}, fmt.Errorf("fetch %s: %w", schemaURL, err)
		}
		defer resp.Body.Close() //nolint:errcheck // Best-effort close.

		if resp.StatusCode != http.StatusOK {
			return Result{}, fmt.Errorf("fetch %s: status %d", schemaURL, resp.StatusCode)
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxSchemaSize))
		if err != nil {
			return Result{}, fmt.Errorf("read response from %s: %w", schemaURL, err)
		}

		// If we read exactly maxSchemaSize bytes, try reading one more to detect
		// if the response was truncated. If we can read another byte (err == nil)
		// or get any error other than EOF, the actual size exceeds our limit.
		if int64(len(data)) == maxSchemaSize {
			var extra [1]byte

			_, err = resp.Body.Read(extra[:])
			if err == nil || !errors.Is(err, io.EOF) {
				return Result{}, fmt.Errorf("fetch %s: schema exceeds %d bytes", schemaURL, maxSchemaSize)
			}
		}

		return NewResult(schemaURL, data), nil
	})
}
