package matcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Compile-time interface satisfaction check and path helpers for tests.
var (
	_ matcher.Matcher = matcher.Func(nil)

	kindPath       = paths.Root().Child("kind").Path()
	apiVersionPath = paths.Root().Child("apiVersion").Path()
	metadataName   = paths.Root().Child("metadata").Child("name").Path()
	missingPath    = paths.Root().Child("missing").Path()
)

func TestFunc(t *testing.T) {
	t.Parallel()

	called := false
	m := matcher.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) bool {
		called = true

		return true
	})

	doc := yamltest.FirstDocument(t, "kind: Test")
	got := m.Match(t.Context(), doc)

	assert.True(t, called)
	assert.True(t, got)
}
