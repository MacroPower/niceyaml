package httpfetch_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

func TestGet_RedactsPassword(t *testing.T) {
	t.Parallel()

	// Get cannot redact a URL that does not parse, so it keeps the URL out
	// of the error and sends no request. A password that starts with "/",
	// "?" or "#" parses, but as an empty port and then a path, query or
	// fragment, so Get refuses it too.
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				t.Errorf("Get sent a request to host %q", r.URL.Host)

				return nil, errors.New("unexpected request")
			})}

			_, err := httpfetch.Get(t.Context(), client, tc.url)
			require.Error(t, err)
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

func TestGet_ParseReason(t *testing.T) {
	t.Parallel()

	// A URL with no password keeps the reason url.Parse gives.
	_, err := httpfetch.Get(t.Context(), http.DefaultClient, "https://example.com:port/x")
	require.ErrorContains(t, err, `parse URL: invalid port ":port" after host`)
}

func TestGet_RedactsPasswordOnTransport(t *testing.T) {
	t.Parallel()

	// A closed server refuses the connection, and the transport quotes the
	// URL as written in its error. Get names the URL once, redacted.
	server := httptest.NewServer(http.NotFoundHandler())

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	server.Close()

	u.User = url.UserPassword("user", "secret")

	_, err = httpfetch.Get(t.Context(), server.Client(), u.String())
	require.Error(t, err)
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
