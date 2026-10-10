package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAll(t *testing.T) {
	t.Parallel()

	deployments := matcher.All(
		matcher.Content(kindPath, "Deployment"),
		matcher.MustFilePath("**/k8s/*.yaml"),
	)

	tcs := map[string]struct {
		matcher  matcher.Matcher
		input    string
		filePath string
		want     bool
	}{
		"all match": {
			matcher:  deployments,
			input:    `kind: Deployment`,
			filePath: "deploy/k8s/app.yaml",
			want:     true,
		},
		"first only matches": {
			matcher:  deployments,
			input:    `kind: Deployment`,
			filePath: "deploy/other/app.yaml",
			want:     false,
		},
		"second only matches": {
			matcher:  deployments,
			input:    `kind: Service`,
			filePath: "deploy/k8s/app.yaml",
			want:     false,
		},
		"none match": {
			matcher:  deployments,
			input:    `kind: Service`,
			filePath: "deploy/other/app.yaml",
			want:     false,
		},
		"empty matchers (vacuous truth)": {
			matcher: matcher.All(),
			input:   `kind: Deployment`,
			want:    true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocumentWithPath(t, tc.input, tc.filePath)

			assert.Equal(t, tc.want, match(t, tc.matcher, doc))
		})
	}

	t.Run("error ends the evaluation", func(t *testing.T) {
		t.Parallel()

		called := false
		m := matcher.All(undecided, recording(&called))
		doc := yamltest.FirstDocument(t, `kind: Deployment`)

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, errUndecided)
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
		doc := yamltest.FirstDocument(t, `kind: Service`)

		assert.False(t, match(t, m, doc))
	})
}
