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
// See [Embedded], [File], [URL], [Validator], [Ref], [Custom], and [Func]
// for implementations.
type Loader interface {
	// Load returns schema data or a pre-compiled validator for the document.
	Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error)
}

// Func adapts a function to the [Loader] interface.
type Func func(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error)

// Load implements [Loader].
func (f Func) Load(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error) {
	return f(ctx, doc)
}

// Result contains the output of a [Loader].
//
// A Result must provide either Validator or Data:
//   - If Validator is set, it is used directly and Data is ignored.
//   - If Validator is nil, Data must contain schema bytes for compilation.
//
// Use [NewResult] or [NewResultWithValidator] to construct valid Results.
// These constructors enforce the invariant that either Validator or Data must
// be set.
//
// URL identifies the schema for caching. When URL is empty, the registry skips
// caching entirely, so each validation compiles the schema fresh. Built-in
// loaders always set URL appropriately.
type Result struct {
	// Validator is an optional pre-compiled validator.
	// If set, Data is ignored and the validator is used directly.
	// Both pre-compiled and compiled validators are cached by URL.
	Validator niceyaml.SchemaValidator

	// URL identifies the schema for caching.
	// When empty, the registry skips caching and compiles fresh each time.
	// Built-in loaders always set this field.
	URL string

	// Data contains the schema bytes for compilation.
	// Ignored if Validator is set. Required if Validator is nil.
	Data []byte
}

// NewResult creates a [Result] with schema data for compilation.
//
// Panics if data is empty, since a Result must have either Validator or Data.
func NewResult(url string, data []byte) Result {
	if len(data) == 0 {
		panic("loader.NewResult: data is required when validator is nil")
	}

	return Result{Data: data, URL: url}
}

// NewResultWithValidator creates a [Result] with a pre-compiled validator.
//
// Panics if v is nil, since a Result must have either Validator or Data.
func NewResultWithValidator(url string, v niceyaml.SchemaValidator) Result {
	if v == nil {
		panic("loader.NewResultWithValidator: validator is required")
	}

	return Result{Validator: v, URL: url}
}
