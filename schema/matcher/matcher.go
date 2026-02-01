package matcher

import (
	"context"

	"jacobcolvin.com/niceyaml"
)

// Matcher determines whether a schema should be applied to a document.
//
// Matchers are evaluated in registration order; first match wins.
//
// See [Content], [FilePath], [Any], [All], [Always], and [Func] for
// implementations.
type Matcher interface {
	// Match returns true if the matcher's criteria are satisfied by the document.
	Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool
}

// Func adapts a function to the [Matcher] interface.
type Func func(ctx context.Context, doc *niceyaml.DocumentDecoder) bool

// Match implements [Matcher].
func (f Func) Match(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
	return f(ctx, doc)
}
