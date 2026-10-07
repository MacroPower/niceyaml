package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Matcher determines whether a schema applies to a document.
//
// A Matcher guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When], which reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for a document the
// Matcher declines and returns the error of one that cannot decide, so a
// registry moves past the first and stops at the second.
//
// See [Content], [Text], [Exists], [FilePath], [Any], [All], and [Func]
// for implementations.
type Matcher interface {
	// Match reports whether the document satisfies the matcher. The error
	// is for a matcher that cannot decide, such as one whose context ended
	// or whose path an alias in the document leaves unresolved. A document
	// that reads as a plain no is false with no error.
	Match(ctx context.Context, doc *niceyaml.Node) (bool, error)
}

// Func adapts a function to the [Matcher] interface. The function below
// matches a null at enabled, which [Content] has no want for. A document
// without the path reads as a plain no, and so does a value the decoder
// rejects, so the function returns false with no error for each, as
// [Content] does:
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
