package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

// notMatcher matches the documents that its matcher declines (NOT logic).
type notMatcher struct {
	matcher Matcher
}

// Not creates a new [Matcher] that matches each document that m declines
// (NOT logic). When m cannot decide, Match returns false and the error of
// m unchanged, so a registry stops at the document rather than routing
// it.
//
// Not accepts every document that m declines. [Content] and [Text]
// decline a document without the path, a node that is not a scalar, and
// a different value alike. So Not(Content(kind, "Service")) accepts a
// document that has no kind. Pair Not with [Exists] in an [All] to accept
// a document that has the field with another value:
//
//	kind := paths.Doc().Child("kind")
//	matcher.All(
//	    matcher.Exists(kind),
//	    matcher.Not(matcher.Content(kind, "Service")),
//	)
//
// Not also excludes a pattern from a [FilePath]:
//
//	matcher.All(
//	    matcher.MustFilePath("k8s/**"),
//	    matcher.Not(matcher.MustFilePath("k8s/vendor/**")),
//	)
//
// The Matcher describes itself through [Describer] as "not" and the
// description of m in parentheses, so Not(Exists(kind)) reads
// `not ($.kind exists)`. The parentheses keep the scope of "not" clear
// when an [All] or an [Any] joins the description with others. The
// description is "" when m does not have one, such as a [Func].
// [Described] gives the Matcher other words.
//
// Not panics if m is nil, including a nil pointer or a nil [Func].
func Not(m Matcher) Matcher {
	if nilness.IsNil(m) {
		panic("matcher.Not: matcher is nil")
	}

	return &notMatcher{matcher: m}
}

// Describe implements [Describer].
func (m *notMatcher) Describe() string {
	desc := describe(m.matcher)
	if desc == "" {
		return ""
	}

	return "not (" + desc + ")"
}

// Match implements [Matcher].
func (m *notMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	ok, err := m.matcher.Match(ctx, doc)
	if err != nil {
		//nolint:wrapcheck // The error of the matcher that Not negates is its own.
		return false, err
	}

	return !ok, nil
}
