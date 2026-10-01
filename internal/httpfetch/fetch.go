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
// Errors name the URL as [Redacted] spells it, which replaces any password
// in its userinfo, so a password in a schema URL does not reach logs.
// [Redacted] keeps a username as written, so a token given as the
// username does reach logs.
// An error about a URL that does not parse quotes it only when it starts
// with http:// or https://, since [Redacted] can miss a password in other
// text that does not parse. When [Redacted] would hide a password in such
// a URL, errors omit the reason too, since the reason can quote part of
// the password.
//
// A password that starts with "/", "?" or "#" parses, but as a host with
// an empty port and then a path, query or fragment. Get refuses a URL
// with no password whose host ends in a colon when an "@" follows the
// host, and it sends no request, since the request would carry that
// password to a host named after the user. A password that starts with
// digits and then one of those delimiters parses as a host with a valid
// port, so Get cannot tell it from a URL with an "@" in its path and
// names the URL as written. A password that holds an "@" and later one
// of those delimiters parses as a shorter password and a host named after
// the text between them. Get cannot tell it from a URL with a password
// and an "@" in its path, so it sends the request to that host, and its
// errors name the URL as [Redacted] spells it. When that request fails,
// the error omits the reason, since the reason can name the host, as a
// failed DNS lookup does. When ctx has ended, the error wraps the
// context's error in place of the reason.
func Get(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		if _, ok := redactUnparsed(rawURL); ok {
			return nil, fmt.Errorf("%s: reason withheld because it can quote the password", parsePrefix(rawURL))
		}

		return nil, fmt.Errorf("%s: %w", parsePrefix(rawURL), reason(err))
	}

	if hidesPassword(u, rawURL) {
		return nil, fmt.Errorf(
			`%s: empty port and a later "@" suggest a password with an unencoded "/", "?" or "#"`,
			parsePrefix(rawURL),
		)
	}

	name := redacted(u, nil, rawURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", name, reason(err))
	}

	resp, err := client.Do(req)
	if err != nil {
		if splitsPassword(u, rawURL) {
			ctxErr := ctx.Err()
			if ctxErr != nil {
				return nil, fmt.Errorf("fetch %s: %w", name, ctxErr)
			}

			return nil, fmt.Errorf("fetch %s: reason withheld because it can quote the password", name)
		}

		return nil, fmt.Errorf("fetch %s: %w", name, reason(err))
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", name, resp.StatusCode)
	}

	// Read one byte past the limit so an over-size response reads as
	// MaxSize+1 bytes. Anything at or under the limit is the whole body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", name, err)
	}

	if int64(len(data)) > MaxSize {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", name, MaxSize)
	}

	return data, nil
}

// parsePrefix returns the start of an error about a URL that [Get] refuses
// before it sends a request. It quotes rawURL as [Redacted] spells it when
// rawURL starts with http:// or https://, since then the first "://" ends
// the scheme and [Redacted] finds any password after it.
func parsePrefix(rawURL string) string {
	if !IsHTTPURL(rawURL) {
		return "parse URL"
	}

	return fmt.Sprintf("parse URL %q", Redacted(rawURL))
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

// IsHTTPURL reports whether rawURL starts with http:// or https://, in
// any letter case. The schema loaders fetch only such a URL, and the
// SchemaStore catalog keeps only entries whose URL passes, so no catalog
// URL fails the loaders' scheme check. IsHTTPURL does not parse rawURL,
// so [Get] can still refuse a URL that passes, such as one that does not
// parse.
func IsHTTPURL(rawURL string) bool {
	for _, prefix := range []string{"http://", "https://"} {
		if len(rawURL) >= len(prefix) && strings.EqualFold(rawURL[:len(prefix)], prefix) {
			return true
		}
	}

	return false
}

// Redacted returns rawURL with any password in its userinfo replaced by
// "xxxxx", for use in messages. A string that carries no password comes
// back unchanged, so a name that is not a URL keeps its spelling. For a
// URL that does not parse, for one whose host ends in a colon when an "@"
// follows the host, and for one with a password and an "@" after its
// authority, Redacted replaces a wider span. The span runs from the first
// colon after "://" or a leading "//" to the last "@". It covers a
// password that holds a "/", "?" or "#", at its start or later, even
// after an "@" in the password. It can also cover text that is not a
// password, such as a port or a host and path before an "@" in the path.
// A password that starts with digits and then one of those delimiters
// parses as a host with a valid port, so Redacted cannot tell it from a
// URL with an "@" in its path and returns it unchanged.
func Redacted(rawURL string) string {
	u, err := url.Parse(rawURL)

	return redacted(u, err, rawURL)
}

// redacted is [Redacted] for rawURL, given the u and err that
// [url.Parse] returns for it, so [Get] can name the URL it already
// parsed.
func redacted(u *url.URL, err error, rawURL string) string {
	if err != nil || hidesPassword(u, rawURL) || splitsPassword(u, rawURL) {
		name, _ := redactUnparsed(rawURL)

		return name
	}

	if _, ok := u.User.Password(); !ok {
		return rawURL
	}

	return u.Redacted()
}

// splitsPassword reports whether u, parsed from rawURL, can hold part of
// a password in its host and path. The parser splits userinfo at the
// last "@" before the first "/", "?" or "#", so a password with an "@"
// and then one of those delimiters parses as a shorter password, and its
// tail lands in the host and path. A password and a later "@" mark that
// case.
func splitsPassword(u *url.URL, rawURL string) bool {
	_, ok := u.User.Password()

	return ok && atAfterAuthority(rawURL)
}

// atAfterAuthority reports whether rawURL holds an "@" after its
// authority, which ends at the first "/", "?" or "#" after "://" or a
// leading "//".
func atAfterAuthority(rawURL string) bool {
	start, ok := authorityStart(rawURL)
	if !ok {
		return false
	}

	rest := rawURL[start:]
	end := strings.IndexAny(rest, "/?#")

	return end >= 0 && strings.Contains(rest[end:], "@")
}

// hidesPassword reports whether u, parsed from rawURL, can hide a password
// the parser did not read as one. The parser reads "user:/pass@host" as the
// host "user:" with an empty port and then the path "/pass@host", and it
// reads a leading "?" or "#" in the password the same way. A username can
// hold an unencoded "@", so "jane@corp.com:/pass@host" reads as the user
// "jane" and the host "corp.com:". So a URL with no password in its
// userinfo, a host that ends in a colon, and an "@" after "://" or a
// leading "//" can hold a password. An authority seldom ends in an empty
// port, so the check seldom flags a URL that holds no password.
func hidesPassword(u *url.URL, rawURL string) bool {
	if _, ok := u.User.Password(); ok || !strings.HasSuffix(u.Host, ":") {
		return false
	}

	_, ok := redactUnparsed(rawURL)

	return ok
}

// redactUnparsed is [Redacted] for a URL that does not parse or that
// [hidesPassword] or [splitsPassword] flags. It replaces everything from
// the first colon after "://" or a leading "//" to the last "@", and it
// reports whether it found such a span. The span does not stop at the
// first "/", "?" or "#", because a password can hold one of them.
func redactUnparsed(rawURL string) (string, bool) {
	start, ok := authorityStart(rawURL)
	if !ok {
		return rawURL, false
	}

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

// authorityStart returns the index at which the authority of rawURL
// starts, and it reports whether rawURL has one. [url.Parse] reads an
// authority after a leading "//" in a URL with no scheme, and after a
// scheme and "://" otherwise. A scheme holds no colon, so the first
// "://" ends it.
func authorityStart(rawURL string) (int, bool) {
	if strings.HasPrefix(rawURL, "//") {
		return len("//"), true
	}

	const sep = "://"

	i := strings.Index(rawURL, sep)
	if i < 0 {
		return 0, false
	}

	return i + len(sep), true
}
