package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/escape"
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
// [Node.Bind] binds errors built elsewhere to the document the caller checked them
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
	name     string
	filePath string
	lines    line.Lines
	// Holds the stream that [Source.Tokens] rebuilds from lines on its
	// first call.
	stream token.Tokens
	file   *ast.File
	// Holds the set of copies of the tokens parse hands the parser. Every
	// token the parser takes from the stream is one of these copies, and
	// holdsToken looks a token up in the set by pointer. The implicit null
	// tokens the parser makes for missing values are not copies, so
	// holdsToken finds them among the tokens of the nodes of the document
	// instead.
	fileTokens map[*token.Token]struct{}
	fileErr    error
	// A second parse of the tokens, which decodeParse makes the first time
	// a document changes its tree for the decoder, as decodeTree
	// describes, and the set of copies of the tokens that parse hands the
	// parser.
	decodeFile       *ast.File
	decodeFileTokens map[*token.Token]struct{}
	docs             []*Node
	parserOpts       []parser.Option
	decodeOpts       []yaml.DecodeOption
	streamOnce       sync.Once
	fileOnce         sync.Once
	docsOnce         sync.Once
	decodeFileOnce   sync.Once
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
// are [DecodeOption] values. A caller passes them to [Node.Decode], or to
// [NewDecoder] for a [Decoder] that decodes every document with them.
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
// the escape hatch for parser settings that have no option of their own.
// The parser always parses comments.
//
// These options reach the parser only, never the decoder that [Node]
// decodes with. Allow duplicate keys through [WithAllowDuplicateKeys],
// which sets both. [parser.AllowDuplicateMapKey] passed here lets
// [Source.File] accept a duplicate key that a decode into a struct or a
// typed map then rejects. A mapping that decodes into an any value, at
// the top level or in a field, keeps the last value instead.
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
// Returns an error when it cannot read the file.
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
// Returns an error when it cannot read the file.
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
// Returns an error when it cannot read r.
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
// The Source holds clones of the tokens, and [tokens.ResetPositions]
// resets their positions, so the text it holds counts its lines from 1 as
// [tokens.Tokenize] does. Line i of [Source.Lines] holds line i+1 of the
// text. Tokens that count from 1 already, as a whole stream does, keep
// their positions unless the lexer swallowed the first text, as
// [tokens.ResetPositions] describes. It renumbers tokens cut from a longer
// stream, as [Node.Tokens] hands out, from the first one. To render one
// document of a file with the file's line numbers, print the file's view
// with [Node.Span] instead.
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

// Name returns the name of the [Source]: the one [WithName] set, or else
// the file path. Returns an empty string when the Source has neither.
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

// Tokens returns the full [token.Tokens] stream of the [Source]. The first
// call rebuilds the stream from every [line.Line], and each call returns a
// new slice that holds the same tokens. See [line.Lines.Tokens] for how a
// token cut across lines comes back whole.
//
// The tokens are the clones [NewSourceFromTokens] made through
// [tokens.ResetPositions], not the tokens the caller passed. The Source keeps
// using them, so treat them as read-only. When the input came from
// [tokens.Tokenize], as it does for every constructor that reads text, the
// Line and Column of each token name the rune where its text starts.
func (s *Source) Tokens() token.Tokens {
	s.streamOnce.Do(func() {
		s.stream = s.lines.Tokens()
	})

	return slices.Clone(s.stream)
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
	docs, err := s.documents()
	if err != nil {
		return nil, err
	}

	return slices.Clone(docs), nil
}

// documents returns the root [*Node] of each YAML document, as
// [Source.Documents] does, in the slice the Source keeps rather than a
// copy, so a caller must not change it.
func (s *Source) documents() ([]*Node, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	s.docsOnce.Do(func() {
		s.docs = newDocuments(s, f)
	})

	return s.docs, nil
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
// below them rather than a document of their own. A file that opens with
// a license header therefore holds a single document. A file of comments
// alone holds one that decodes to the zero value as an empty file does.
//
// When the file holds more than one document, it returns an error wrapping
// [ErrMultipleDocuments], bound to the Source. The error points at the
// header of the second document, or at the first token of its content
// when a "..." marker rather than a header opens it. When the file holds
// no document at all, which happens for text that is only a "..." marker
// with or without a comment on its line, it returns an error wrapping
// [ErrNoDocuments], bound to the Source. A file that does not parse
// returns the error [Source.File] returns. Use [Source.Documents] for a
// file that may hold several.
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
	docs, err := s.documents()
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
// result. A "---" header that directly follows another header starts a
// document of its own, and a "..." end marker ends its document.
// [parser.Parse] alone drops the rest of the stream after such a header,
// and it can reject what follows a marker or join it to the document the
// marker ends.
//
// The tree leaves out a comment on a line of its own below a document whose
// root is a scalar or a flow collection. Below a block mapping or a block
// sequence at the root, it leaves out such a comment that starts left of
// the first key or "-" of the root. It leaves out such a comment between
// a %YAML or %TAG directive and its "---" header, between the
// "?" of an explicit key and the key, and between the key and its ":".
// Inside a flow collection, it also leaves out such a comment before a key
// of a flow mapping or before a ",", "]", or "}". [parser.Parse] rejects or
// misreads valid YAML that holds a comment in any of these places. A
// comment on the last line of a scalar that spans several lines counts as
// one on a line of its own. The parser attaches a comment to the token
// before it only when that token starts on the line of the comment. The
// tokens of the Source and of each [Node] still hold such a comment.
//
// The tree also leaves out the comments on lines of their own between an
// anchor and the node below them where they make the parser reject or
// misread that node. The anchor ends its line and directly follows a "-",
// or a key and its ":", on that line. The key starts on that line too, at
// its "?" when it has one. When the first of the comments starts left of
// the "-" or key, the parser gives the anchor a null value. The tree then
// leaves the comments out above a node that the parser takes as the value
// of the anchor without them. That node starts right of the "-" or key, or
// it is a "-" in the column of the key. When the first of the comments
// starts in the column of the "-" or key or right of it, the parser gives
// the anchor the node below them. The tree then leaves them out above a
// node left of the "-" or key, above a key in the column of the key, and
// above a "-" in the column of the "-". Below an anchor with no value that
// ends its document, the tree leaves out the comment too, and holds a null
// as the value of that anchor, as it does when a header follows the anchor.
//
// The tokens of the file are copies of the Source's own, since the parser
// relinks the tokens it receives. A copy matches the original by its type,
// value, origin, and position, so a token taken from a node finds its
// lines through [line.Lines.TokenRanges] and [line.Lines.ContentRanges] as
// the original does.
//
// Every [Node] of the Source shares the tree, and [Node.At],
// [Node.Ranges], and every error binding resolve against it, so it is
// read-only. A caller that modifies it corrupts the positions those
// resolve to and races with any concurrent use of the Source. A caller
// that edits a document parses a tree of its own, or edits the text and
// builds a new Source from the result.
//
// A YAML syntax error comes back as a [*SourceError] bound to this Source,
// so [FormatError] renders it with the offending token marked. Its message
// names each control character by its Unicode Control Picture, so a tab
// the parser rejects reads as "␉" there as it does in the excerpt. A
// panic in the parser comes back as a [*SourceError] that matches
// [ErrParseRejected], and every later call returns the same error.
func (s *Source) File() (*ast.File, error) {
	s.fileOnce.Do(func() {
		s.file, s.fileTokens, s.fileErr = s.parse()
	})

	return s.file, s.fileErr
}

// parse hands the parser the copies of the tokens that
// [tokens.ForParser] makes. The go-yaml parser relinks Next and Prev while
// it moves comment tokens, and the Source's own tokens, which its lines and
// the caller share, stay untouched. It returns the set of copies with the
// file they parsed to.
//
// ForParser cuts the blank lines in front of the first text down to their
// line breaks, so a blank line that holds a tab does not make the parser
// reject the first key. It also moves the blank content of a block scalar
// to the line after the header, so the parser attaches a comment below
// that content to the next node. Each copy gets the Origin and position
// of its own token back once the parser returns, so it matches the
// Source's token as [Source.File] promises.
func (s *Source) parse() (*ast.File, map[*token.Token]struct{}, error) {
	shared := s.Tokens()
	tks := tokens.ForParser(shared)

	set := make(map[*token.Token]struct{}, len(tks))
	for _, tk := range tks {
		set[tk] = struct{}{}
	}

	// The Source holds no nil tokens, so each copy sits at the index of
	// its own token.
	defer func() {
		for i, tk := range tks {
			tk.Origin = shared[i].Origin

			// Clone gives each copy a Position of its own, so the write
			// leaves the Source's token alone.
			if tk.Position != nil && shared[i].Position != nil {
				*tk.Position = *shared[i].Position
			}
		}
	}()

	file := &ast.File{Docs: []*ast.DocumentNode{}}

	for _, run := range splitDocumentRuns(tks) {
		f, err := s.parseRun(dropStrandedComments(run))
		if err == nil {
			nullEmptyAnchors(f)

			file.Docs = append(file.Docs, f.Docs...)

			continue
		}

		// The documents come from the file this parse returns, so the error
		// binds to the source alone rather than routing to one of them.
		// The go-yaml scanner spells the tab it rejects as a raw tab, which
		// the renderers would lay out as four spaces, so the message names
		// each control character by its picture.
		if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
			msg := escape.Control(yamlErr.GetMessage())

			return nil, nil, bindTree(
				WrapError(yamlMessageError{err: yamlErr, msg: msg}, atToken(yamlErr.GetToken())),
				binder{src: s},
			)
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return nil, nil, err
	}

	return file, set, nil
}

// parseRun parses run, one run of [splitDocumentRuns]. It turns a panic
// in the parser into a [*SourceError] bound to the Source that matches
// [ErrParseRejected], located at the first token of run that carries a
// position.
func (s *Source) parseRun(run token.Tokens) (*ast.File, error) {
	var (
		f   *ast.File
		err error
	)

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			var at *token.Token

			if i := slices.IndexFunc(run, func(tk *token.Token) bool { return tk.Position != nil }); i >= 0 {
				at = run[i]
			}

			err = bindTree(WrapError(fmt.Errorf("%w: panic: %v", ErrParseRejected, p), atToken(at)), binder{src: s})
		}()

		f, err = parser.Parse(run, parser.ParseComments, s.parserOpts...)
	}()

	return f, err //nolint:wrapcheck // The caller binds the error.
}

// decodeParse returns a second parse of the tokens of the Source, with the
// set of copies of the tokens that parse hands the parser, and makes it on
// the first call. Nothing but the decoder reads its tree, so a document
// changes its own part of that tree as [decodeTree] describes, and the
// tree [Source.File] returns stays as the parser built it. The parse
// holds its own copies of the tokens, so the go-yaml formatter, which
// reads the text of each token through the links between them, finds a
// renamed anchor or a verbatim !!int tag with the text of the Source.
// The formatter reads the tokens of every document, so the first call
// writes the nulls of all the documents into the tokens, as
// [nullEnclosedAliases] describes, before it returns the parse to any of
// them. It returns nil when the parse fails, which it does only when the
// first parse failed.
func (s *Source) decodeParse() (*ast.File, map[*token.Token]struct{}) {
	s.decodeFileOnce.Do(func() {
		file, set, err := s.parse()
		if err == nil {
			nullEnclosedAliases(s.docs, file)

			s.decodeFile, s.decodeFileTokens = file, set
		}
	})

	return s.decodeFile, s.decodeFileTokens
}

// splitDocumentRuns cuts tks into runs that each parse on its own. It cuts
// before each "---" header that follows a token other than a comment, and
// after each "..." end marker that a token on a later line follows.
//
// The go-yaml parser (v1.19.3-0.20260407131736-edee2f91616c) groups the
// documents of a stream in a recursion that copies the rest of the list
// at each header (parser/token.go:662), so one run of many documents takes
// time quadratic in their number. It also mishandles some sequences of
// headers and markers. It stops at a header that directly follows another
// and drops every token after it (parser/token.go:637). It drops a "..."
// marker that directly follows a header and merges the header into what
// comes after the marker (parser/token.go:661). A second header there
// gives the merged document two headers, which fails with "unexpected
// scalar value type". A %YAML or %TAG directive or a document without a
// header there joins the empty document rather than starting the next
// one, and a %YAML directive there fails when another opens the empty
// document. The parser also rejects a scalar document below any marker
// (parser/token.go:682).
//
// A header stays in the run above it in two cases. A header that a
// directive precedes stays in the run of the directive, since the parser
// rejects a run that ends in a directive. YAML allows a directive only
// before the first document or after a marker, where a run starts anyway,
// and the parser rejects one anywhere else only while the document above
// it shares its run. A header that follows an anchor with no value stays in
// the run of the anchor. The parser takes the two tokens after the "&" as
// the name and the value of the anchor, and rejects a run that ends before
// them (parser/token.go:311).
//
// Each run parses with a parser of its own, so a %TAG or %YAML directive
// holds for the documents of its run. That is the one document the
// directive heads, as YAML defines, unless that document ends in an
// anchor with no value. The parser alone carries a %TAG directive for
// "!!" into every later document of its input.
//
// The look-back skips comments. The parser folds a comment on the line of
// a header or a marker into that token, so such a comment stays in the
// run of its token. A comment on a line of its own above a header parses
// to the same documents whether or not a run ends there. One below a
// marker starts the next run, as it starts a token group of
// [tokens.SplitDocuments]. Left in the run above, it would parse to a node
// that shares that group with the node below, and both would take the
// tokens of the group. A token on the line of a marker stays in its run,
// since YAML allows only a comment there and the parser rejects a scalar
// there.
func splitDocumentRuns(tks token.Tokens) []token.Tokens {
	var (
		runs  []token.Tokens
		start int
		// Whether a directive follows the last header.
		directive bool
	)

	// The indexes of the last two tokens that are not comments.
	prev, prevprev := -1, -1

	for i, tk := range tks {
		if tk.Type == token.CommentType {
			continue
		}

		if prev >= 0 {
			last := tks[prev]

			switch {
			case last.Type == token.DocumentEndType && startsBelow(tk, last):
				// The comments on lines below the marker open the next run.
				cut := i
				for cut > prev+1 && startsBelow(tks[cut-1], last) {
					cut--
				}

				runs = append(runs, tks[start:cut])
				start = cut

			case tk.Type == token.DocumentHeaderType && last.Type != token.DocumentEndType:
				anchored := last.Type == token.AnchorType ||
					prevprev >= 0 && tks[prevprev].Type == token.AnchorType

				if last.Type == token.DocumentHeaderType || !directive && !anchored {
					runs = append(runs, tks[start:i])
					start = i
				}
			}
		}

		switch tk.Type {
		case token.DirectiveType:
			directive = true

		case token.DocumentHeaderType:
			directive = false

		default:
		}

		prevprev, prev = prev, i
	}

	return append(runs, tks[start:])
}

// dropStrandedComments returns the tokens of run without the comments on
// lines of their own in places where the go-yaml parser can reject or
// misread valid YAML when it keeps comments, or run itself when it holds
// none.
//
// The parser (v1.19.3-0.20260407131736-edee2f91616c) attaches such a
// comment below the content of a document only when the root of the
// document is a block mapping or a block sequence. The comment must also
// start at or right of the column of the first key or "-" of that root
// (parser/parser.go:525, parser/parser.go:1117). Below a scalar or a flow
// collection, or left of that column, it leaves the comment unread and
// fails with "value is not allowed in this context" (parser/parser.go:171).
// It also requires the header to follow the line of a %YAML or %TAG
// directive at once, and fails with "document not started" when a comment
// sits between them (parser/token.go:598). The go-yaml decoder parses
// without comments and accepts both. The comments stay among the tokens of
// the Source, where [tokens.SplitDocuments] still hands them to their
// documents.
//
// A comment sits on a line of its own when it starts on a line below the
// line where the last token before it starts. The parser attaches a
// comment to the token before it only when both start on one line
// (parser/token.go:254), so it reads a comment on the last line of a
// scalar that spans several lines as it reads one on a line of its own.
//
// Below a scalar or a flow collection, only the comments that close the
// document drop: those that the end of run, a "---" header, or a "..."
// marker follows. The parser rejects any other token after them, with or
// without the comments. Below a block mapping or a block sequence, only
// the comments that close the document and start left of its first key
// or "-" drop. The key starts at its anchor or tag when it has one, as
// [keyStart] finds it. A comment left of that column stays when more of
// the root follows it, since the parser looks past it to the next key or
// "-".
//
// Below an anchor with no value, the comments that close the document stay,
// since the parser takes them as the value of the anchor and rejects an
// anchor that no token follows past its name (parser/token.go:311).
// [nullEmptyAnchors] puts a null in their place once the parser returns. In
// a block mapping or a block sequence at the root, an anchor that directly
// follows a "-", or a key and its ":", on its line is the exception when a
// "---" header or a "..." marker follows the comments. The parser takes
// that anchor without them, as [takesNull] reports, so those that start
// left of the first key or "-" of the root drop, as they do below any other
// node of such a root. Above a node, the comments drop when [breaksAnchor]
// reports that they make the parser give the anchor the wrong value. Inside
// a flow collection, the anchor does not decide whether they drop. In a
// flow sequence, the parser still reads an anchor after the key of a pair
// by the column of the first comment. When that comment starts left of the
// key, the parser gives the anchor a null value and rejects the value below
// the comment.
//
// Below a "&" with no name, the comments stay wherever they sit, except
// between a directive and its header. The parser takes the token after a
// "&" as the name of the anchor (parser/token.go:314), so it rejects a "&"
// above a comment. Without the comments, it would name the anchor after the
// token below them.
//
// In block and flow style alike, the parser takes the token after a "?"
// as the key the "?" introduces, and the token before a ":" as the key of
// that ":" (parser/token.go:493, parser/token.go:513). With a comment in
// either place, it misreads the mapping or fails with "unexpected scalar
// value type" (parser/parser.go:326). Those comments drop.
//
// Inside a flow collection, the parser reads the other comments as the
// head comment of the node that follows them. It fails when a ",", "]",
// or "}" follows them instead, and when they sit after a "{" or "," of a
// flow mapping, where it looks for a key (parser/parser.go:349,
// parser/parser.go:407, parser/parser.go:1042). Those comments drop, and
// the comments before any other node stay.
func dropStrandedComments(run token.Tokens) token.Tokens {
	var (
		kept token.Tokens
		// What the document the scan is in has shown so far: a token of
		// its content, an indicator of a block mapping or a block sequence
		// at its top level, and a directive that awaits its header.
		content, block, directive bool
		// The column where the first key or "-" of the block mapping or
		// block sequence at the root of the document starts, or -1.
		rootCol = -1
		// The types of the "[" and "{" tokens of the flow collections
		// that hold the scan, the innermost last.
		flows []token.Type
		// The index of the last anchor in run.
		anchor int
		// The last two tokens other than comments that hold text.
		last, beforeLast *token.Token
	)

	for i := 0; i < len(run); {
		tk := run[i]

		if tk.Type != token.CommentType {
			switch tk.Type {
			case token.DocumentHeaderType, token.DocumentEndType:
				content, block, directive, flows = false, false, false, flows[:0]
				rootCol = -1

			case token.DirectiveType:
				directive = true

			case token.SequenceStartType, token.MappingStartType:
				content = true

				flows = append(flows, tk.Type)

			case token.SequenceEndType, token.MappingEndType:
				if len(flows) > 0 {
					flows = flows[:len(flows)-1]
				}

			case token.MappingKeyType, token.MappingValueType, token.SequenceEntryType:
				if !block && len(flows) == 0 {
					rootCol = rootColumn(run, i)
				}

				content = true
				block = block || len(flows) == 0

			case token.AnchorType:
				content = true
				anchor = i

			default:
				content = true
			}

			if strings.Trim(tk.Origin, " \t\r\n") != "" {
				beforeLast, last = last, tk
			}

			if kept != nil {
				kept = append(kept, tk)
			}

			i++

			continue
		}

		next := i
		for next < len(run) && run[next].Type == token.CommentType {
			next++
		}

		// A "&" with no name ends the content.
		bare := last != nil && last.Type == token.AnchorType

		// The name of an anchor ends the content when the anchor has no
		// value.
		anchored := beforeLast != nil && beforeLast.Type == token.AnchorType

		// Whether the comments close the document.
		closes := next == len(run) ||
			run[next].Type == token.DocumentHeaderType || run[next].Type == token.DocumentEndType

		// The parser takes a comment after a "?" or before a ":" as a key.
		stranded := directive || last != nil && last.Type == token.MappingKeyType ||
			next < len(run) && run[next].Type == token.MappingValueType

		// Whether the comments that start left of rootCol drop.
		leftDrops := false

		switch {
		case bare:
			// Without the comments, the parser would take the token below
			// them as the name of the anchor.
			stranded = directive

		case len(flows) > 0:
			stranded = stranded || next < len(run) && endsFlowEntry(run[next]) ||
				startsFlowKey(last, flows[len(flows)-1])

		case anchored:
			// The first comment on a line of its own, past any comment on
			// the line where the last token starts.
			below := i
			for below < next && !startsBelow(run[below], last) {
				below++
			}

			stranded = stranded ||
				below < next && !closes && breaksAnchor(run, anchor, below, next)
			leftDrops = closes && next < len(run) && rootCol >= 0 && takesNull(run, anchor)

		case content && !block:
			stranded = stranded || closes

		case block:
			leftDrops = closes && rootCol >= 0
		}

		for j := i; j < next; j++ {
			left := leftDrops && run[j].Position != nil && run[j].Position.Column < rootCol

			if (stranded || left) && startsBelow(run[j], last) {
				if kept == nil {
					kept = slices.Clone(run[:j])
				}

				continue
			}

			if kept != nil {
				kept = append(kept, run[j])
			}
		}

		i = next
	}

	if kept == nil {
		return run
	}

	return kept
}

// nullEmptyAnchors puts a null in place of each comment that the parser
// took as the value of an anchor of file. When a header or another entry
// follows an anchor with no value, the parser gives the anchor an
// implicit null, and this null takes the token type and position of that
// one. The go-yaml decoder reads the null as an empty node, where it
// would reject the comment in a typed target and give a pointer a value
// for it.
func nullEmptyAnchors(file *ast.File) {
	for _, doc := range file.Docs {
		for _, node := range sourceNodes(doc) {
			anchor, ok := node.(*ast.AnchorNode)
			if !ok || astnode.HasContent(anchor.Value) {
				continue
			}

			var pos *token.Position

			if anchor.Start != nil && anchor.Start.Position != nil {
				p := *anchor.Start.Position
				p.Column++
				pos = &p
			}

			tk := token.New("null", " null", pos)
			tk.Type = token.ImplicitNullType
			anchor.Value = ast.Null(tk)
		}
	}
}

// endsFlowEntry reports whether tk is a ",", "]", or "}", which ends an
// entry or a collection in flow style.
func endsFlowEntry(tk *token.Token) bool {
	switch tk.Type {
	case token.CollectEntryType, token.SequenceEndType, token.MappingEndType:
		return true

	default:
		return false
	}
}

// startsFlowKey reports whether tk is the "{" or a "," of a flow mapping,
// which a key follows. A token of type flow opens the innermost flow
// collection around tk. It reports false when tk is nil.
func startsFlowKey(tk *token.Token, flow token.Type) bool {
	if tk == nil || flow != token.MappingStartType {
		return false
	}

	return tk.Type == token.MappingStartType || tk.Type == token.CollectEntryType
}

// breaksAnchor reports whether the comments between the anchor at run[at]
// and the node at run[next] make the parser give the anchor the wrong
// value where it gives the right one without them. The anchor has its
// name, and the comment at run[below] is the first of the comments on a
// line of its own.
//
// The comments change what the parser reads only below an anchor that
// directly follows a "-", or a key and its ":", on its line. The key must
// start on that line too, where [keyStart] finds it, and an explicit key
// starts at its "?" (parser/parser.go:730). The parser gives that anchor a
// null value when the first comment starts left of the "-" or key, and the
// node otherwise (parser/parser.go:787, parser/parser.go:1186). It reads
// any other anchor the same way with the comments as without them, so
// breaksAnchor reports false for one.
//
// Without the comments, the parser takes the node as the value of the
// anchor when the node starts right of the "-" or key, or is a "-" in the
// column of the key, where a block sequence may start. Above such a node,
// breaksAnchor reports whether the first comment starts left of the "-" or
// key, which makes the parser give the anchor a null value.
//
// Above any other node, YAML gives the anchor a null value, so
// breaksAnchor reports false when the first comment starts left of the "-"
// or key. Otherwise it reports true above a node left of the "-" or key, a
// key in the column of the key, and a "-" in the column of the "-". The
// parser gives the anchor a null value there without the comments
// (parser/parser.go:750, parser/parser.go:787, parser/parser.go:1149,
// parser/parser.go:1186). It reports false above any other node. One such
// node is a key in the column of the "-", which the parser takes as the
// value without the comments too.
//
// When a token it compares has no position, breaksAnchor reports false.
func breaksAnchor(run token.Tokens, at, below, next int) bool {
	comment, tk := run[below], run[next]
	if comment.Position == nil || tk.Position == nil {
		return false
	}

	// The "-" or ":" that the anchor directly follows on its line.
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return false
	}

	// The index of the "-" or the start of the key.
	holder := i
	mapping := run[i].Type == token.MappingValueType

	switch {
	case mapping:
		holder = keyStart(run, i)

	case run[i].Type != token.SequenceEntryType:
		return false
	}

	if holder < 0 || run[holder].Position == nil {
		return false
	}

	var (
		col   = run[holder].Position.Column
		entry = tk.Type == token.SequenceEntryType
		// Whether the parser gives the anchor a null value with the
		// comments in the tree.
		null = comment.Position.Column < col
	)

	switch {
	case tk.Position.Column > col, mapping && entry && tk.Position.Column == col:
		return null

	case null:
		return false

	case tk.Position.Column < col:
		return true

	case mapping:
		return startsKey(run, next)

	default:
		return entry
	}
}

// startsKey reports whether a key of a block mapping starts at run[at],
// the first token on its line. That token is a "?" or a ":", or a ":"
// follows it on its line. A "[" or "{" before that ":" makes the line a
// flow collection, which the parser does not read as a key, so startsKey
// reports false for it.
func startsKey(run token.Tokens, at int) bool {
	if run[at].Type == token.MappingKeyType {
		return true
	}

	for j := at; j < len(run) && sameLine(run[j], run[at]); j++ {
		switch run[j].Type {
		case token.MappingValueType:
			return true

		case token.SequenceStartType, token.MappingStartType:
			return false

		default:
		}
	}

	return false
}

// takesNull reports whether the parser gives the anchor at run[at], which
// has its name, a null value when a token left of its key or "-" follows
// it. It does so for an anchor that directly follows a "-", or a key and
// its ":", on its line (parser/parser.go:787, parser/parser.go:1186). The
// parser rejects any other anchor that no node follows.
func takesNull(run token.Tokens, at int) bool {
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return false
	}

	switch run[i].Type {
	case token.SequenceEntryType:
		return true

	case token.MappingValueType:
		return keyStart(run, i) >= 0

	default:
		return false
	}
}

// rootColumn returns the column where the entry of the "-", "?", or ":"
// at run[at] starts, or -1 when the token there has no position. An
// entry starts at its "-" or "?". For a ":", it starts where [keyStart]
// finds the start of the key, or at the ":" itself when no token of a
// key precedes the ":" on its line.
func rootColumn(run token.Tokens, at int) int {
	tk := run[at]

	if tk.Type == token.MappingValueType {
		if k := keyStart(run, at); k >= 0 {
			tk = run[k]
		}
	}

	if tk.Position == nil {
		return -1
	}

	return tk.Position.Column
}

// keyStart returns the index of the token on the line of the ":" at
// run[colon] where the key before that ":" starts. An explicit key starts
// at its "?". Any other key starts past any "-" or ":" on the line, at its
// anchor or tag when it has one. It returns -1 when no token of a key
// precedes the ":" on its line, as with a ":" that starts its line, and
// when the "?" of the key sits on a line above.
func keyStart(run token.Tokens, colon int) int {
	start := -1

	k := colon - 1
	for ; k >= 0 && sameLine(run[k], run[colon]); k-- {
		switch run[k].Type {
		case token.MappingKeyType:
			return k

		case token.SequenceEntryType, token.MappingValueType, token.DocumentHeaderType:
			return start

		default:
		}

		start = k
	}

	// The token before the line of the ":", past any comments.
	for k >= 0 && run[k].Type == token.CommentType {
		k--
	}

	if k >= 0 && run[k].Type == token.MappingKeyType {
		return -1
	}

	return start
}

// sameLine reports whether a and b start on the same line. It reports
// false when either token has no position.
func sameLine(a, b *token.Token) bool {
	return a.Position != nil && b.Position != nil && a.Position.Line == b.Position.Line
}

// startsBelow reports whether tk starts on a line below the line mark
// starts on. It reports false when mark is nil or either token has no
// position.
func startsBelow(tk, mark *token.Token) bool {
	return mark != nil && tk.Position != nil && mark.Position != nil &&
		tk.Position.Line > mark.Position.Line
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
// the caller checked it against, which also resolves a path from the
// scope of a Document from [Node.At].
//
// In every other way Bind is [Node.Bind], which describes what comes
// back. [SourceError.Document] returns the document each location fell
// in, whether or not the location resolves there, and nil for an error
// that fell in none, as [SourceError.Node] describes.
func (s *Source) Bind(err error) error {
	return bindTree(err, binder{src: s, route: true})
}

// Lines returns the [line.Lines] of the [Source], which hold its tokens
// in one line per line of text. Line i holds line i+1 of the text, so
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
