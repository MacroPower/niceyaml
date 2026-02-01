package matcher

import (
	"context"
	"fmt"

	"jacobcolvin.com/niceyaml"
)

// anyMatcher matches if any sub-matcher matches (OR logic).
type anyMatcher struct {
	matchers []Matcher
}

// Any creates a new [Matcher] that matches if ANY sub-matcher matches (OR
// logic). Evaluation short-circuits on the first matching matcher.
//
// Returns false if no matchers are provided.
//
// Panics if any matcher is nil.
//
// This is useful for matching multiple document types with the same schema:
//
//	kindPath := paths.Root().Child("kind").Path()
//	matcher.Any(
//	    matcher.Content(kindPath, "Deployment"),
//	    matcher.Content(kindPath, "StatefulSet"),
//	    matcher.Content(kindPath, "DaemonSet"),
//	)
func Any(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if m == nil {
			panic(fmt.Sprintf("matcher.Any: matcher at index %d is nil", i))
		}
	}

	return &anyMatcher{matchers: matchers}
}

// Match implements [Matcher].
func (m *anyMatcher) Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
	for _, matcher := range m.matchers {
		if matcher.Match(ctx, doc) {
			return true
		}
	}

	return false
}
