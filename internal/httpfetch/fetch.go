package httpfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// MaxSize is the largest response body [Get] accepts, in bytes.
const MaxSize = 10 * 1024 * 1024 // 10 MB.

// Get performs an HTTP GET for url with client and returns the body. It
// rejects a response with a status other than 200 OK and a body over
// [MaxSize] bytes.
//
// Errors name the URL with any password in its userinfo redacted, so a
// credential embedded in a schema URL does not reach logs.
func Get(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	name := Redacted(rawURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", name, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", name, resp.StatusCode)
	}

	// Read one byte past the limit so an over-size response reads as
	// MaxSize+1 bytes; anything at or under the limit is the whole body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", name, err)
	}

	if int64(len(data)) > MaxSize {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", name, MaxSize)
	}

	return data, nil
}

// Redacted returns rawURL with any password in its userinfo replaced by
// "xxxxx", for use in messages. A string that does not parse as a URL comes
// back unchanged.
func Redacted(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	return u.Redacted()
}
