package matcher

import (
	"context"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
)

// allMatcher matches if all sub-matchers match (AND logic).
type allMatcher struct {
	matchers []Matcher
}

// All creates a new [Matcher] that matches if ALL sub-matchers match (AND
// logic). Evaluation short-circuits on the first non-matching matcher.
//
// Returns true if no matchers are provided.
//
// Panics if any matcher is nil.
//
//	// Matches YAML files in k8s directories with kind: Deployment.
//	matcher.All(
//	    matcher.FilePath(filepaths.MustPattern("**/k8s/*.yaml")),
//	    matcher.Content(paths.Root().Child("kind").Path(), "Deployment"),
//	)
func All(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if m == nil {
			panic(fmt.Sprintf("matcher.All: matcher at index %d is nil", i))
		}
	}

	return &allMatcher{matchers: matchers}
}

// Match implements [Matcher].
func (m *allMatcher) Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
	for _, matcher := range m.matchers {
		if !matcher.Match(ctx, doc) {
			return false
		}
	}

	return true
}
