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

func TestAll(t *testing.T) {
	t.Parallel()

	k8sPattern := matcher.MustFilePath("**/k8s/*.yaml")

	t.Run("all match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			k8sPattern,
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Deployment`), "deploy/k8s/app.yaml")

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("first only matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			k8sPattern,
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Deployment`), "deploy/other/app.yaml")

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("second only matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			k8sPattern,
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Service`), "deploy/k8s/app.yaml")

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("none match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			k8sPattern,
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Service`), "deploy/other/app.yaml")

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("empty matchers (vacuous truth)", func(t *testing.T) {
		t.Parallel()

		m := matcher.All()
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("error ends the evaluation", func(t *testing.T) {
		t.Parallel()

		undecided := errors.New("undecided")
		called := false
		m := matcher.All(
			matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
				return false, undecided
			}),
			matcher.Func(func(_ context.Context, _ *niceyaml.Node) (bool, error) {
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

		assert.PanicsWithValue(t, "matcher.All: matcher at index 0 is nil", func() {
			matcher.All(matcher.Func(nil))
		})
		assert.PanicsWithValue(t, "matcher.All: matcher at index 1 is nil", func() {
			matcher.All(matcher.Content(kindPath, "Test"), nil)
		})
		assert.PanicsWithValue(t, "matcher.All: matcher at index 1 is nil", func() {
			matcher.All(matcher.Exists(kindPath), (*pointerMatcher)(nil))
		})
	})

	t.Run("writing to the caller's slice changes nothing", func(t *testing.T) {
		t.Parallel()

		ms := []matcher.Matcher{matcher.Content(kindPath, "Deployment")}
		m := matcher.All(ms...)
		ms[0] = matcher.Content(kindPath, "Service")
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))

		got := match(t, m, doc)
		assert.False(t, got)
	})
}
