package niceyaml

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Source is a YAML file: one stream of text that holds one or more YAML
// documents. It holds the tokens the text was lexed from, the [*ast.File]
// they parse into, and the settings for parsing, decoding, and reporting
// errors. [Source.Documents] returns each document in the file as a
// [*Document], and [Source.Document] returns the one document of a file
// that holds one, which is where decoding and validation live.
//
// Source separates two concerns. Parsing lives on Source itself, where
// [Source.File] lazily parses the AST and [Source.Documents] builds the
// documents. Every error they and their Documents produce comes back
// bound to the Source as a [SourceError]. [Document.Bind] binds errors
// built elsewhere to the document they were checked against, and
// [Source.Bind] binds one to the document its location falls in.
// Rendering lives in a [line.View], which carries the overlays, annotations, and
// flags that a [printer.Printer] renders over the [line.Lines] the Source
// holds. [Source.Lines] returns those lines, which the [finder.Finder] and
// [diff.Differ] read, and [Source.View] returns a fresh view over them for
// the [printer.Printer].
//
// Typical use creates a Source and renders a view of it:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	fmt.Println(p.Print(source.View()))
//
// A Source never changes after creation. Overlays and annotations go on the
// view, and a fresh view renders the document as parsed:
//
//	view := source.View()
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//	fmt.Println(p.Print(view))
//
// Since nothing mutates a Source, it is safe for concurrent use. Every view
// taken from it shares its lines and owns its decoration, so creating one
// costs nothing.
//
// Create instances with [NewSourceFromFile], [NewSourceFromBytes],
// [NewSourceFromString], or [NewSourceFromTokens].
type Source struct {
	name       string
	filePath   string
	lines      line.Lines
	file       *ast.File
	fileErr    error
	docs       []*Document
	parserOpts []parser.Option
	decodeOpts []yaml.DecodeOption
	fileOnce   sync.Once
	docsOnce   sync.Once
	// Accepts a mapping with the same key twice when parsing and decoding.
	allowDuplicateKeys bool
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithFilePath]
//   - [WithAllowDuplicateKeys]
//   - [WithYAMLParserOptions]
//
// Settings that only affect decoding, such as [WithDisallowUnknownFields],
// are [DecodeOption] values passed to [Document.Decode].
type SourceOption func(*Source)

// WithName is a [SourceOption] that sets the name for the [Source], which
// labels it in output such as diff headers. Without it, [Source.Name]
// returns the file path.
func WithName(name string) SourceOption {
	return func(s *Source) {
		s.name = name
	}
}

// WithFilePath is a [SourceOption] that sets the file path for the [Source].
// Each [Document] of the Source reports it from [Document.FilePath], which
// schema matchers route on.
//
// For file-based sources, use [NewSourceFromFile] which sets this
// automatically.
func WithFilePath(path string) SourceOption {
	return func(s *Source) {
		s.filePath = path
	}
}

// WithAllowDuplicateKeys is a [SourceOption] that sets whether a mapping may
// hold the same key twice, both when [Source.File] parses the document and
// when [Document] decodes it. When allowed, the last value wins. The default
// is false, and a duplicate key is then an error.
func WithAllowDuplicateKeys(allow bool) SourceOption {
	return func(s *Source) {
		s.allowDuplicateKeys = allow
	}
}

// WithYAMLParserOptions is a [SourceOption] that passes [parser.Option]
// values to the go-yaml parser when [Source.File] parses the document. It is
// the escape hatch for parser settings that have no option of their own;
// comments are always parsed.
func WithYAMLParserOptions(opts ...parser.Option) SourceOption {
	return func(s *Source) {
		s.parserOpts = append(s.parserOpts, opts...)
	}
}

// NewSourceFromFile creates a new [*Source] by reading a file from disk.
//
// The file path is set on the [Source], so each [Document] reports it for
// schema routing, and [Source.Name] returns it unless [WithName] sets a
// name.
//
// Returns an error if the file cannot be read.
func NewSourceFromFile(path string, opts ...SourceOption) (*Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Prepend file path option so user options can override if needed.
	opts = append([]SourceOption{WithFilePath(path)}, opts...)

	return NewSourceFromString(string(data), opts...), nil
}

// NewSourceFromBytes creates a new [*Source] from raw YAML bytes.
func NewSourceFromBytes(data []byte, opts ...SourceOption) *Source {
	return NewSourceFromString(string(data), opts...)
}

// NewSourceFromString creates a new [*Source] from a YAML string using
// [tokens.Tokenize].
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := tokens.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromTokens creates a new [*Source] from [token.Tokens].
// See [line.NewLines] for details on token splitting behavior.
//
// The Source holds clones of the tokens with their positions reset through
// [tokens.ResetPositions], so the text it holds counts its lines from 1 as
// [tokens.Tokenize] does, and line i of [Source.Lines] is line i+1 of the
// text. Tokens that count from 1 already, as a whole stream
// does, keep their positions. Tokens cut from a longer stream, as
// [Document.Tokens] hands out, are renumbered from the first one; to render
// one document of a file with the file's line numbers, print the file's
// view with [Document.Span] instead.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	if t.allowDuplicateKeys {
		t.parserOpts = append(t.parserOpts, parser.AllowDuplicateMapKey())
		t.decodeOpts = append(t.decodeOpts, yaml.AllowDuplicateMapKey())
	}

	t.lines = line.NewLines(tokens.ResetPositions(tks))

	return t
}

// Name returns the name of the [Source]: the one [WithName] set, or the
// file path when none was set. Returns an empty string when the Source has
// neither.
func (s *Source) Name() string {
	if s.name == "" {
		return s.filePath
	}

	return s.name
}

// FilePath returns the file path of the [Source].
//
// Returns an empty string if not set via [WithFilePath] or [NewSourceFromFile].
func (s *Source) FilePath() string {
	return s.filePath
}

// Tokens reconstructs the full [token.Tokens] stream from all [line.Line]s.
// See [line.Lines.Tokens] for details on token recombination behavior.
func (s *Source) Tokens() token.Tokens {
	return s.lines.Tokens()
}

// Documents returns the [*Document] values of this [Source], one per YAML
// document in file order.
//
// The parser cuts the comments and %YAML or %TAG directives above a "---"
// header, and the comments after a "..." marker, into a node of their own
// with no header and no content. The YAML spec attaches those to the
// document below or above them, so Documents folds each such node into
// that document, where [Document.Preamble] returns the tokens above the
// content. A document that opens with a "---" header and holds only
// comments is an explicit empty document and stays one.
//
// It parses the source and builds each Document once, so every call returns
// the same pointers. The slice itself is a copy, so reordering it reaches
// nothing.
//
// A YAML syntax error comes back bound to the Source. It is the same error
// [Source.File] returns.
func (s *Source) Documents() ([]*Document, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	s.docsOnce.Do(func() {
		s.docs = newDocuments(s, f)
	})

	return slices.Clone(s.docs), nil
}

// Document returns the [*Document] of a [Source] that holds a single YAML
// document, which is the direct path for a configuration file:
//
//	doc, err := niceyaml.NewSourceFromString(yamlContent).Document()
//	if err != nil {
//		return err
//	}
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//
// The comments above the first "---" are the preamble of the document
// below them rather than a document of their own, so a file that opens
// with a license header holds a single document, and a file of comments
// alone holds one that decodes to the zero value as an empty file does.
//
// When the file holds more than one document, it returns an error wrapping
// [ErrMultipleDocuments], bound to the Source and pointing at the header of
// the second document, or at its first token when a "..." marker rather
// than a header opens it. When the file holds no document at all, which
// happens for text that is only a "..." marker, it returns an error
// wrapping [ErrNoDocuments], bound to the Source. A file that does not
// parse returns the error [Source.File] returns. Use [Source.Documents]
// for a file that may hold several.
func (s *Source) Document() (*Document, error) {
	doc, err := s.single()
	if err != nil {
		return nil, s.Bind(err)
	}

	return doc, nil
}

// single returns the one document of the Source, or the reason it has
// none, unbound: the error [Source.File] returns, [ErrNoDocuments], or
// [ErrMultipleDocuments] at the anchor of the second document.
func (s *Source) single() (*Document, error) {
	docs, err := s.Documents()
	if err != nil {
		return nil, err
	}

	switch len(docs) {
	case 0:
		return nil, WrapError(ErrNoDocuments)

	case 1:
		return docs[0], nil

	default:
		return nil, WrapError(
			fmt.Errorf("%w: %d documents", ErrMultipleDocuments, len(docs)),
			atToken(docs[1].anchorToken()),
		)
	}
}

// anchorToken returns the token that locates the document: its header, or
// the first token of its text when it has no header, as a document after a
// "..." marker has none. It is nil when the document has neither.
func (dd *Document) anchorToken() *token.Token {
	if dd.doc.Start != nil {
		return dd.doc.Start
	}

	if len(dd.tokens) > 0 {
		return dd.tokens[0]
	}

	return nil
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The file is lazily parsed on first call using [parser.Parse] with options
// provided via [WithYAMLParserOptions]. Subsequent calls return the cached result.
//
// The tokens of the file are copies of the Source's own, since the parser
// relinks the tokens it is given. A copy matches the original by its type,
// value, origin, and position, so a token taken from a node finds its
// lines through [line.Lines.TokenRanges] and [line.Lines.ContentRanges] as
// the original does.
//
// A YAML syntax error comes back as a [*SourceError] bound to this Source,
// so the %+v verb renders it with the offending token marked.
func (s *Source) File() (*ast.File, error) {
	s.fileOnce.Do(func() {
		s.file, s.fileErr = s.parse()
	})

	return s.file, s.fileErr
}

// parse hands a private copy of the tokens to the parser. The go-yaml parser
// relinks Next and Prev while it moves comment tokens, and the Source's own
// tokens, which its lines and the caller share, stay untouched.
func (s *Source) parse() (*ast.File, error) {
	shared := s.Tokens()

	tks := make(token.Tokens, 0, len(shared))
	for _, tk := range shared {
		tks.Add(tk.Clone())
	}

	file, err := parser.Parse(tks, parser.ParseComments, s.parserOpts...)
	if err == nil {
		return file, nil
	}

	// The documents are built from the file this parse returns, so the
	// error binds to the source alone rather than routing to one of them.
	if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
		return nil, bindTree(WrapError(yamlMessageError{yamlErr}, atToken(yamlErr.GetToken())), binder{src: s})
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// Bind binds err to the [Source] and to the document each location in
// it falls in, so a caller that holds the source binds without picking a
// document. An error that carries a [position.Position] or a
// [position.Range], as a check that runs on [Source.Lines] produces, binds
// to the document whose [Document.Span] holds the line, whatever the
// file holds:
//
//	for i, ln := range source.Lines().All() {
//		if ln.Width() > 120 {
//			rng := position.NewRange(position.New(i, 120), position.New(i, ln.Width()))
//
//			return source.Bind(niceyaml.NewError("line exceeds 120 columns", niceyaml.WithRange(rng)))
//		}
//	}
//
// A path resolves in the one document of the source, the one
// [Source.Document] returns, so a check on a configuration file binds its
// findings here as it would through [Document.Bind]:
//
//	return source.Bind(check(cfg))
//
// A path in a source that holds several documents, or none, resolves
// nowhere: the bound error keeps its message and the name of the source,
// [SourceError.Range] returns [ErrPathNeedsDocument] wrapping the reason
// Source.Document gives, and the %+v verb names it in place of the
// excerpt. Bind such an error through [Document.Bind] with the document
// it was checked against, which also resolves a path from the scope of a
// Document from [Document.At].
//
// In every other way Bind is [Document.Bind], which describes what comes
// back. [SourceError.Document] returns the document each location fell
// in, and nil for an error whose location resolves in none.
func (s *Source) Bind(err error) error {
	return bindTree(err, binder{src: s, route: true})
}

// Lines returns the [line.Lines] of the [Source]: its tokens split into
// one line per line of text. Line i is line i+1 of the text, so
// [position.NewFromToken] converts any token of the Source to a position
// in the lines.
//
// The lines never change, so every call returns the same value and the
// [finder.Finder] and [diff.Differ] read it as it is. To render the
// Source, take a [line.View] from [Source.View].
func (s *Source) Lines() line.Lines {
	return s.lines
}

// View returns a new [*line.View] over [Source.Lines] with no decoration.
// Each call returns a view of its own, so overlays and annotations added to
// one never reach the Source or another view. Render the view to see them.
func (s *Source) View() *line.View {
	return line.NewView(s.lines)
}
