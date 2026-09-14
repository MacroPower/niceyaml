package niceyaml

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
)

// LineIterator provides line-by-line access to YAML tokens.
//
// [line.Lines] implements it directly, and [Source] implements it by
// delegating to its view.
type LineIterator interface {
	AllLines(spans ...position.Span) iter.Seq2[position.Position, line.Line]
	AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune]
	Len() int
	IsEmpty() bool
}

// Source is a YAML document. It holds the tokens the document was lexed from,
// the [*ast.File] they parse into, and the settings for parsing, decoding, and
// reporting errors.
//
// Source separates two concerns. Parsing and decoding live on Source itself,
// where [Source.File] lazily parses the AST, [Source.Decoder] iterates the
// documents, and [Source.WrapError] attaches source context to errors.
// Rendering lives in a [line.Lines] view, available from [Source.Lines], which
// organizes the tokens into lines and carries the overlays, annotations, and
// flags that [Printer] renders. Utilities that only render or search, such as
// [Printer], [Finder], and [Differ], accept either a Source or a view.
//
// Typical use creates a Source and passes it straight to a [Printer]:
//
//	source := NewSourceFromString(yamlContent)
//	printer := NewPrinter(WithStyles(theme.Charm()))
//	fmt.Println(printer.Print(source))
//
// Source keeps a small set of view methods for the common case. The
// [LineIterator] methods let a Source go straight to a [Printer] or [Finder],
// and [Source.AddOverlay], [Source.ClearOverlays], and [Source.Width] cover
// highlighting. All of them delegate to the same [line.Lines] value that
// [Source.Lines] returns, so highlighting through either path renders
// identically. Everything else about the view, such as token lookup by
// position or the debugging [line.Lines.String], lives on [line.Lines] and is
// reached through [Source.Lines]. Callers that need an independent copy, for
// instance to highlight the same document two different ways, clone the view
// with [line.Lines.Clone].
//
// A Source is not safe for concurrent mutation. Add overlays from one
// goroutine at a time, and do not add them while another goroutine renders.
// Parsing through [Source.File] is safe to call concurrently.
//
// Create instances with [NewSourceFromFile], [NewSourceFromBytes],
// [NewSourceFromString], [NewSourceFromToken], or [NewSourceFromTokens].
type Source struct {
	name       string
	filePath   string
	lines      line.Lines
	file       *ast.File
	fileErr    error
	parserOpts []parser.Option
	decodeOpts []yaml.DecodeOption
	errorOpts  []ErrorOption
	fileOnce   sync.Once
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithFilePath]
//   - [WithParserOptions]
//   - [WithDecodeOptions]
//   - [WithErrorOptions]
type SourceOption func(*Source)

// WithName is a [SourceOption] that sets the name for the [Source].
func WithName(name string) SourceOption {
	return func(s *Source) {
		s.name = name
	}
}

// WithFilePath is a [SourceOption] that sets the file path for the [Source].
//
// This is used by [Decoder] to propagate file path context to
// [DocumentDecoder] instances, enabling schema matchers to route based on
// file location.
//
// For file-based sources, use [NewSourceFromFile] which sets this
// automatically.
func WithFilePath(path string) SourceOption {
	return func(s *Source) {
		s.filePath = path
	}
}

// WithParserOptions is a [SourceOption] that sets the parser options used when
// parsing the [Source] into an [*ast.File].
//
// These options are passed to [parser.Parse] in addition to
// [parser.ParseComments], which is always included.
func WithParserOptions(opts ...parser.Option) SourceOption {
	return func(s *Source) {
		s.parserOpts = opts
	}
}

// WithDecodeOptions sets [yaml.DecodeOption] values passed to the YAML decoder
// during [DocumentDecoder.Decode] and [DocumentDecoder.Unmarshal].
//
// This allows configuring decoder behavior such as allowing duplicate map keys:
//
//	source := niceyaml.NewSourceFromString(data,
//	    niceyaml.WithDecodeOptions(yaml.AllowDuplicateMapKey()),
//	)
//
// WithDecodeOptions is a [SourceOption].
func WithDecodeOptions(opts ...yaml.DecodeOption) SourceOption {
	return func(s *Source) {
		s.decodeOpts = opts
	}
}

// WithErrorOptions is a [SourceOption] that sets the [ErrorOption] values used
// when wrapping errors with [Source.WrapError].
func WithErrorOptions(opts ...ErrorOption) SourceOption {
	return func(s *Source) {
		s.errorOpts = opts
	}
}

// NewSourceFromFile creates a new [*Source] by reading a file from disk.
//
// The file path is automatically set on the [Source], enabling [Decoder] to
// propagate it to [DocumentDecoder] instances for schema routing.
//
// Returns an error if the file cannot be read.
func NewSourceFromFile(path string, opts ...SourceOption) (*Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Prepend file path option so user options can override if needed.
	opts = append([]SourceOption{WithFilePath(path), WithName(path)}, opts...)

	return NewSourceFromString(string(data), opts...), nil
}

// NewSourceFromBytes creates a new [*Source] from raw YAML bytes.
func NewSourceFromBytes(data []byte, opts ...SourceOption) *Source {
	return NewSourceFromString(string(data), opts...)
}

// NewSourceFromString creates a new [*Source] from a YAML string using
// [lexer.Tokenize].
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := lexer.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromToken creates a new [*Source] from a seed [*token.Token].
// It collects all [token.Tokens] by walking the token chain from start to end.
func NewSourceFromToken(tk *token.Token, opts ...SourceOption) *Source {
	return NewSourceFromTokens(tokenChain(tk), opts...)
}

// tokenChain collects every token linked to tk, from the first to the last.
// Returns nil if tk is nil.
func tokenChain(tk *token.Token) token.Tokens {
	if tk == nil {
		return nil
	}

	// Walk to initial token.
	for tk.Prev != nil {
		tk = tk.Prev
	}

	// Collect all tokens forward.
	var tks token.Tokens

	for ; tk != nil; tk = tk.Next {
		// Avoid calling tks.Add, since it modifies the token's Next/Prev pointers,
		// which will race with any reads/writes.
		// Clone will also break equality checks.
		tks = append(tks, tk)
	}

	return tks
}

// NewSourceFromTokens creates a new [*Source] from [token.Tokens].
// See [line.NewLines] for details on token splitting behavior.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	t.lines = line.NewLines(tks)

	return t
}

// Name returns the name of the [Source].
func (s *Source) Name() string {
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

// Decoder returns a [*Decoder] for iterating over documents in this [Source].
//
// Returns an error if the source cannot be parsed.
func (s *Source) Decoder() (*Decoder, error) {
	return NewDecoder(s)
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The file is lazily parsed on first call using [parser.Parse] with options
// provided via [WithParserOptions]. Subsequent calls return the cached result.
//
// Any YAML parsing errors are converted to [Error] with source annotations.
func (s *Source) File() (*ast.File, error) {
	s.fileOnce.Do(func() {
		s.file, s.fileErr = s.parse()
	})

	return s.file, s.fileErr
}

// parse hands a private copy of the tokens to the parser. The go-yaml parser
// relinks Next and Prev while it moves comment tokens, and the Source's own
// tokens are shared with its lines and with the caller, so they stay untouched.
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

	if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
		return nil, NewError(
			yamlErr.GetMessage(),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// WrapError wraps err in a new [*Error] that carries this [*Source] and any
// [ErrorOption] values from [WithErrorOptions], when err's chain holds an
// [*Error]. The returned error renders the location of that inner Error
// against this source, and context added around it with [fmt.Errorf] is
// preserved in the message.
//
// If err is nil, WrapError returns nil. If err's chain holds no [*Error],
// WrapError returns it unchanged. Nothing in err is modified.
func (s *Source) WrapError(err error) error {
	if err == nil {
		return nil
	}

	yamlErr, ok := errors.AsType[*Error](err)
	if !ok || yamlErr == nil {
		return err
	}

	opts := make([]ErrorOption, 0, len(s.errorOpts)+1)
	opts = append(opts, s.errorOpts...)
	opts = append(opts, WithSource(s))

	return NewErrorFrom(err, opts...)
}

// Lines returns the [line.Lines] view of the [Source].
//
// Lines returns the shared view rather than a copy, so overlays and
// annotations added to it are visible through every other view method on the
// Source. Use [line.Lines.Clone] for an independent copy.
func (s *Source) Lines() line.Lines {
	return s.lines
}

// Len returns the number of lines.
func (s *Source) Len() int {
	return s.lines.Len()
}

// IsEmpty reports whether there are no lines.
func (s *Source) IsEmpty() bool {
	return s.lines.IsEmpty()
}

// AllLines returns an iterator over lines within the given spans.
// See [line.Lines.AllLines].
func (s *Source) AllLines(spans ...position.Span) iter.Seq2[position.Position, line.Line] {
	return s.lines.AllLines(spans...)
}

// AllRunes returns an iterator over runes within the given ranges.
// See [line.Lines.AllRunes].
func (s *Source) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return s.lines.AllRunes(ranges...)
}

// AddOverlay adds an overlay of the given kind to the specified ranges.
// See [line.Lines.AddOverlay].
func (s *Source) AddOverlay(kind style.Style, ranges ...position.Range) {
	s.lines.AddOverlay(kind, ranges...)
}

// ClearOverlays removes all overlays from all lines.
func (s *Source) ClearOverlays() {
	s.lines.ClearOverlays()
}

// Width returns the maximum line width across all lines.
func (s *Source) Width() int {
	return s.lines.Width()
}
