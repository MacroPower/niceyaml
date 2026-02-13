package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAll(t *testing.T) {
	t.Parallel()

	k8sPattern := filepaths.MustPattern("**/k8s/*.yaml")

	t.Run("all match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.FilePath(k8sPattern),
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Deployment`), "deploy/k8s/app.yaml")

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("first only matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.FilePath(k8sPattern),
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Deployment`), "deploy/other/app.yaml")

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("second only matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.FilePath(k8sPattern),
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Service`), "deploy/k8s/app.yaml")

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("none match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.FilePath(k8sPattern),
		)
		doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`kind: Service`), "deploy/other/app.yaml")

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("empty matchers (vacuous truth)", func(t *testing.T) {
		t.Parallel()

		m := matcher.All()
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.All: matcher at index 1 is nil", func() {
			matcher.All(matcher.Always(), nil)
		})
	})
}
