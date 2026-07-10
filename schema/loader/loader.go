package loader

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Loader loads schema data for validation.
//
// All loaders receive the document for consistency, though static loaders
// may ignore it.
//
// See [Embedded], [File], [URL], [Ref], and [Func] for implementations.
type Loader interface {
	// Load returns schema data for the document.
	Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error)
}

// Func adapts a function to the [Loader] interface.
type Func func(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error)

// Load implements [Loader].
func (f Func) Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error) {
	return f(ctx, doc)
}

// Result contains the output of a [Loader]: the schema bytes to compile and
// the URL identifying them.
//
// URL identifies the schema for caching. When URL is empty, the registry skips
// caching entirely, so each validation compiles the schema fresh. Built-in
// loaders always set URL appropriately.
type Result struct {
	// URL identifies the schema for caching.
	// When empty, the registry skips caching and compiles fresh each time.
	// Built-in loaders always set this field.
	URL string

	// Data contains the schema bytes for compilation.
	Data []byte
}
