package matcher_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAny(t *testing.T) {
	t.Parallel()

	kinds := matcher.Any(
		matcher.Content(kindPath, "Deployment"),
		matcher.Content(kindPath, "Service"),
	)

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"first matches":  {matcher: kinds, input: `kind: Deployment`, want: true},
		"second matches": {matcher: kinds, input: `kind: Service`, want: true},
		"none match":     {matcher: kinds, input: `kind: ConfigMap`, want: false},
		"empty matchers": {matcher: matcher.Any(), input: `kind: Deployment`, want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, tc.matcher, doc))
		})
	}

	t.Run("error ends the evaluation", func(t *testing.T) {
		t.Parallel()

		undecided := errors.New("undecided")
		called := false
		m := matcher.Any(
			matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
				return false, undecided
			}),
			matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
				called = true

				return true, nil
			}),
		)
		doc := yamltest.FirstDocument(t, `kind: Deployment`)

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, undecided)
		assert.False(t, called)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Any: matcher at index 1 is nil", func() {
			matcher.Any(matcher.Exists(kindPath), matcher.Func(nil))
		})
		assert.PanicsWithValue(t, "matcher.Any: matcher at index 0 is nil", func() {
			matcher.Any(nil, matcher.Content(kindPath, "Test"))
		})
		assert.PanicsWithValue(t, "matcher.Any: matcher at index 0 is nil", func() {
			matcher.Any((*pointerMatcher)(nil), matcher.Exists(kindPath))
		})
	})

	t.Run("writing to the caller's slice changes nothing", func(t *testing.T) {
		t.Parallel()

		ms := []matcher.Matcher{matcher.Content(kindPath, "Deployment")}
		m := matcher.Any(ms...)
		ms[0] = matcher.Content(kindPath, "Service")
		doc := yamltest.FirstDocument(t, `kind: Service`)

		assert.False(t, match(t, m, doc))
	})
}
