package yamltest_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestNormalizerFunc(t *testing.T) {
	t.Parallel()

	var _ finder.Normalizer = yamltest.NormalizerFunc(nil)

	var receivedInput string

	n := yamltest.NormalizerFunc(func(in string) string {
		receivedInput = in
		return "custom-" + in
	})

	result := n.Normalize("test")

	assert.Equal(t, "test", receivedInput)
	assert.Equal(t, "custom-test", result)
}

func TestRoundTripFunc(t *testing.T) {
	t.Parallel()

	var received *http.Request

	client := &http.Client{
		Transport: yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			received = r

			return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody, Request: r}, nil
		}),
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/schema.json", http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusTeapot, resp.StatusCode)
	assert.Equal(t, "http://example.com/schema.json", received.URL.String())
}
