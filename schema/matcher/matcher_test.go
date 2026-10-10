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

// Compile-time interface satisfaction check, path helpers, and a matcher
// for tests.
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

	// The error undecided reports.
	errUndecided = errors.New("undecided")

	// A matcher that cannot decide, and reports errUndecided.
	undecided = matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) {
		return false, errUndecided
	})
)

// pointerMatcher is a [matcher.Matcher] with a pointer receiver, so a nil
// *pointerMatcher is a non-nil interface value holding a nil pointer.
type pointerMatcher struct{}

func (*pointerMatcher) Match(context.Context, *niceyaml.Node) (bool, error) {
	return true, nil
}

// recording returns a [matcher.Matcher] that matches every document and
// sets called when it runs.
func recording(called *bool) matcher.Matcher {
	return matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) {
		*called = true

		return true, nil
	})
}

// match runs m on doc and fails the test when the matcher cannot decide.
func match(t *testing.T, m matcher.Matcher, doc *niceyaml.Node) bool {
	t.Helper()

	got, err := m.Match(t.Context(), doc)
	require.NoError(t, err)

	return got
}

// requireCanceled runs m with a context that ended. Such a matcher cannot
// decide, so it must return the error of the context rather than a match,
// and the registry stops at the document.
func requireCanceled(t *testing.T, m matcher.Matcher) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ok, err := m.Match(ctx, yamltest.FirstDocument(t, `kind: Deployment`))
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
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

		m := matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
			return false, errUndecided
		})

		doc := yamltest.FirstDocument(t, "kind: Test")
		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, errUndecided)
	})
}

func TestFunc_Null(t *testing.T) {
	t.Parallel()

	// The function the doc of matcher.Func shows, which matches a null.
	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
		var value any

		found, err := doc.DecodeIfPresent(ctx, enabledPath, &value)
		if errors.Is(err, niceyaml.ErrDecode) {
			return false, nil
		}

		if err != nil {
			//nolint:wrapcheck // The Node binds the error already.
			return false, err
		}

		return found && value == nil, nil
	})

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"null":                      {input: `enabled: null`, want: true},
		"tilde":                     {input: `enabled: ~`, want: true},
		"no value":                  {input: `enabled:`, want: true},
		"alias to a null":           {input: "n: &n null\nenabled: *n", want: true},
		"false":                     {input: `enabled: false`},
		"empty string":              {input: `enabled: ""`},
		"str-tagged null":           {input: `enabled: !!str null`},
		"empty mapping":             {input: `enabled: {}`},
		"missing path":              {input: `kind: Test`},
		"value the decoder rejects": {input: `enabled: !!int x`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, m, doc))
		})
	}
}
