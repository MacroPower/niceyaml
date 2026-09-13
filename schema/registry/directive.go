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
	// It wraps [ErrNoMatch], so [Registry] moves on to the next resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", ErrNoMatch)

	// ErrNoFilePath indicates a document has no file path, which is required
	// for resolving relative schema paths in directives.
	ErrNoFilePath = errors.New("document has no file path")
)

// directiveResolver resolves schemas from yaml-language-server directives.
type directiveResolver struct {
	opts []loader.HTTPOption
}

// Directive creates a new [Resolver] for yaml-language-server schema
// directives.
//
// Resolve parses the document's tokens for a directive comment and loads the
// schema it names, relative to the document's file path. A document without
// a directive reports [ErrNoDirective].
//
//	reg.Register(registry.Directive())
func Directive(opts ...loader.HTTPOption) Resolver {
	return &directiveResolver{opts: opts}
}

// Resolve implements [Resolver].
func (r *directiveResolver) Resolve(ctx context.Context, doc *niceyaml.DocumentDecoder) (loader.Result, error) {
	directive := schema.ParseDocumentDirective(doc.Tokens())
	if directive == nil {
		return loader.Result{}, ErrNoDirective
	}

	filePath := doc.FilePath()
	if filePath == "" {
		return loader.Result{}, ErrNoFilePath
	}

	baseDir := filepath.Dir(filePath)

	//nolint:wrapcheck // Loader errors already wrapped with context.
	return loader.Ref(baseDir, directive.Schema, r.opts...).Load(ctx, doc)
}
