package yamltest

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// NormalizerFunc adapts a function to the
// [go.jacobcolvin.com/niceyaml/finder.Normalizer] interface.
type NormalizerFunc func(in string) string

// Normalize implements [go.jacobcolvin.com/niceyaml/finder.Normalizer].
func (f NormalizerFunc) Normalize(in string) string {
	return f(in)
}

// RoundTripFunc adapts a function to the [http.RoundTripper] interface,
// so a test answers the requests of an [http.Client] itself.
type RoundTripFunc func(r *http.Request) (*http.Response, error)

// RoundTrip implements [http.RoundTripper].
func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Client returns an [*http.Client] that sends every request to f.
func (f RoundTripFunc) Client() *http.Client {
	return &http.Client{Transport: f}
}

// Respond returns a [RoundTripFunc] that answers every request with
// status and body. It opens no socket, so it works inside a synctest
// bubble and no other process can reach it.
func Respond(status int, body string) RoundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	}
}

// CountingClient returns an [*http.Client] that sends every request
// through rt, along with a count of the requests it has sent.
func CountingClient(rt http.RoundTripper) (*http.Client, *atomic.Int32) {
	var requests atomic.Int32

	client := RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)

		return rt.RoundTrip(r)
	}).Client()

	return client, &requests
}

// RespondBody returns a [RoundTripFunc] that answers a request with
// status 200 and the bytes body reads, so a test serves a body that fails
// partway or that runs past a limit. Every response reads from the one
// body, so a second request gets what the first left unread.
func RespondBody(body io.Reader) RoundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(body),
			Request:    r,
		}, nil
	}
}
