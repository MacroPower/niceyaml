package httpfetch_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

func TestGet_RedactsPassword(t *testing.T) {
	t.Parallel()

	// Get sends no request for a URL that does not parse, and its error
	// names the URL as Redacted spells it. A password that starts with
	// "/", "?" or "#" parses, but as an empty port and then a path, query
	// or fragment, so Get refuses it too.
	tcs := map[string]struct {
		url string
	}{
		"invalid port":                           {url: "https://user:secret@example.com:port/x"},
		"invalid percent escape":                 {url: "https://user:secret@example.com/%zz"},
		"control character":                      {url: "https://user:secret@example.com/\x7f"},
		"password with a slash":                  {url: "https://user:secret/x@example.com/x"},
		"password with a question mark":          {url: "https://user:secret?x@example.com/x"},
		"password with a hash":                   {url: "https://user:secret#x@example.com/x"},
		"password starting with a slash":         {url: "https://user:/secret@example.com/x"},
		"password starting with a question mark": {url: "https://user:?secret@example.com/x"},
		"password starting with a hash":          {url: "https://user:#secret@example.com/x"},
		"username with an at sign":               {url: "https://jane@corp.com:/secret@example.com/x"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				t.Errorf("Get sent a request to host %q", r.URL.Host)

				return nil, errors.New("unexpected request")
			})}

			_, err := httpfetch.Get(t.Context(), client, tc.url)
			require.ErrorContains(t, err, "parse URL "+strconv.Quote(httpfetch.Redacted(tc.url))+": ")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

// roundTripFunc adapts a function to [http.RoundTripper].
type roundTripFunc func(r *http.Request) (*http.Response, error)

// RoundTrip implements [http.RoundTripper].
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGet_RedactsPasswordWithAtSign(t *testing.T) {
	t.Parallel()

	// A password with an "@" and then "/", "?" or "#" parses as a shorter
	// password and a host named after the text between them. Get sends
	// the request as parsed. The transport error names that host, as a
	// dialer or a DNS lookup does, so Get leaves the error out.
	tcs := map[string]struct {
		url string
	}{
		"slash":         {url: "https://user:p@host.invalid/tail@example.com/s.json"},
		"question mark": {url: "https://user:p@host.invalid?tail@example.com/s.json"},
		"hash":          {url: "https://user:p@host.invalid#tail@example.com/s.json"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, errors.New("lookup " + r.URL.Host + ": no such host")
			})}

			_, err := httpfetch.Get(t.Context(), client, tc.url)
			require.ErrorContains(t, err, "fetch "+httpfetch.Redacted(tc.url)+": reason withheld")
			assert.NotContains(t, err.Error(), "host.invalid")
			assert.NotContains(t, err.Error(), "tail")
		})
	}
}

func TestGet_PasswordWithAtSignKeepsContextError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("lookup %s: %w", r.URL.Host, r.Context().Err())
	})}

	_, err := httpfetch.Get(ctx, client, "https://user:p@host.invalid/tail@example.com/s.json")
	require.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "host.invalid")
	assert.NotContains(t, err.Error(), "tail")
}

func TestGet_ParseReason(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		url  string
		want string
	}{
		"url with no password keeps the reason": {
			url:  "https://example.com:port/x",
			want: `parse URL "https://example.com:port/x": invalid port ":port" after host`,
		},
		"control character is quoted": {
			url:  "https://example.com/\x7f",
			want: `parse URL "https://example.com/\x7f": `,
		},
		// Redacted finds a password only after a scheme and "://", so Get
		// leaves any other text out of the error.
		"scheme-relative url is not named": {
			url:  "//user:secret@example.com/%zz",
			want: `parse URL: invalid URL escape "%zz"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := httpfetch.Get(t.Context(), http.DefaultClient, tc.url)
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestGet_RedactsPasswordOnTransport(t *testing.T) {
	t.Parallel()

	// The dialer refuses every connection in place of a closed server, so
	// the test never dials a released port that another process may have
	// bound. The client quotes the URL as written in its error, and Get
	// names the URL once, redacted.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, syscall.ECONNREFUSED
			},
		},
	}

	_, err := httpfetch.Get(t.Context(), client, "http://user:secret@127.0.0.1:8080/s.json")
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
	assert.NotContains(t, err.Error(), "secret")
	assert.NotContains(t, err.Error(), `Get "`)
	assert.Equal(t, 1, strings.Count(err.Error(), "xxxxx"))
}

func TestGet_RedactsPasswordOnStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	u.User = url.UserPassword("user", "secret")

	_, err = httpfetch.Get(t.Context(), server.Client(), u.String())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
	assert.Contains(t, err.Error(), "xxxxx")
}

func TestGet_NamesURLAsRedacted(t *testing.T) {
	t.Parallel()

	// Callers name the URL with Redacted, so Get spells it the same way in
	// its errors rather than re-encoding it.
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	u.User = url.UserPassword("user", "secret")

	tcs := map[string]struct {
		url string
	}{
		"space in the path": {
			url: server.URL + "/my schema.json",
		},
		"password and a space in the path": {
			url: u.String() + "/my schema.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := httpfetch.Get(t.Context(), server.Client(), tc.url)
			require.ErrorContains(t, err, "fetch "+httpfetch.Redacted(tc.url)+": status 404")
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestRedacted(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		url  string
		want string
	}{
		"password": {
			url:  "https://user:secret@example.com/s.json",
			want: "https://user:xxxxx@example.com/s.json",
		},
		"user without password": {
			url:  "https://user@example.com/s.json",
			want: "https://user@example.com/s.json",
		},
		"no userinfo keeps its spelling": {
			url:  "file:///srv/my schema.json",
			want: "file:///srv/my schema.json",
		},
		"not a url": {
			url:  "embedded:0123abcd",
			want: "embedded:0123abcd",
		},
		"password in a url that does not parse": {
			url:  "https://user:secret@example.com/%zz",
			want: "https://user:xxxxx@example.com/%zz",
		},
		"password in a url with an invalid port": {
			url:  "https://user:secret@example.com:port/x",
			want: "https://user:xxxxx@example.com:port/x",
		},
		"password with a slash in a url that does not parse": {
			url:  "https://user:s3/cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password with a question mark in a url that does not parse": {
			url:  "https://user:s3?cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password with a hash in a url that does not parse": {
			url:  "https://user:s3#cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password starting with a slash": {
			url:  "https://user:/s3cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password starting with a question mark": {
			url:  "https://user:?s3cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password starting with a hash": {
			url:  "https://user:#s3cret@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"username with an at sign and a password starting with a slash": {
			url:  "https://jane@corp.com:/s3cret@example.com/x",
			want: "https://jane@corp.com:xxxxx@example.com/x",
		},
		"user without password and an empty port keeps its spelling": {
			url:  "https://jane@corp.com:/x",
			want: "https://jane@corp.com:/x",
		},
		"password with an at sign and then a slash": {
			url:  "https://user:p@ss/word@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password with an at sign and then a question mark": {
			url:  "https://user:p@ss?word@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password with an at sign and then a hash": {
			url:  "https://user:p@ss#word@example.com/x",
			want: "https://user:xxxxx@example.com/x",
		},
		"password and an at sign in the path redacts through the path": {
			url:  "https://user:pw@example.com/pkg@1.0/x",
			want: "https://user:xxxxx@1.0/x",
		},
		"at sign in the path of a url that parses keeps its spelling": {
			url:  "https://example.com:8443/pkg@1.0/s.json",
			want: "https://example.com:8443/pkg@1.0/s.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, httpfetch.Redacted(tc.url))
		})
	}
}

func TestIsHTTPURL(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		url  string
		want bool
	}{
		"http":                 {url: "http://example.com/s.json", want: true},
		"https":                {url: "https://example.com/s.json", want: true},
		"upper-case scheme":    {url: "HTTPS://example.com/s.json", want: true},
		"mixed-case scheme":    {url: "hTtP://example.com/s.json", want: true},
		"scheme alone":         {url: "https://", want: true},
		"file url":             {url: "file:///srv/s.json", want: false},
		"scheme without slash": {url: "https:example.com", want: false},
		"shorter than scheme":  {url: "http:/", want: false},
		"relative path":        {url: "schemas/s.json", want: false},
		"empty":                {url: "", want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, httpfetch.IsHTTPURL(tc.url))
		})
	}
}
