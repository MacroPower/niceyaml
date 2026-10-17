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

func TestAll_Describe(t *testing.T) {
	t.Parallel()

	var (
		kind       = paths.Doc().Child("kind")
		deployment = matcher.Content(kind, "Deployment")
		k8s        = matcher.MustFilePath("**/k8s/*.yaml")
		named      = matcher.Exists(paths.Doc().Child("metadata").Child("name"))
		isPod      = matcher.Func(func(context.Context, *niceyaml.Node) (bool, error) { return true, nil })
	)

	tcs := map[string]struct {
		matcher matcher.Matcher
		want    string
	}{
		"one matcher": {
			matcher: matcher.All(deployment),
			want:    `$.kind is "Deployment"`,
		},
		"two matchers": {
			matcher: matcher.All(k8s, deployment),
			want:    `file path matches "**/k8s/*.yaml" and $.kind is "Deployment"`,
		},
		"an Any of two goes in parentheses": {
			matcher: matcher.All(k8s, matcher.Any(deployment, named)),
			want:    `file path matches "**/k8s/*.yaml" and ($.kind is "Deployment" or $.metadata.name exists)`,
		},
		"an Any of one does not": {
			matcher: matcher.All(k8s, matcher.Any(deployment)),
			want:    `file path matches "**/k8s/*.yaml" and $.kind is "Deployment"`,
		},
		"an All of one that holds an Any of two goes in parentheses": {
			matcher: matcher.All(k8s, matcher.All(matcher.Any(deployment, named))),
			want:    `file path matches "**/k8s/*.yaml" and ($.kind is "Deployment" or $.metadata.name exists)`,
		},
		"a Func child has no description": {
			matcher: matcher.All(k8s, isPod),
		},
		"a described Func child": {
			matcher: matcher.All(k8s, matcher.Described(isPod, "the file holds a pod")),
			want:    `file path matches "**/k8s/*.yaml" and the file holds a pod`,
		},
		"no matchers": {
			matcher: matcher.All(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, description(t, tc.matcher))
		})
	}
}
