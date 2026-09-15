package schema

import (
	"regexp"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/position"
)

// schemaDirectiveRE matches yaml-language-server schema directives.
// Example: yaml-language-server: $schema=./schema.json.
var schemaDirectiveRE = regexp.MustCompile(`yaml-language-server:\s*\$schema=(.+)`)

// Directive represents a parsed yaml-language-server schema directive.
//
// Create instances with [ParseDirective] or [ParseDocumentDirective].
type Directive struct {
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
func ParseDirective(comment string) *Directive {
	matches := schemaDirectiveRE.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return nil
	}

	ref := strings.TrimSpace(matches[1])
	if ref == "" {
		return nil
	}

	return &Directive{
		Schema: ref,
	}
}

// ParseDocumentDirective extracts a schema directive from a single document's
// tokens, such as those [go.jacobcolvin.com/niceyaml.DocumentDecoder.Tokens]
// returns.
//
// The directive must appear before any non-comment content in the document;
// a document header (---) may precede it. The first directive wins. Returns
// nil if no directive is found before content.
func ParseDocumentDirective(tks token.Tokens) *Directive {
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
