package schema

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/position"
)

// The JavaScript \s matches \v and the Unicode spaces, such as U+00A0 and
// U+3000, and yaml-language-server reads a directive with it. The RE2 \s
// matches only ASCII. The constants in this block spell the JavaScript
// set out.
const (
	// The characters that the JavaScript \s matches, other than \r and
	// \n.
	jsBlank = `\t\v\f \p{Z}\x{FEFF}`
	// The characters that the JavaScript \s matches.
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
	// whole comment for it, so other settings can come before it. The
	// reference runs to the first whitespace, so a remark after the
	// reference is not part of it.
	schemaValueRE = regexp.MustCompile(`\$schema(?:=|:[` + jsBlank + `]*)([^` + jsSpace + `]+)`)

	// ErrNoDirective indicates that a document does not have a schema
	// directive. It wraps [ErrNoMatch], so [Registry] tries the next
	// resolver.
	ErrNoDirective = fmt.Errorf("%w: no schema directive", ErrNoMatch)

	// ErrNoFilePath indicates that a document does not have a file path.
	// [Directive] resolves a relative schema path against the directory of
	// that file path.
	ErrNoFilePath = errors.New("document has no file path")

	// The schema that a "$schema=none" directive names. It passes every
	// document without decoding it, so the directive turns validation off.
	noneSchema = &Schema{acceptAll: true}
)

// ParsedDirective is a yaml-language-server schema directive read from a
// comment. [Directive] is the resolver that names the schema that it
// references.
//
// Create instances with [ParseDirective] or [ParseDocumentDirective].
type ParsedDirective struct {
	// Schema is the schema reference that the directive holds, a file
	// path or a URL.
	Schema string

	// Position is the 0-indexed position of the comment that holds the
	// directive. [ParseDocumentDirective] fills it from the comment token.
	// [ParseDirective] reads a bare string and leaves it at the zero value.
	Position position.Position
}

// ParseDirective extracts a schema directive from comment text. It
// returns nil when the comment does not hold a schema directive.
//
// Pass the comment text without its '#' prefix, as in
// " yaml-language-server: $schema=./schema.json".
//
// The marker is "yaml-language-server:", with optional whitespace before
// the colon, or the IntelliJ "$schema:" short form. The marker must open
// the comment, after any leading whitespace. A comment that mentions the
// marker after other text is not a directive.
//
// The reference follows "$schema=" or "$schema:" anywhere in the
// comment, so other settings can precede it. The reference runs to the
// first whitespace, as yaml-language-server reads it, and that whitespace
// includes Unicode spaces such as U+00A0. A remark after the reference on
// the same line is not part of it, and a path cannot contain spaces.
// ParseDirective returns nil for a directive that does not name a
// reference.
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

// ParseDocumentDirective extracts a schema directive from the tokens of
// a single document, such as those that
// [go.jacobcolvin.com/niceyaml.Node.Tokens] returns.
//
// The directive must appear before any non-comment content in the
// document. A document header (---), a document end marker (...), and a
// %YAML or %TAG directive line can precede it. Each of those lines can
// carry the directive as its trailing comment. The first directive wins.
// ParseDocumentDirective returns nil when no directive appears before
// content.
func ParseDocumentDirective(tks token.Tokens) *ParsedDirective {
	for _, tk := range tks[:preamble.Len(tks)] {
		if tk == nil || tk.Type != token.CommentType {
			continue
		}

		directive := ParseDirective(tk.Value)
		if directive != nil {
			directive.Position = position.NewFromToken(tk)

			return directive
		}
	}

	return nil
}

// directiveResolver resolves schemas from yaml-language-server directives.
type directiveResolver struct{}

// Directive creates a new [Resolver] for yaml-language-server schema
// directives.
//
// Resolve reads the directive comment from the document's
// [niceyaml.Node.Preamble] and names the schema that it references
// through [RefBeside]. It resolves a relative path against the directory
// of the document's file. For a relative path, Resolve therefore reports
// [ErrNoFilePath] when the document does not have a file path. It reports
// [ErrNoBaseDir] when the document has a relative file path and does not
// have a file system, as RefBeside describes. A URL or an absolute path
// does not need a file path and resolves either way. Resolve reports
// [ErrNoDirective] for a document without a directive.
//
// The schema lives where the document does. A document that
// [niceyaml.NewSourceFromFile] opened names a file on disk. A document
// that [niceyaml.NewSourceFromFS] opened, or one whose source
// [niceyaml.WithFS] gave a file system, names a file in that file
// system. The registry never reads the disk for such a document. An
// absolute path does not name a file there and fails to load with
// [fs.ErrInvalid]. One registry thus validates bundled documents and
// documents on disk, each against the schema beside it.
//
// A fragment selects a subschema on a path as it does on a URL, and as it
// does in yaml-language-server. The directive
// "$schema=./schema.json#/definitions/Foo" names the Foo definition in
// schema.json. A '#' that opens the reference is part of the file name.
//
// A directive of "$schema=none", in any letter case, turns validation off
// for the document, as it does in yaml-language-server. Resolve names a
// schema that passes every document without decoding it. The lookup thus
// ends at the directive, and the document does not need a file path.
//
// The preamble holds the comments above the "---" header of the document
// and those below it. A directive in either place names the schema of
// the document that it opens:
//
//	# yaml-language-server: $schema=./schema.json
//	---
//	key: value
//
// The first directive in the preamble wins. The registry fetches the URL
// that a directive names with the client that [WithHTTPClient] gave it.
//
//	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))
//
// The author of the document picks the file or URL that the directive
// names. A directive can therefore name any file that the registry can
// read, including a path outside the document's directory. It can also
// name any host that the client can reach. A program that validates
// documents from another trust domain gives Directive a registry of its
// own, and confines that registry with [WithAccess] and
// [WithCacheLimit]. That registry is a resolver of the registry that
// holds the program's own schemas, as [Registry.Resolve] describes:
//
//	untrusted := schema.NewRegistry(
//	    schema.WithAccess(access),
//	    schema.WithCacheLimit(1024),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
//	reg := schema.NewRegistry(schema.WithResolvers(untrusted, schemastore.New()))
//
// The Untrusted Documents section of the package documentation gives the
// whole recipe. [AllowRoot] confines a document on disk, whether
// [niceyaml.NewSourceFromFile] read it or the program built it from
// bytes with an absolute file path. A program that does not trust any
// directive leaves Directive out of the resolvers.
//
// The path that a directive names can hold control characters. The
// message of each error from a read of the file draws them as pictures.
// A document thus cannot start a line of its own or write to a terminal
// through the path. [errors.As] still finds the error of the file
// system, whose own text holds the path as the directive wrote it.
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

	// Like yaml-language-server, read a '#' after the first character of a
	// path as the start of a fragment that names a subschema, rather than
	// as part of the file name. [FileOrURL] splits the fragment off a URL
	// itself.
	path, fragment := directive.Schema, ""
	if !httpfetch.IsHTTPURL(path) && !isFileURL(path) {
		if i := strings.Index(path, "#"); i > 0 {
			path, fragment = path[:i], path[i+1:]
		}
	}

	ref, err := RefBeside(doc, path)
	if errors.Is(err, ErrNoBaseDir) && doc.FilePath() == "" {
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
