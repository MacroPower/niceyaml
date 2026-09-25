package httpfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxSize is the largest response body [Get] accepts, in bytes.
const MaxSize = 10 * 1024 * 1024 // 10 MB.

// Get performs an HTTP GET for rawURL with client and returns the body. It
// rejects any status other than 200 OK and any body over [MaxSize] bytes.
//
// Errors name the URL with any password in its userinfo redacted, so a
// credential embedded in a schema URL does not reach logs. Errors omit a
// URL that does not parse, since Get cannot redact its userinfo.
func Get(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", reason(err))
	}

	name := u.Redacted()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", name, reason(err))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", name, reason(err))
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

// reason returns the reason err reports without the URL a [*url.Error]
// quotes around it, which carries the userinfo as the caller wrote it.
// The reason names the part of the URL it rejected, never the whole.
func reason(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return urlErr.Err
	}

	return err
}

// Redacted returns rawURL with any password in its userinfo replaced by
// "xxxxx", for use in messages. A string that carries no password comes
// back unchanged, so a name that is not a URL keeps its spelling. A URL
// that does not parse still has the password between the first colon of
// its userinfo and the "@" replaced.
func Redacted(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return redactUnparsed(rawURL)
	}

	if u.User == nil {
		return rawURL
	}

	if _, ok := u.User.Password(); !ok {
		return rawURL
	}

	return u.Redacted()
}

// redactUnparsed is [Redacted] for a URL that does not parse. It finds the
// authority after "://" and replaces the password in its userinfo.
func redactUnparsed(rawURL string) string {
	const sep = "://"

	i := strings.Index(rawURL, sep)
	if i < 0 {
		return rawURL
	}

	start := i + len(sep)
	rest := rawURL[start:]

	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}

	at := strings.LastIndex(rest[:end], "@")
	if at < 0 {
		return rawURL
	}

	colon := strings.Index(rest[:at], ":")
	if colon < 0 {
		return rawURL
	}

	return rawURL[:start+colon+1] + "xxxxx" + rest[at:]
}
