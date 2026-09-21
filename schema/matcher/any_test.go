package matcher_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAny(t *testing.T) {
	t.Parallel()

	t.Run("first matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("second matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("none match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: ConfigMap`))

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("empty matchers", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any()
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("error ends the evaluation", func(t *testing.T) {
		t.Parallel()

		undecided := errors.New("undecided")
		called := false
		m := matcher.Any(
			matcher.Func(func(_ context.Context, _ *niceyaml.Document) (bool, error) {
				return false, undecided
			}),
			matcher.Func(func(_ context.Context, _ *niceyaml.Document) (bool, error) {
				called = true

				return true, nil
			}),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, undecided)
		assert.False(t, called)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Any: matcher at index 0 is nil", func() {
			matcher.Any(nil, matcher.Content(kindPath, "Test"))
		})
	})

	t.Run("writing to the caller's slice changes nothing", func(t *testing.T) {
		t.Parallel()

		ms := []matcher.Matcher{matcher.Content(kindPath, "Deployment")}
		m := matcher.Any(ms...)
		ms[0] = matcher.Content(kindPath, "Service")
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))

		got := match(t, m, doc)
		assert.False(t, got)
	})
}
