package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

	testCombinator(t, "matcher.Any", matcher.Any)
}
