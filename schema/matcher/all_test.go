package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
		"first only matches":  {matcher: deployments, input: `kind: Deployment`, filePath: "deploy/other/app.yaml"},
		"second only matches": {matcher: deployments, input: `kind: Service`, filePath: "deploy/k8s/app.yaml"},
		"none match":          {matcher: deployments, input: `kind: Service`, filePath: "deploy/other/app.yaml"},
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

	testCombinator(t, "matcher.All", matcher.All)
}
