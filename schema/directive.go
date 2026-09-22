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
	// "yaml-language-server: $schema=./schema.json". The marker must open
	// the comment, after optional whitespace, so a comment that mentions
	// the marker mid-sentence is not a directive. The reference runs to
	// the first whitespace, as yaml-language-server reads it, so a remark
	// after the reference is not part of it.
	schemaDirectiveRE = regexp.MustCompile(`^\s*yaml-language-server:\s*\$schema=(\S+)`)

	// ErrNoDirective indicates a document has no schema directive.
	// It wraps [ErrNoMatch], so [Registry] moves on to the next resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", ErrNoMatch)

	// ErrNoFilePath indicates a document has no file path, which [Directive]
	// needs to resolve a relative schema path.
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
// The "yaml-language-server:" marker must open the comment, after any
// leading whitespace; a comment that mentions the marker after other text
// is not a directive. The reference is the text after "$schema=" up to the
// first whitespace, as yaml-language-server reads it, so a remark after
// the reference on the same line is not part of it, and a path cannot
// contain spaces. A directive with nothing after the equals sign yields
// nil.
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
// tokens, such as those [go.jacobcolvin.com/niceyaml.Node.Tokens]
// returns.
//
// The directive must appear before any non-comment content in the document;
// a document header (---) and a %YAML or %TAG directive line may precede
// it, and such a line may carry the directive as its trailing comment. The
// first directive wins. Returns nil when no directive appears before
// content.
func ParseDocumentDirective(tks token.Tokens) *ParsedDirective {
	// The parser splits a %YAML or %TAG line into a directive token and the
	// tokens holding its value, so the value reads as content unless the
	// scan skips the rest of the directive line with it.
	inDirective, directiveLine := false, 0

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		line, hasLine := tokenLine(tk)

		if tk.Type == token.DirectiveType {
			inDirective, directiveLine = hasLine, line

			continue
		}

		// A token the scan can place on a later line ends the directive
		// line. A token without a position leaves the line open, and the
		// scan reads it rather than skipping it.
		if hasLine && line != directiveLine {
			inDirective = false
		}

		// A comment on the directive line is no part of the directive's
		// value, so the scan reads it as a comment rather than skipping it.
		if inDirective && hasLine && tk.Type != token.CommentType {
			continue
		}

		switch tk.Type {
		case token.DocumentHeaderType, token.DocumentEndType:
			// A document marker is part of the preamble rather than
			// content, so the scan continues past it.
			continue

		case token.CommentType:
			directive := ParseDirective(tk.Value)
			if directive != nil {
				directive.Position = position.NewFromToken(tk)

				return directive
			}

		default:
			// The scan found content before any directive, so this
			// document has none.
			return nil
		}
	}

	return nil
}

// tokenLine returns the line tk sits on, and whether it carries a position
// to read the line from.
func tokenLine(tk *token.Token) (int, bool) {
	if tk == nil || tk.Position == nil {
		return 0, false
	}

	return tk.Position.Line, true
}

// directiveResolver resolves schemas from yaml-language-server directives.
type directiveResolver struct{}

// Directive creates a new [Resolver] for yaml-language-server schema
// directives.
//
// Resolve reads the directive comment from the document's
// [niceyaml.Node.Preamble] and names the schema it references through
// [FileOrURL]. It resolves a relative path against the directory of the
// document's file, so a document without a file path reports
// [ErrNoFilePath] for a relative path; a URL or an absolute path needs no
// file and resolves either way. A document without a directive reports
// [ErrNoDirective].
//
// The preamble holds the comments above the document's "---" header as
// well as those below it, so a directive written either way names the
// schema of the document it opens:
//
//	# yaml-language-server: $schema=./schema.json
//	---
//	key: value
//
// The first directive in the preamble wins. The registry fetches a
// directive that names a URL with the client [WithHTTPClient] gave it.
//
//	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))
func Directive() Resolver {
	return directiveResolver{}
}

// Resolve implements [Resolver].
func (directiveResolver) Resolve(_ context.Context, doc *niceyaml.Node) (Ref, error) {
	directive := ParseDocumentDirective(doc.Preamble())
	if directive == nil {
		return Ref{}, ErrNoDirective
	}

	var baseDir string

	if filePath := doc.FilePath(); filePath != "" {
		baseDir = filepath.Dir(filePath)
	}

	ref, err := FileOrURL(baseDir, directive.Schema)
	if errors.Is(err, ErrNoBaseDir) {
		return Ref{}, fmt.Errorf("%w: %w", ErrNoFilePath, err)
	}

	if err != nil {
		//nolint:wrapcheck // The reference error already names the reference.
		return Ref{}, err
	}

	return ref, nil
}
