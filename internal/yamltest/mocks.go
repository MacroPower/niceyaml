package yamltest

import "net/http"

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
