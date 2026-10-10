package yamltest_test

import (
	"io"
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

	client := yamltest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		received = r

		return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody, Request: r}, nil
	}).Client()

	status, _ := get(t, client, "http://example.com/schema.json")

	assert.Equal(t, http.StatusTeapot, status)
	assert.Equal(t, "http://example.com/schema.json", received.URL.String())
}

func TestRespond(t *testing.T) {
	t.Parallel()

	client := yamltest.Respond(http.StatusNotFound, "no such schema").Client()

	// Every request gets the body from its start.
	for range 2 {
		status, body := get(t, client, "http://example.com/schema.json")

		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "no such schema", body)
	}
}

func TestCountingClient(t *testing.T) {
	t.Parallel()

	client, requests := yamltest.CountingClient(yamltest.Respond(http.StatusOK, "{}"))
	assert.Equal(t, int32(0), requests.Load())

	for want := range int32(2) {
		status, body := get(t, client, "http://example.com/schema.json")

		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "{}", body)
		assert.Equal(t, want+1, requests.Load())
	}
}

// get sends a GET request for url through client and returns the status
// and the body of the response.
func get(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, string(body)
}
