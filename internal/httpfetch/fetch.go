package httpfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// MaxSize is the largest response body [Get] accepts, in bytes.
const MaxSize = 10 * 1024 * 1024 // 10 MB.

// Get performs an HTTP GET for url with client and returns the body. It
// rejects a response with a status other than 200 OK and a body over
// [MaxSize] bytes.
func Get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}

	// Read one byte past the limit so an over-size response reads as
	// MaxSize+1 bytes; anything at or under the limit is the whole body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", url, err)
	}

	if int64(len(data)) > MaxSize {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, MaxSize)
	}

	return data, nil
}
