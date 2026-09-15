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
// the schema it references through [loader.FileOrURL]. A relative path is
// resolved against the directory of the document's file, so a document
// without a file path reports [ErrNoFilePath] for a relative path; a URL or
// an absolute path needs no file and resolves either way. A document
// without a directive reports [ErrNoDirective].
//
//	reg.Register(registry.Directive())
func Directive(opts ...loader.HTTPOption) schema.Resolver {
	return &directiveResolver{opts: opts}
}

// Resolve implements [schema.Resolver].
func (r *directiveResolver) Resolve(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
	directive := schema.ParseDocumentDirective(doc.Tokens())
	if directive == nil {
		return schema.Ref{}, ErrNoDirective
	}

	var baseDir string

	if filePath := doc.FilePath(); filePath != "" {
		baseDir = filepath.Dir(filePath)
	}

	ref, err := loader.FileOrURL(baseDir, directive.Schema, r.opts...).Resolve(ctx, doc)
	if errors.Is(err, loader.ErrNoBaseDir) {
		return schema.Ref{}, fmt.Errorf("%w: %w", ErrNoFilePath, err)
	}

	//nolint:wrapcheck // Loader errors already carry the reference.
	return ref, err
}
