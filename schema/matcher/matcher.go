package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Matcher determines whether a schema should be applied to a document.
//
// A Matcher guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema/registry.When].
//
// See [Content], [Exists], [FilePath], [Any], [All], and [Func] for
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
