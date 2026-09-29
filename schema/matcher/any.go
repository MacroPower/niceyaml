package matcher

import (
	"context"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

// anyMatcher matches if any sub-matcher matches (OR logic).
type anyMatcher struct {
	matchers []Matcher
}

// Any creates a new [Matcher] that matches if ANY sub-matcher matches (OR
// logic). Evaluation short-circuits on the first matching matcher, and the
// first matcher that returns an error ends it with that error.
//
// Returns false when the caller passes no matchers.
//
// Panics if any matcher is nil, including a nil pointer or a nil [Func].
// The matcher keeps its own copy of matchers, so writing to the caller's
// slice afterwards changes nothing.
//
// Use Any to match several document types with the same schema:
//
//	kindPath := paths.Root().Child("kind")
//	matcher.Any(
//	    matcher.Content(kindPath, "Deployment"),
//	    matcher.Content(kindPath, "StatefulSet"),
//	    matcher.Content(kindPath, "DaemonSet"),
//	)
func Any(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if nilness.IsNil(m) {
			panic(fmt.Sprintf("matcher.Any: matcher at index %d is nil", i))
		}
	}

	return &anyMatcher{matchers: slices.Clone(matchers)}
}

// Match implements [Matcher].
func (m *anyMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	for _, matcher := range m.matchers {
		ok, err := matcher.Match(ctx, doc)
		if err != nil || ok {
			//nolint:wrapcheck // The sub-matcher's error is this matcher's own.
			return ok, err
		}
	}

	return false, nil
}
