package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// Source is a YAML file, one stream of text that holds one or more YAML
// documents. It holds the tokens lexed from the text, the [*ast.File]
// they parse into, and the settings for parsing and for reporting
// errors. [Source.Documents] returns the root [*Node] of each document in
// the file, and [Source.Document] returns the root of the one document of
// a file that holds one, which is where decoding and validation live.
//
// Source separates two concerns. Parsing lives on Source itself, where [Source.File]
// lazily parses the AST and [Source.Documents] builds the documents. Every error they
// and their Nodes produce comes back bound to the Source as a [SourceError].
// [Node.Bind] binds errors built elsewhere to the document they were checked
// against, and [Source.Bind] binds one to the document its location falls in. Rendering
// lives in a [line.View], which carries the overlays, annotations, and flags that a
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders over the [line.Lines] the
// Source holds. [Source.Lines] returns those lines, which the
// [go.jacobcolvin.com/niceyaml/finder.Finder] and
// [go.jacobcolvin.com/niceyaml/diff.Differ] read, and [Source.View] returns a fresh
// view over them for the [go.jacobcolvin.com/niceyaml/printer.Printer].
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
// costs an index of the lines and no copy of their content.
//
// Create instances with [NewSourceFromFile], [NewSourceFromFS],
// [NewSourceFromReader], [NewSourceFromBytes], [NewSourceFromString], or
// [NewSourceFromTokens].
type Source struct {
	name       string
	filePath   string
	lines      line.Lines
	file       *ast.File
	fileErr    error
	docs       []*Node
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
// are [DecodeOption] values passed to [Node.Decode], or to [NewDecoder]
// for a [Decoder] that decodes every document with them.
type SourceOption func(*Source)

// WithName is a [SourceOption] that sets the name for the [Source], which
// [SourceError.Error] puts in front of the position of every error bound
// to it, as "name:line:col: msg". Without it, [Source.Name] returns the
// file path.
func WithName(name string) SourceOption {
	return func(s *Source) {
		s.name = name
	}
}

// WithFilePath is a [SourceOption] that sets the file path for the [Source].
// Each document of the Source reports it from [Node.FilePath], which
// schema matchers route on.
//
// For file-based sources, [NewSourceFromFile] and [NewSourceFromFS] set
// this automatically.
func WithFilePath(path string) SourceOption {
	return func(s *Source) {
		s.filePath = path
	}
}

// WithAllowDuplicateKeys is a [SourceOption] that sets whether a mapping may
// hold the same key twice, both when [Source.File] parses the document and
// when a [Node] decodes it. When allowed, the last value wins. The default
// is false, and a duplicate key is then an error.
func WithAllowDuplicateKeys(allow bool) SourceOption {
	return func(s *Source) {
		s.allowDuplicateKeys = allow
	}
}

// WithYAMLParserOptions is a [SourceOption] that passes [parser.Option]
// values to the go-yaml parser when [Source.File] parses the document. It is
// the escape hatch for parser settings that have no option of their own;
// the parser always parses comments.
func WithYAMLParserOptions(opts ...parser.Option) SourceOption {
	return func(s *Source) {
		s.parserOpts = append(s.parserOpts, opts...)
	}
}

// NewSourceFromFile creates a new [*Source] by reading a file from disk.
//
// It sets the file path on the [Source], so each document reports it for
// schema routing, and [Source.Name] returns it unless [WithName] sets a
// name. [NewSourceFromFS] reads a file from an [fs.FS] the same way.
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

// NewSourceFromFS creates a new [*Source] by reading the file at path
// from fsys, such as an [embed.FS] that ships configuration with the
// binary or an [fs.FS] a test builds. It sets the path on the [Source] as
// [NewSourceFromFile] does, so each document reports it for schema
// routing and a schema directive resolves relative to it in the same
// file system, through the registry option
// [go.jacobcolvin.com/niceyaml/schema.WithFS]:
//
//	source, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
//
// Returns an error if the file cannot be read.
func NewSourceFromFS(fsys fs.FS, path string, opts ...SourceOption) (*Source, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return NewSourceFromBytes(data, append([]SourceOption{WithFilePath(path)}, opts...)...), nil
}

// NewSourceFromReader creates a new [*Source] by reading r to its end,
// such as standard input or the body of an HTTP request. The Source has
// no file path unless [WithFilePath] sets one, and [WithName] names it in
// output:
//
//	source, err := niceyaml.NewSourceFromReader(os.Stdin, niceyaml.WithName("<stdin>"))
//
// Returns an error if r cannot be read.
func NewSourceFromReader(r io.Reader, opts ...SourceOption) (*Source, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	return NewSourceFromBytes(data, opts...), nil
}

// NewSourceFromBytes creates a new [*Source] from raw YAML bytes.
func NewSourceFromBytes(data []byte, opts ...SourceOption) *Source {
	return NewSourceFromString(string(data), opts...)
}

// NewSourceFromString creates a new [*Source] from a YAML string using
// [tokens.Tokenize]. The Source holds the text without the byte order
// marks Tokenize drops, so no key reads a mark as part of its text.
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
// [Node.Tokens] hands out, are renumbered from the first one; to render
// one document of a file with the file's line numbers, print the file's
// view with [Node.Span] instead.
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
// Returns an empty string unless [WithFilePath], [NewSourceFromFile], or
// [NewSourceFromFS] sets it.
func (s *Source) FilePath() string {
	return s.filePath
}

// Tokens reconstructs the full [token.Tokens] stream from all [line.Line]s.
// See [line.Lines.Tokens] for details on token recombination behavior. The
// tokens are the ones [tokens.Tokenize] returned for the text, so the Line
// and Column of each name the rune where its text starts.
func (s *Source) Tokens() token.Tokens {
	return s.lines.Tokens()
}

// Documents returns the root [*Node] of each YAML document of this
// [Source], in file order.
//
// The parser cuts the comments and %YAML or %TAG directives above a "---"
// header, and the comments after a "..." marker, into a node of their own
// with no header and no content. Documents folds each such node into the
// document below it, or into the last document when no document follows,
// and [Node.Preamble] returns the tokens it put above the content. A
// document that opens with a "---" header and holds only comments is an
// explicit empty document and stays one.
//
// It parses the source and builds each Node once, so every call returns
// the same pointers. The slice itself is a copy, so reordering it reaches
// nothing.
//
// A YAML syntax error comes back bound to the Source. It is the same error
// [Source.File] returns.
func (s *Source) Documents() ([]*Node, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	s.docsOnce.Do(func() {
		s.docs = newDocuments(s, f)
	})

	return slices.Clone(s.docs), nil
}

// Document returns the root [*Node] of a [Source] that holds a single
// YAML document, for a caller that reads a configuration file in more
// than one step, such as one that scopes a Node with [Node.At] or binds
// a check with [Node.Bind]. [Source.Decode] decodes that document in
// one step:
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
// the second document, or at the first token of its content when a "..."
// marker rather than a header opens it. When the file holds no document at
// all, which happens for text that is only a "..." marker, it returns an
// error wrapping [ErrNoDocuments], bound to the Source. A file that does
// not parse returns the error [Source.File] returns. Use [Source.Documents]
// for a file that may hold several.
func (s *Source) Document() (*Node, error) {
	doc, err := s.single()
	if err != nil {
		return nil, s.Bind(err)
	}

	return doc, nil
}

// DecodeInto validates and decodes the one document of the [Source] into
// v, as [Node.DecodeInto] decodes the root Node [Source.Document]
// returns. A Source that holds more than one document, or none, returns
// the error Source.Document returns, so a configuration file that must
// hold one document decodes in one step and reports a second document
// as the error it is.
func (s *Source) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, err := s.Document()
	if err != nil {
		return err
	}

	return doc.DecodeInto(ctx, v, opts...)
}

// Decode validates and decodes the one document of the [Source] into a
// new T, as [Node.Decode] decodes the root Node [Source.Document]
// returns, which is the direct path for a configuration file:
//
//	source, err := niceyaml.NewSourceFromFile(path)
//	if err != nil {
//		return err
//	}
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// A Source that holds more than one document, or none, returns the error
// Source.Document returns. On error, the returned T is the zero value.
func (s *Source) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := s.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// single returns the one document of the Source, or the reason it has
// none, unbound: the error [Source.File] returns, [ErrNoDocuments], or
// [ErrMultipleDocuments] at the anchor of the second document.
func (s *Source) single() (*Node, error) {
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
			atToken(docs[1].doc.anchorToken()),
		)
	}
}

// anchorToken returns the token that locates the document: its header, or
// the first token of its content when it has no header, as a document after
// a "..." marker has none, past the comments folded above it. It is nil
// when the document has neither.
func (d *document) anchorToken() *token.Token {
	if d.root.Start != nil {
		return d.root.Start
	}

	if d.preamble < len(d.tokens) {
		return d.tokens[d.preamble]
	}

	if len(d.tokens) > 0 {
		return d.tokens[0]
	}

	return nil
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The first call parses the file with [parser.Parse] and the options
// [WithYAMLParserOptions] provides. Subsequent calls return the cached
// result. A "---" header that directly follows another starts a document
// of its own, where [parser.Parse] alone drops the rest of the stream.
//
// The tokens of the file are copies of the Source's own, since the parser
// relinks the tokens it is given. A copy matches the original by its type,
// value, origin, and position, so a token taken from a node finds its
// lines through [line.Lines.TokenRanges] and [line.Lines.ContentRanges] as
// the original does.
//
// The tree is shared with every [Node] of the Source, and [Node.At],
// [Node.Ranges], and every error binding resolve against it, so it is
// read-only. A caller that modifies it corrupts the positions those
// resolve to and races with any concurrent use of the Source. A caller
// that edits a document parses a tree of its own, or edits the text and
// builds a new Source from the result.
//
// A YAML syntax error comes back as a [*SourceError] bound to this Source,
// so [FormatError] renders it with the offending token marked.
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

	file := &ast.File{Docs: []*ast.DocumentNode{}}

	for _, run := range splitConsecutiveHeaders(tks) {
		f, err := parser.Parse(run, parser.ParseComments, s.parserOpts...)
		if err == nil {
			file.Docs = append(file.Docs, f.Docs...)

			continue
		}

		// The documents come from the file this parse returns, so the error
		// binds to the source alone rather than routing to one of them.
		if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
			return nil, bindTree(WrapError(yamlMessageError{yamlErr}, atToken(yamlErr.GetToken())), binder{src: s})
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return nil, err
	}

	return file, nil
}

// splitConsecutiveHeaders cuts tks before each "---" header that directly
// follows another, so each run it returns parses on its own. Each header
// starts a document, but the go-yaml parser stops at a header that directly
// follows another and drops every token after it (v1.19.2,
// parser/token.go:637). The look-back skips comments. The parser folds a
// comment on a header's line into that header, so the two headers still
// meet, and a comment on a line of its own parses to the same documents
// whether or not a run ends there.
func splitConsecutiveHeaders(tks token.Tokens) []token.Tokens {
	var (
		runs        []token.Tokens
		start       int
		afterHeader bool
	)

	for i, tk := range tks {
		if tk.Type == token.CommentType {
			continue
		}

		if tk.Type == token.DocumentHeaderType && afterHeader {
			runs = append(runs, tks[start:i])
			start = i
		}

		afterHeader = tk.Type == token.DocumentHeaderType
	}

	return append(runs, tks[start:])
}

// Bind binds err to the [Source] and to the document each location in
// it falls in, so a caller that holds the source binds without picking a
// document. An error that carries a [position.Position] or a
// [position.Range], as a check that runs on [Source.Lines] produces, binds
// to the document whose [Node.Span] holds the line, whatever the
// file holds:
//
//	for i, ln := range source.Lines().All() {
//		if ln.Width() > 120 {
//			rng := position.NewRange(position.New(i, 120), position.New(i, ln.Width()))
//
//			return source.Bind(niceyaml.NewError("line exceeds 120 columns", niceyaml.AtRange(rng)))
//		}
//	}
//
// A path resolves in the one document of the source, the one
// [Source.Document] returns, so a check on a configuration file binds its
// findings here as it would through [Node.Bind]:
//
//	return source.Bind(check(cfg))
//
// A path in a source that holds several documents, or none, resolves
// nowhere. The bound error keeps its message and the name of the source,
// [SourceError.Unresolved] returns [ErrPathNeedsDocument] wrapping the
// reason [Source.Document] gives, and [FormatError] names it in place of
// the excerpt. Bind such an error through [Node.Bind] with the document
// it was checked against, which also resolves a path from the scope of a
// Document from [Node.At].
//
// In every other way Bind is [Node.Bind], which describes what comes
// back. [SourceError.Document] returns the document each location fell
// in, and nil for an error whose location resolves in none.
func (s *Source) Bind(err error) error {
	return bindTree(err, binder{src: s, route: true})
}

// Lines returns the [line.Lines] of the [Source], its tokens split into
// one line per line of text. Line i is line i+1 of the text, so
// [position.NewFromToken] converts any token of the Source to a position
// in the lines.
//
// The lines never change, so every call returns the same value and the
// [go.jacobcolvin.com/niceyaml/finder.Finder] and
// [go.jacobcolvin.com/niceyaml/diff.Differ] read it as it is. To render the Source,
// take a [line.View] from [Source.View].
func (s *Source) Lines() line.Lines {
	return s.lines
}

// View returns a new [*line.View] over [Source.Lines] with no decoration.
// Each call returns a view of its own, so overlays and annotations added to
// one never reach the Source or another view. Render the view to see them.
func (s *Source) View() *line.View {
	return line.NewView(s.lines)
}
