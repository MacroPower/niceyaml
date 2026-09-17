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

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Source is a YAML file: one stream of text that holds one or more YAML
// documents. It holds the tokens the text was lexed from, the [*ast.File]
// they parse into, and the settings for parsing, decoding, and reporting
// errors. [Source.Documents] yields each document in the file as a
// [*Document].
//
// Source separates two concerns. Parsing and decoding live on Source itself,
// where [Source.File] lazily parses the AST and [Source.Documents] iterates
// the documents. Every error they and their Documents produce comes back
// bound to the Source as a [SourceError], and [Source.WrapError] binds
// errors built elsewhere. Rendering lives in a [line.Lines] view, available
// from [Source.Lines], which organizes the tokens into lines and carries the
// overlays, annotations, and flags that a [printer.Printer] renders. Utilities that
// only render or search, such as [printer.Printer], [finder.Finder], and
// [diff.Differ], accept either a Source or a view.
//
// Typical use creates a Source and passes it straight to a
// [printer.Printer]:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	fmt.Println(p.Print(source))
//
// A Source never changes after creation. It implements [line.View] over its
// pristine lines, so printing a Source always renders the document as
// parsed, and [Source.AllLines] yields a copy of each line. To highlight or
// annotate, take a view with [Source.Lines], which returns an independent
// copy each call, and render the view instead:
//
//	view := source.Lines()
//	view.AddOverlay(style.GenericHighlight, ranges...)
//	fmt.Println(p.Print(view))
//
// Since nothing mutates a Source, it is safe for concurrent use, and every
// view taken from it is a private copy.
//
// Create instances with [NewSourceFromFile], [NewSourceFromBytes],
// [NewSourceFromString], or [NewSourceFromTokens].
type Source struct {
	name       string
	filePath   string
	lines      line.Lines
	file       *ast.File
	fileErr    error
	parserOpts []parser.Option
	decodeOpts []yaml.DecodeOption
	fileOnce   sync.Once
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
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	if t.allowDuplicateKeys {
		t.parserOpts = append(t.parserOpts, parser.AllowDuplicateMapKey())
		t.decodeOpts = append(t.decodeOpts, yaml.AllowDuplicateMapKey())
	}

	t.lines = line.NewLines(tks)

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

// Documents returns the [*Documents] of this [Source].
//
// It parses the source and builds a [*Document] for each parsed document
// once, so [Documents.All] can be iterated any number of times without
// repeating either step.
//
// A YAML syntax error comes back bound to the Source. It is the same error
// [Source.File] returns.
func (s *Source) Documents() (*Documents, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	return newDocuments(s, f), nil
}

// File returns an [*ast.File] for the [Source] tokens.
//
// The file is lazily parsed on first call using [parser.Parse] with options
// provided via [WithYAMLParserOptions]. Subsequent calls return the cached result.
//
// A YAML syntax error comes back as a [*SourceError] bound to this Source,
// so the %+v verb renders it with the offending token highlighted.
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
		return nil, s.WrapError(NewError(
			yamlErr.GetMessage(),
			WithToken(yamlErr.GetToken()),
		))
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// WrapError binds err to this [*Source] when err's chain holds an [*Error].
// The returned [*SourceError] resolves the location of that inner Error
// against this source, and its [SourceError.Render] and
// [SourceError.Detail] accept [DetailOption] values for how the excerpt
// looks.
//
// Errors from [Source.File], [Source.Documents], and the [Document] methods
// are bound already, so they need no WrapError. WrapError is for errors
// built elsewhere, such as a validator that returns an [*Error] with a path. The
// message of err stays as it is, and the resolved position of a path error
// goes in front of it, so bind such an error before adding context with
// [fmt.Errorf] to keep the position beside the message:
//
//	fmt.Errorf("document %d: %w", i, source.WrapError(err))
//
// If err is nil, WrapError returns nil. If err's chain holds no [*Error], or
// the first one it holds is a nil pointer, or the first [*SourceError] it
// holds is bound to this Source already, WrapError returns err unchanged.
// WrapError never modifies err.
func (s *Source) WrapError(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := firstError(err); !ok { //nolint:errcheck // Presence check, not a value extraction.
		return err
	}

	bound, ok := errors.AsType[*SourceError](err)
	if ok && bound.source == s {
		return err
	}

	return newSourceError(err, s)
}

// Lines returns a [line.Lines] view of the [Source].
//
// Each call returns an independent copy, so overlays and annotations added to
// one view never reach the Source or another view. Render the view to see
// them.
func (s *Source) Lines() line.Lines {
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

// AllLines returns an iterator over lines within the given spans. See
// [line.Lines.AllLines].
//
// Each iteration yields a copy of the line, so a change to it reaches
// neither the Source nor a later iteration. To keep overlays or
// annotations, add them to a view from [Source.Lines].
func (s *Source) AllLines(spans ...position.Span) iter.Seq2[int, *line.Line] {
	return func(yield func(int, *line.Line) bool) {
		for i, l := range s.lines.AllLines(spans...) {
			if !yield(i, l.Clone()) {
				return
			}
		}
	}
}

// AllRunes returns an iterator over runes within the given ranges.
// See [line.Lines.AllRunes].
func (s *Source) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return s.lines.AllRunes(ranges...)
}
