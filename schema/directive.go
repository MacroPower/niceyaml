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

// The JavaScript \s, which yaml-language-server reads a directive with,
// matches \v and the Unicode spaces, such as U+00A0 and U+3000, where the
// RE2 \s matches only ASCII. The patterns below spell that set out.
const (
	// The characters the JavaScript \s matches, other than \r and \n.
	jsBlank = `\t\v\f \p{Z}\x{FEFF}`
	// The characters the JavaScript \s matches.
	jsSpace = `\r\n` + jsBlank
)

var (
	// Pattern of the marker that opens a schema directive after optional
	// whitespace. The marker is "yaml-language-server:", with optional
	// whitespace before the colon, or the IntelliJ "$schema:" short form.
	// A comment that mentions a marker mid-sentence is not a directive.
	modelineRE = regexp.MustCompile(`^[` + jsSpace + `]*(?:yaml-language-server[` + jsSpace + `]*:|\$schema:)`)

	// Pattern of the schema reference in a directive, after "$schema=" or
	// "$schema:". Like yaml-language-server, ParseDirective searches the
	// whole comment for it, so other settings may come before it. The
	// reference runs to the first whitespace, so a remark after the
	// reference is not part of it.
	schemaValueRE = regexp.MustCompile(`\$schema(?:=|:[` + jsBlank + `]*)([^` + jsSpace + `]+)`)

	// ErrNoDirective indicates a document has no schema directive.
	// It wraps [ErrNoMatch], so [Registry] moves on to the next resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", ErrNoMatch)

	// ErrNoFilePath indicates a document has no file path, which [Directive]
	// needs to resolve a relative schema path.
	ErrNoFilePath = errors.New("document has no file path")

	// Schema that a "$schema=none" directive names. It passes every
	// document without decoding it, so the directive turns validation off.
	noneSchema = &Schema{compiled: MustCompile([]byte("true")).compiled, acceptAll: true}
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
// The marker is "yaml-language-server:", with optional whitespace before
// the colon, or the IntelliJ "$schema:" short form. The marker must open
// the comment, after any leading whitespace; a comment that mentions the
// marker after other text is not a directive. The reference follows
// "$schema=" or "$schema:" anywhere in the comment, so other settings may
// precede it. It runs to the first whitespace, as yaml-language-server
// reads it, and that includes a Unicode space such as U+00A0. A remark
// after the reference on the same line is not part of it, and a path
// cannot contain spaces. A directive that names no reference yields nil.
func ParseDirective(comment string) *ParsedDirective {
	if !modelineRE.MatchString(comment) {
		return nil
	}

	matches := schemaValueRE.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return nil
	}

	return &ParsedDirective{
		Schema: matches[1],
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
// A fragment selects a subschema on a path as it does on a URL, so
// "$schema=./schema.json#/definitions/Foo" names the Foo definition in
// schema.json, as it does in yaml-language-server. A '#' that opens the
// reference is part of the file name.
//
// A directive of "$schema=none", in any letter case, turns validation off
// for the document, as it does in yaml-language-server. Resolve names a
// schema that passes every document without decoding it, so the lookup
// ends at the directive, and the document needs no file path.
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
//
// The author of the document picks the file or URL the directive names,
// so a directive can name any file the registry can read, a path outside
// the document's directory included, and any host the client can reach.
// A program that validates documents from another trust domain confines
// the registry. [WithFS] with the file system of an [os.Root] restricts
// file reads to one directory tree, and a client whose Transport or
// CheckRedirect restricts hosts on every hop bounds the fetch. Neither
// limits how many schemas documents can name. The registry keeps every
// schema it compiles, so documents that name endless distinct URLs on an
// allowed host, such as one URL with a new query string each time, grow
// its cache without bound. A long-running program that validates such
// documents builds a registry per batch or request. A program that trusts
// no directive at all leaves Directive out of the resolvers.
func Directive() Resolver {
	return directiveResolver{}
}

// Resolve implements [Resolver].
func (directiveResolver) Resolve(_ context.Context, doc *niceyaml.Node) (Ref, error) {
	directive := ParseDocumentDirective(doc.Preamble())
	if directive == nil {
		return Ref{}, ErrNoDirective
	}

	// Only the bare token turns validation off, as in yaml-language-server;
	// "./none" and "none.json" still name files.
	if strings.EqualFold(directive.Schema, "none") {
		return noneSchema.Ref(), nil
	}

	var baseDir string

	if filePath := doc.FilePath(); filePath != "" {
		baseDir = filepath.Dir(filePath)
	}

	// Like yaml-language-server, read a '#' after the first character of a
	// path as the start of a fragment that names a subschema, rather than
	// as part of the file name. [FileOrURL] splits the fragment off a URL
	// itself.
	path, fragment := directive.Schema, ""
	if !isHTTPURL(path) && !isFileURL(path) {
		if i := strings.Index(path, "#"); i > 0 {
			path, fragment = path[:i], path[i+1:]
		}
	}

	ref, err := FileOrURL(baseDir, path)
	if errors.Is(err, ErrNoBaseDir) {
		return Ref{}, fmt.Errorf("%w: %w", ErrNoFilePath, err)
	}

	if err != nil {
		//nolint:wrapcheck // The reference error already names the reference.
		return Ref{}, err
	}

	if fragment != "" {
		ref.key += "#" + fragment
	}

	return ref, nil
}
