package matcher

import (
	"context"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

// allMatcher matches if all sub-matchers match (AND logic).
type allMatcher struct {
	matchers []Matcher
}

// All creates a new [Matcher] that matches if ALL sub-matchers match (AND
// logic). Match calls the sub-matchers in order and stops at the first one
// that does not match or that returns an error. It returns the error of
// that sub-matcher.
//
// When matchers is empty, the matcher matches every document.
//
// All panics if any matcher is nil, including a nil pointer or a nil
// [Func]. The matcher keeps its own copy of matchers, so a later write to
// the caller's slice does not change it.
//
//	// Matches YAML files in k8s directories with kind: Deployment.
//	matcher.All(
//	    matcher.MustFilePath("**/k8s/*.yaml"),
//	    matcher.Content(paths.Doc().Child("kind"), "Deployment"),
//	)
//
// The Matcher describes itself through [Describer] by joining the
// descriptions of its matchers with "and", so the example reads
// `file path matches "**/k8s/*.yaml" and $.kind is "Deployment"`. A
// matcher that joins two or more descriptions of its own, such as an
// [Any] of two matchers, goes in parentheses. The description is "" when
// matchers is empty, or when one of them does not have a description,
// such as a [Func]. [Described] gives such a matcher a description.
func All(matchers ...Matcher) Matcher {
	for i, m := range matchers {
		if nilness.IsNil(m) {
			panic(fmt.Sprintf("matcher.All: matcher at index %d is nil", i))
		}
	}

	return &allMatcher{matchers: slices.Clone(matchers)}
}

// Describe implements [Describer].
func (m *allMatcher) Describe() string {
	return describeJoined(m.matchers, " and ")
}

// Match implements [Matcher].
func (m *allMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	for _, matcher := range m.matchers {
		ok, err := matcher.Match(ctx, doc)
		if err != nil || !ok {
			//nolint:wrapcheck // The sub-matcher's error is this matcher's own.
			return false, err
		}
	}

	return true, nil
}
