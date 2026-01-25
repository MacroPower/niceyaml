package validator

import (
	"regexp"

	"github.com/goccy/go-yaml/token"

	"jacobcolvin.com/niceyaml/tokens"
)

// schemaDirectiveRE matches yaml-language-server schema directives.
// Example: yaml-language-server: $schema=./schema.json.
var schemaDirectiveRE = regexp.MustCompile(`yaml-language-server:\s*\$schema=(.+)`)

// Directive represents a parsed yaml-language-server schema directive.
//
// Create instances with [ParseDirective] or [ParseDocumentDirectives].
type Directive struct {
	// Position is the position of the comment containing the directive.
	Position *token.Position

	// Schema is the schema path extracted from the directive.
	// This may be a file path or URL.
	Schema string
}

// ParseDirective extracts a schema directive from comment text.
// Returns nil if the comment doesn't contain a schema directive.
//
// The comment text should not include the '#' prefix.
// Example input: " yaml-language-server: $schema=./schema.json".
func ParseDirective(comment string) *Directive {
	matches := schemaDirectiveRE.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return nil
	}

	return &Directive{
		Schema: matches[1],
	}
}

// DocumentDirectives maps document indices to their schema [Directive]s.
//
// Create instances with [ParseDocumentDirectives].
type DocumentDirectives map[int]*Directive

// ParseDocumentDirectives extracts schema directives for each document in a
// token stream.
//
// Associates comments with documents based on position relative to document
// headers (---) and non-comment content.
//
// A schema directive must appear before any non-comment content in its document
// to be associated with that document. Comments appearing after content are
// ignored.
//
// For multi-document streams, each document header (---) starts a new document
// context.
//
// The first document (index 0) may not have an explicit header.
func ParseDocumentDirectives(tks token.Tokens) DocumentDirectives {
	directives := make(DocumentDirectives)

	for docIdx, docTokens := range tokens.SplitDocuments(tks) {
		directive := parseDocumentDirective(docTokens)
		if directive != nil {
			directives[docIdx] = directive
		}
	}

	return directives
}

// parseDocumentDirective extracts a schema directive from a single document's
// tokens.
//
// Returns nil if no directive is found before content.
func parseDocumentDirective(tks token.Tokens) *Directive {
	for _, tk := range tks {
		switch tk.Type {
		case token.DocumentHeaderType:
			// Skip document header, continue looking for directive.
			continue

		case token.CommentType:
			directive := ParseDirective(tk.Value)
			if directive != nil {
				directive.Position = tk.Position

				return directive
			}

		default:
			// Content found before directive, no directive for this document.
			return nil
		}
	}

	return nil
}
