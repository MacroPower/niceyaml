package niceyaml

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/lexers"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// View is read-only, line-by-line access to content that [Printer] renders
// and [Finder] searches.
//
// AllLines yields each [line.Line] by value, so a change to a yielded line
// reaches nothing. Overlays and annotations go through the [Lines] methods
// and through indexing a [Lines] collection directly.
//
// [Lines] implements View directly, and [*Source] implements it over its
// pristine lines.
type View interface {
	AllLines(spans ...position.Span) iter.Seq2[int, line.Line]
	AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune]
	Len() int
}

// Source is a YAML document. It holds the tokens the document was lexed from,
// the [*ast.File] they parse into, and the settings for parsing, decoding, and
// reporting errors.
//
// Source separates two concerns. Parsing and decoding live on Source itself,
// where [Source.File] lazily parses the AST, [Source.Decoder] iterates the
// documents, and [Source.WrapError] attaches source context to errors.
// Rendering lives in a [Lines] view, available from [Source.Lines], which
// organizes the tokens into lines and carries the overlays, annotations, and
// flags that [Printer] renders. Utilities that only render or search, such as
// [Printer], [Finder], and [Differ], accept either a Source or a view.
//
// Typical use creates a Source and passes it straight to a [Printer]:
//
//	source := NewSourceFromString(yamlContent)
//	printer := NewPrinter()
//	fmt.Println(printer.Print(source))
//
// A Source never changes after creation. It implements [View] over its
// pristine lines, so printing a Source always renders the document as
// parsed. To highlight or annotate, take a view with [Source.Lines], which
// returns an independent copy each call, and render the view instead:
//
//	view := source.Lines()
//	view.AddOverlay(style.GenericHighlight, ranges...)
//	fmt.Println(printer.Print(view))
//
// Since nothing mutates a Source, it is safe for concurrent use, and every
// view taken from it is a private copy.
//
// Create instances with [NewSourceFromFile], [NewSourceFromBytes],
// [NewSourceFromString], or [NewSourceFromTokens].
type Source struct {
	name       string
	filePath   string
	lines      Lines
	file       *ast.File
	fileErr    error
	parserOpts []parser.Option
	decodeOpts []yaml.DecodeOption
	fileOnce   sync.Once
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithFilePath]
//   - [WithAllowDuplicateKeys]
//   - [WithDisallowUnknownFields]
//   - [WithYAMLParserOptions]
//   - [WithYAMLDecodeOptions]
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

// WithAllowDuplicateKeys is a [SourceOption] that accepts a mapping with the
// same key twice, both when [Source.File] parses the document and when
// [DocumentDecoder] decodes it. The last value wins. Without it a duplicate
// key is an error.
func WithAllowDuplicateKeys() SourceOption {
	return func(s *Source) {
		s.parserOpts = append(s.parserOpts, parser.AllowDuplicateMapKey())
		s.decodeOpts = append(s.decodeOpts, yaml.AllowDuplicateMapKey())
	}
}

// WithDisallowUnknownFields is a [SourceOption] that makes [DocumentDecoder]
// reject a mapping key that has no field in the target struct. Without it
// unknown keys are ignored.
func WithDisallowUnknownFields() SourceOption {
	return WithYAMLDecodeOptions(yaml.DisallowUnknownField())
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

// WithYAMLDecodeOptions is a [SourceOption] that passes [yaml.DecodeOption]
// values to the go-yaml decoder in [DocumentDecoder.Decode] and
// [DocumentDecoder.DecodeInto]. It is the escape hatch for decoder settings
// that have no option of their own, such as [WithAllowDuplicateKeys].
func WithYAMLDecodeOptions(opts ...yaml.DecodeOption) SourceOption {
	return func(s *Source) {
		s.decodeOpts = append(s.decodeOpts, opts...)
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
// [lexers.Tokenize].
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := lexers.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromTokens creates a new [*Source] from [token.Tokens].
// See [NewLines] for details on token splitting behavior.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	t.lines = NewLines(tks)

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
// See [Lines.Tokens] for details on token recombination behavior.
func (s *Source) Tokens() token.Tokens {
	return s.lines.Tokens()
}

// Decoder returns a [*Decoder] for iterating over documents in this [Source].
//
// Decoder parses the source and pairs each parsed document with its tokens
// once, so [Decoder.Documents] can be iterated any number of times without
// repeating either step.
//
// Returns an error if the source cannot be parsed.
func (s *Source) Decoder() (*Decoder, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	return &Decoder{source: s, file: f, docTokens: alignDocumentTokens(f, s.Tokens())}, nil
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The file is lazily parsed on first call using [parser.Parse] with options
// provided via [WithYAMLParserOptions]. Subsequent calls return the cached result.
//
// A YAML syntax error comes back as an [*Error] that carries the offending
// token. Wrap it with [Source.WrapError] to render it against the source.
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

	if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
		return nil, NewError(
			yamlErr.GetMessage(),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// WrapError binds err to this [*Source] when err's chain holds an [*Error].
// The returned [*SourceError] resolves the location of that inner Error
// against this source, and its [SourceError.Render] and
// [SourceError.Detail] accept [DetailOption] values for how the excerpt
// looks. Context added around the Error with [fmt.Errorf] is preserved in
// the message.
//
// If err is nil, WrapError returns nil. If err's chain holds no [*Error], or
// the first one it holds is a nil pointer, WrapError returns err unchanged.
// WrapError never modifies err.
func (s *Source) WrapError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := firstError(err); !ok { //nolint:errcheck // Presence check, not a value extraction.
		return err
	}

	return newSourceError(err, s)
}

// Lines returns a [Lines] view of the [Source].
//
// Each call returns an independent copy, so overlays and annotations added to
// one view never reach the Source or another view. Render the view to see
// them.
func (s *Source) Lines() Lines {
	return s.lines.Clone()
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
// See [Lines.AllLines].
func (s *Source) AllLines(spans ...position.Span) iter.Seq2[int, line.Line] {
	return s.lines.AllLines(spans...)
}

// AllRunes returns an iterator over runes within the given ranges.
// See [Lines.AllRunes].
func (s *Source) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return s.lines.AllRunes(ranges...)
}
