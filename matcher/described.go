package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

// describedMatcher is a [Matcher] with a description in the words of its
// caller.
type describedMatcher struct {
	matcher     Matcher
	description string
}

// Described creates a new [Matcher] that matches the documents that m
// matches and describes them as description, through [Describer]. The
// description is the reason that [go.jacobcolvin.com/niceyaml/schema.When]
// gives when the Matcher declines a document. Write it as a clause in the
// present tense, so the reason reads "applies only when" and then the
// description:
//
//	isPod := matcher.Described(podMatcher, "the file holds a pod")
//
// Described gives a [Func] a description, which it does not have of its
// own. An [All] or an [Any] with such a matcher among its matchers then
// has a description too. Described also replaces the description of a
// matcher of this package, such as a [FilePath] whose absolute pattern
// names a directory that the reader of the error must not see.
//
// Described panics if m is nil, including a nil pointer or a nil [Func],
// or if description is empty.
func Described(m Matcher, description string) Matcher {
	if nilness.IsNil(m) {
		panic("matcher.Described: matcher is nil")
	}

	if description == "" {
		panic("matcher.Described: description is empty")
	}

	return &describedMatcher{matcher: m, description: description}
}

// Describe implements [Describer].
func (m *describedMatcher) Describe() string {
	return m.description
}

// Match implements [Matcher].
func (m *describedMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	//nolint:wrapcheck // The error of the matcher that Described wraps is its own.
	return m.matcher.Match(ctx, doc)
}
