package schema

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// Pattern of a yaml-language-server schema directive, such as
	// "yaml-language-server: $schema=./schema.json".
	schemaDirectiveRE = regexp.MustCompile(`yaml-language-server:\s*\$schema=(.+)`)

	// ErrNoDirective indicates no schema directive was found in the document.
	// It wraps [ErrNoMatch], so [Registry] moves on to the next resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", ErrNoMatch)

	// ErrNoFilePath indicates a document has no file path, which is required
	// for resolving relative schema paths in directives.
	ErrNoFilePath = errors.New("document has no file path")
)

// ParsedDirective is a yaml-language-server schema directive read from a
// comment. [Directive] is the resolver that names the schema it references.
//
// Create instances with [ParseDirective] or [ParseDocumentDirective].
type ParsedDirective struct {
	// Schema is the schema path extracted from the directive.
	// This may be a file path or URL.
	Schema string

	// Position is the 0-indexed position of the comment holding the
	// directive. [ParseDocumentDirective] fills it from the comment token;
	// [ParseDirective] reads a bare string and leaves it at the zero value.
	Position position.Position
}

// ParseDirective extracts a schema directive from comment text.
// Returns nil if the comment doesn't contain a schema directive.
//
// The comment text should not include the '#' prefix.
// Example input: " yaml-language-server: $schema=./schema.json".
//
// The YAML parser keeps trailing spaces in a comment's value, so
// ParseDirective trims whitespace around the schema reference. A directive
// with nothing after the equals sign yields nil.
func ParseDirective(comment string) *ParsedDirective {
	matches := schemaDirectiveRE.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return nil
	}

	ref := strings.TrimSpace(matches[1])
	if ref == "" {
		return nil
	}

	return &ParsedDirective{
		Schema: ref,
	}
}

// ParseDocumentDirective extracts a schema directive from a single document's
// tokens, such as those [go.jacobcolvin.com/niceyaml.Document.Tokens]
// returns.
//
// The directive must appear before any non-comment content in the document;
// a document header (---) may precede it. The first directive wins. Returns
// nil if no directive is found before content.
func ParseDocumentDirective(tks token.Tokens) *ParsedDirective {
	for _, tk := range tks {
		switch tk.Type {
		case token.DocumentHeaderType:
			// Skip document header, continue looking for directive.
			continue

		case token.CommentType:
			directive := ParseDirective(tk.Value)
			if directive != nil {
				directive.Position = position.NewFromToken(tk)

				return directive
			}

		default:
			// Content found before directive, no directive for this document.
			return nil
		}
	}

	return nil
}

// directiveResolver resolves schemas from yaml-language-server directives.
type directiveResolver struct {
	opts []HTTPOption
}

// Directive creates a new [Resolver] for yaml-language-server schema
// directives.
//
// Resolve parses the document's tokens for a directive comment and names
// the schema it references through [FileOrURL]. A relative path is
// resolved against the directory of the document's file, so a document
// without a file path reports [ErrNoFilePath] for a relative path; a URL or
// an absolute path needs no file and resolves either way. A document
// without a directive reports [ErrNoDirective].
//
//	reg.Register(schema.Directive())
func Directive(opts ...HTTPOption) Resolver {
	return &directiveResolver{opts: opts}
}

// Resolve implements [Resolver].
func (r *directiveResolver) Resolve(ctx context.Context, doc *niceyaml.Document) (Ref, error) {
	directive := ParseDocumentDirective(doc.Tokens())
	if directive == nil {
		return Ref{}, ErrNoDirective
	}

	var baseDir string

	if filePath := doc.FilePath(); filePath != "" {
		baseDir = filepath.Dir(filePath)
	}

	ref, err := FileOrURL(baseDir, directive.Schema, r.opts...).Resolve(ctx, doc)
	if errors.Is(err, ErrNoBaseDir) {
		return Ref{}, fmt.Errorf("%w: %w", ErrNoFilePath, err)
	}

	//nolint:wrapcheck // Loader errors already carry the reference.
	return ref, err
}
