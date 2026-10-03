package matcher_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Compile-time interface satisfaction check and path helpers for tests.
var (
	_ matcher.Matcher = matcher.Func(nil)

	kindPath       = paths.Current().Child("kind")
	apiVersionPath = paths.Current().Child("apiVersion")
	metadataName   = paths.Current().Child("metadata").Child("name")
	missingPath    = paths.Current().Child("missing")
	versionPath    = paths.Current().Child("version")
	twoKeyPath     = paths.Current().Child("2").Key()
	enabledPath    = paths.Current().Child("enabled")
	timeoutPath    = paths.Current().Child("timeout")
	createdPath    = paths.Current().Child("created")
)

// pointerMatcher is a [matcher.Matcher] with a pointer receiver, so a nil
// *pointerMatcher is a non-nil interface value holding a nil pointer.
type pointerMatcher struct{}

func (*pointerMatcher) Match(context.Context, *niceyaml.Node) (bool, error) {
	return true, nil
}

// match runs m on doc and fails the test when the matcher cannot decide.
func match(t *testing.T, m matcher.Matcher, doc *niceyaml.Node) bool {
	t.Helper()

	got, err := m.Match(t.Context(), doc)
	require.NoError(t, err)

	return got
}

func TestFunc(t *testing.T) {
	t.Parallel()

	t.Run("reports the result of the function", func(t *testing.T) {
		t.Parallel()

		called := false
		m := matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
			called = true

			return true, nil
		})

		doc := yamltest.FirstDocument(t, "kind: Test")
		got := match(t, m, doc)

		assert.True(t, called)
		assert.True(t, got)
	})

	t.Run("passes the error of the function through", func(t *testing.T) {
		t.Parallel()

		undecided := errors.New("undecided")
		m := matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
			return false, undecided
		})

		doc := yamltest.FirstDocument(t, "kind: Test")
		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, undecided)
	})
}
