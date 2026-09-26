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
// URL that does not parse, since Get cannot redact its userinfo. When
// [Redacted] would hide a password in such a URL, errors omit the reason
// too, since the reason can quote part of the password.
func Get(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		if _, ok := redactUnparsed(rawURL); ok {
			return nil, errors.New("parse URL: reason withheld because it can quote the password")
		}

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
// back unchanged, so a name that is not a URL keeps its spelling. For a
// URL that does not parse, Redacted replaces everything from the first
// colon after "://" to the last "@". That span covers a password that
// holds a "/", "?" or "#", and it can also cover text that is not a
// password, such as a port before an "@" in the path.
func Redacted(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		name, _ := redactUnparsed(rawURL)

		return name
	}

	if u.User == nil {
		return rawURL
	}

	if _, ok := u.User.Password(); !ok {
		return rawURL
	}

	return u.Redacted()
}

// redactUnparsed is [Redacted] for a URL that does not parse. It replaces
// everything from the first colon after "://" to the last "@", and it
// reports whether it found such a span. The span does not stop at the
// first "/", "?" or "#", because a password can hold one of them.
func redactUnparsed(rawURL string) (string, bool) {
	const sep = "://"

	i := strings.Index(rawURL, sep)
	if i < 0 {
		return rawURL, false
	}

	start := i + len(sep)
	rest := rawURL[start:]

	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return rawURL, false
	}

	colon := strings.Index(rest[:at], ":")
	if colon < 0 {
		return rawURL, false
	}

	return rawURL[:start+colon+1] + "xxxxx" + rest[at:], true
}
