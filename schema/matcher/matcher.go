package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Matcher determines whether a schema should be applied to a document.
//
// A Matcher guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When], which reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for a document the
// Matcher declines and returns the error of one that cannot decide, so a
// registry moves past the first and stops at the second.
//
// See [Content], [Exists], [FilePath], [Any], [All], and [Func] for
// implementations.
type Matcher interface {
	// Match reports whether the document satisfies the matcher. The error
	// is for a matcher that cannot decide, such as one whose context ended
	// or whose path an alias in the document leaves unresolved; a document
	// that reads as a plain no is false with no error.
	Match(ctx context.Context, doc *niceyaml.Document) (bool, error)
}

// Func adapts a function to the [Matcher] interface.
//
//	kindPath := paths.Root().Child("kind")
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Document) (bool, error) {
//	    kind, err := doc.Get[string](ctx, kindPath)
//	    if errors.Is(err, paths.ErrNotFound) {
//	        return false, nil
//	    }
//
//	    if err != nil {
//	        return false, err
//	    }
//
//	    return strings.HasPrefix(kind, "Custom"), nil
//	})
type Func func(ctx context.Context, doc *niceyaml.Document) (bool, error)

// Match implements [Matcher].
func (f Func) Match(ctx context.Context, doc *niceyaml.Document) (bool, error) {
	return f(ctx, doc)
}
