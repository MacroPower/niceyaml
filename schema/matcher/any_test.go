package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
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

	testCombinator(t, "matcher.Any", matcher.Any)
}

func TestAny_Describe(t *testing.T) {
	t.Parallel()

	var (
		kind       = paths.Doc().Child("kind")
		apiVersion = paths.Doc().Child("apiVersion")
		deployment = matcher.Content(kind, "Deployment")
		service    = matcher.Content(kind, "Service")
		appsV1     = matcher.Content(apiVersion, "apps/v1")
	)

	tcs := map[string]struct {
		matcher matcher.Matcher
		want    string
	}{
		"one matcher": {
			matcher: matcher.Any(deployment),
			want:    `$.kind is "Deployment"`,
		},
		"two matchers": {
			matcher: matcher.Any(deployment, service),
			want:    `$.kind is "Deployment" or $.kind is "Service"`,
		},
		"an All of two goes in parentheses": {
			matcher: matcher.Any(matcher.All(appsV1, deployment), service),
			want:    `($.apiVersion is "apps/v1" and $.kind is "Deployment") or $.kind is "Service"`,
		},
		"a nested Any of two goes in parentheses": {
			matcher: matcher.Any(matcher.Any(deployment, service), appsV1),
			want:    `($.kind is "Deployment" or $.kind is "Service") or $.apiVersion is "apps/v1"`,
		},
		"a described child keeps its own words": {
			matcher: matcher.Any(matcher.Described(matcher.All(appsV1, deployment), "a deployment"), service),
			want:    `a deployment or $.kind is "Service"`,
		},
		"a Func child has no description": {
			matcher: matcher.Any(deployment, undecided),
		},
		"no matchers": {
			matcher: matcher.Any(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, description(t, tc.matcher))
		})
	}
}
