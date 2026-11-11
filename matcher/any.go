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
// logic). Match calls the sub-matchers in order and stops at the first one
// that matches or that returns an error. It returns the error of that
// sub-matcher.
//
// When matchers is empty, the matcher does not match any document.
//
// Any panics if any matcher is nil, including a nil pointer or a nil
// [Func]. The matcher keeps its own copy of matchers, so a later write to
// the caller's slice does not change it.
//
// Use Any to match several document types with the same schema:
//
//	kindPath := paths.Doc().Child("kind")
//	matcher.Any(
//	    matcher.Content(kindPath, "Deployment"),
//	    matcher.Content(kindPath, "StatefulSet"),
//	    matcher.Content(kindPath, "DaemonSet"),
//	)
//
// The Matcher describes itself through [Describer] by joining the
// descriptions of its matchers with "or", so the example reads
// `$.kind is "Deployment" or $.kind is "StatefulSet" or $.kind is
// "DaemonSet"`. A matcher that joins two or more descriptions of its own,
// such as an [All] of two matchers, goes in parentheses. The description
// is "" when matchers is empty, or when one of them does not have a
// description, such as a [Func]. [Described] gives such a matcher a
// description.
func Any(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if nilness.IsNil(m) {
			panic(fmt.Sprintf("matcher.Any: matcher at index %d is nil", i))
		}
	}

	return &anyMatcher{matchers: slices.Clone(matchers)}
}

// Describe implements [Describer].
func (m *anyMatcher) Describe() string {
	return describeJoined(m.matchers, " or ")
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
