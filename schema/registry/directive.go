package registry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

var (
	// ErrNoDirective indicates no schema directive was found in the document.
	// It wraps [schema.ErrNoMatch], so [Registry] moves on to the next
	// resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", schema.ErrNoMatch)

	// ErrNoFilePath indicates a document has no file path, which is required
	// for resolving relative schema paths in directives.
	ErrNoFilePath = errors.New("document has no file path")
)

// directiveResolver resolves schemas from yaml-language-server directives.
type directiveResolver struct {
	opts []loader.HTTPOption
}

// Directive creates a new [schema.Resolver] for yaml-language-server schema
// directives.
//
// Resolve parses the document's tokens for a directive comment and names
// the schema it references through [loader.FileOrURL], relative to the
// document's file path. A document without a directive reports
// [ErrNoDirective]; one without a file path reports [ErrNoFilePath].
//
//	reg.Register(registry.Directive())
func Directive(opts ...loader.HTTPOption) schema.Resolver {
	return &directiveResolver{opts: opts}
}

// Resolve implements [schema.Resolver].
func (r *directiveResolver) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (schema.Ref, error) {
	directive := schema.ParseDocumentDirective(doc.Tokens())
	if directive == nil {
		return schema.Ref{}, ErrNoDirective
	}

	filePath := doc.FilePath()
	if filePath == "" {
		return schema.Ref{}, ErrNoFilePath
	}

	//nolint:wrapcheck // Loader errors already carry the reference.
	return loader.FileOrURL(filepath.Dir(filePath), directive.Schema, r.opts...).Resolve(ctx, doc)
}
