package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAny(t *testing.T) {
	t.Parallel()

	t.Run("first matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("second matches", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Service`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("none match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(kindPath, "Service"),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: ConfigMap`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("empty matchers", func(t *testing.T) {
		t.Parallel()

		m := matcher.Any()
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Any: matcher at index 0 is nil", func() {
			matcher.Any(nil, matcher.Always())
		})
	})
}
