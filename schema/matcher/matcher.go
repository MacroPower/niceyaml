package matcher

import (
	"context"

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
// See [Content], [Text], [Exists], [FilePath], [Any], [All], and [Func]
// for implementations.
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
type Func func(ctx context.Context, doc *niceyaml.Node) (bool, error)

// Match implements [Matcher].
func (f Func) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	return f(ctx, doc)
}
