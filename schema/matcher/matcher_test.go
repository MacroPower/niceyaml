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

	kindPath       = paths.Root().Child("kind")
	apiVersionPath = paths.Root().Child("apiVersion")
	metadataName   = paths.Root().Child("metadata").Child("name")
	missingPath    = paths.Root().Child("missing")
)

func TestFunc(t *testing.T) {
	t.Parallel()

	called := false
	m := matcher.Func(func(_ context.Context, _ *niceyaml.Document) bool {
		called = true

		return true
	})

	doc := yamltest.FirstDocument(t, "kind: Test")
	got := m.Match(t.Context(), doc)

	assert.True(t, called)
	assert.True(t, got)
}
