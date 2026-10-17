package matcher

import (
	"context"
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// Matcher determines whether a schema applies to a document.
//
// A Matcher guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When]. The guarded resolver reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for a document that the
// Matcher declines, so a registry moves on to its next resolver. It
// returns the error of a Matcher that cannot decide, so the registry stops
// at the document.
//
// A Matcher can describe the documents that it accepts by implementing
// [Describer]. [go.jacobcolvin.com/niceyaml/schema.When] gives that
// description as its reason when the Matcher declines a document. So the
// error of a registry that no resolver applies to says what each guard
// expected. Each matcher of this package describes itself, except
// [Func]. [Described] gives any Matcher a description.
//
// See [Content], [Text], [Exists], [FilePath], [Any], [All], [Described],
// and [Func] for implementations.
type Matcher interface {
	// Match reports whether the document satisfies the matcher. Match
	// returns an error only when it cannot decide, such as when its context
	// ended or when an alias in the document leaves its path unresolved.
	// For a document that does not match, Match returns false and a nil
	// error.
	Match(ctx context.Context, doc *niceyaml.Node) (bool, error)
}

// Func adapts a function to the [Matcher] interface.
//
// The example Func matches a null at enabled, which [Content] does not
// match for any want. Like [Content], it returns false and a nil error for
// a document without the path and for a value that the decoder rejects:
//
//	enabledPath := paths.Doc().Child("enabled")
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
//	    var value any
//
//	    found, err := doc.DecodeIfPresent(ctx, enabledPath, &value)
//	    if errors.Is(err, niceyaml.ErrDecode) {
//	        return false, nil
//	    }
//
//	    if err != nil {
//	        return false, err
//	    }
//
//	    return found && value == nil, nil
//	})
//
// A Func does not have a description, so
// [go.jacobcolvin.com/niceyaml/schema.When] declines without a reason for
// it. Wrap it in [Described] to give it one.
type Func func(ctx context.Context, doc *niceyaml.Node) (bool, error)

// Match implements [Matcher].
func (f Func) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	return f(ctx, doc)
}

// Describer describes the documents that a [Matcher] accepts.
// [go.jacobcolvin.com/niceyaml/schema.When] reads the description of its
// Matcher through Describer and gives it as the reason of each decline,
// as the Descriptions section of the package documentation describes.
//
// See [Content] for an implementation.
type Describer interface {
	// Describe returns the condition that each document the Matcher
	// accepts meets, as a clause in the present tense such as
	// `$.kind is "Deployment"`. It returns "" for a Matcher that does not
	// have a description.
	Describe() string
}

// describe returns the description of m, or "" when m does not implement
// [Describer].
func describe(m Matcher) string {
	d, ok := m.(Describer)
	if !ok {
		return ""
	}

	return d.Describe()
}

// describeJoined returns the descriptions of matchers joined by
// conjunction, such as " and ". It puts the description of a child in
// parentheses when that child joins two or more descriptions of its own
// and stands beside another child. It returns "" when matchers is empty
// or when a child does not have a description.
func describeJoined(matchers []Matcher, conjunction string) string {
	if len(matchers) == 0 {
		return ""
	}

	parts := make([]string, 0, len(matchers))

	for _, m := range matchers {
		desc := describe(m)
		if desc == "" {
			return ""
		}

		if len(matchers) > 1 && joins(m) {
			desc = "(" + desc + ")"
		}

		parts = append(parts, desc)
	}

	return strings.Join(parts, conjunction)
}

// joins reports whether the description of m joins two or more
// descriptions, as that of an [All] or an [Any] with two or more
// matchers does. An All or Any with one matcher describes itself as that
// matcher does.
func joins(m Matcher) bool {
	var children []Matcher

	switch c := m.(type) {
	case *allMatcher:
		children = c.matchers
	case *anyMatcher:
		children = c.matchers
	default:
		return false
	}

	if len(children) == 1 {
		return joins(children[0])
	}

	return len(children) > 1
}
