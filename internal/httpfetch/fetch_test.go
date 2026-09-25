package httpfetch_test

import (
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
	// of the error.
	tcs := map[string]struct {
		url string
	}{
		"invalid port":           {url: "https://user:secret@example.com:port/x"},
		"invalid percent escape": {url: "https://user:secret@example.com/%zz"},
		"control character":      {url: "https://user:secret@example.com/\x7f"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := httpfetch.Get(t.Context(), http.DefaultClient, tc.url)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, httpfetch.Redacted(tc.url))
		})
	}
}
