package matcher

import (
	"context"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
)

// allMatcher matches if all sub-matchers match (AND logic).
type allMatcher struct {
	matchers []Matcher
}

// All creates a new [Matcher] that matches if ALL sub-matchers match (AND
// logic). Evaluation short-circuits on the first non-matching matcher, and
// the first matcher that returns an error ends it with that error.
//
// Returns true when the caller passes no matchers.
//
// Panics if any matcher is nil. The matcher keeps its own copy of
// matchers, so writing to the caller's slice afterwards changes nothing.
//
//	// Matches YAML files in k8s directories with kind: Deployment.
//	matcher.All(
//	    matcher.MustFilePath("**/k8s/*.yaml"),
//	    matcher.Content(paths.Root().Child("kind"), "Deployment"),
//	)
func All(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if isNil(m) {
			panic(fmt.Sprintf("matcher.All: matcher at index %d is nil", i))
		}
	}

	return &allMatcher{matchers: slices.Clone(matchers)}
}

// Match implements [Matcher].
func (m *allMatcher) Match(ctx context.Context, doc *niceyaml.Document) (bool, error) {
	for _, matcher := range m.matchers {
		ok, err := matcher.Match(ctx, doc)
		if err != nil || !ok {
			//nolint:wrapcheck // The sub-matcher's error is this matcher's own.
			return false, err
		}
	}

	return true, nil
}
