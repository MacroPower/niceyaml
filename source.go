package niceyaml

import (
	"errors"
	"iter"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/style"
)

// LineGetter provides direct access to lines as a slice.
// See [Source] for an implementation.
type LineGetter interface {
	Lines() line.Lines
	Len() int
	IsEmpty() bool
}

// LineIterator provides line-by-line access to YAML tokens.
// See [Source] for an implementation.
type LineIterator interface {
	AllLines(spans ...position.Span) iter.Seq2[position.Position, line.Line]
	AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune]
	Len() int
	IsEmpty() bool
}

// Source is the central type for parsing, displaying, and annotating YAML.
// It organizes YAML tokens into lines, enabling precise position tracking
// and styled rendering through [Printer].
//
// Typical use involves creating a Source, then passing it to utilities like
// [Printer] for rendering or [Finder] for searching:
//
//	source := NewSourceFromString(yamlContent)
//	printer := NewPrinter(WithStyles(theme.Charm()))
//	fmt.Println(printer.Print(source))
//
// The token-based line structure enables features that would be difficult
// with string-based approaches: partial rendering of specific line ranges,
// accurate diff computation between YAML revisions, and search highlighting
// that respects token boundaries.
//
// For structured access to the YAML content, use the [Source.File] method,
// which lazily parses the AST. Any parsing errors can be wrapped with source
// context using [Source.WrapError] for user-friendly error messages.
//
// Overlay operations (for highlighting search results or diagnostics) are
// thread-safe; a Source can be highlighted from multiple goroutines while
// being rendered.
//
// Create instances with [NewSourceFromString], [NewSourceFromToken], or
// [NewSourceFromTokens].
type Source struct {
	name       string
	lines      line.Lines
	file       *ast.File
	fileErr    error
	parserOpts []parser.Option
	errorOpts  []ErrorOption
	fileOnce   sync.Once
	overlayMu  sync.RWMutex
}

// SourceOption configures [Source] creation.
//
// Available options:
//   - [WithName]
//   - [WithParserOptions]
//   - [WithErrorOptions]
type SourceOption func(*Source)

// WithName is a [SourceOption] that sets the name for the [Source].
func WithName(name string) SourceOption {
	return func(s *Source) {
		s.name = name
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

// WithErrorOptions is a [SourceOption] that sets the [ErrorOption]s used when
// wrapping errors with [Source.WrapError].
func WithErrorOptions(opts ...ErrorOption) SourceOption {
	return func(s *Source) {
		s.errorOpts = opts
	}
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
	if tk == nil {
		return &Source{}
	}

	// Walk to initial token.
	for tk.Prev != nil {
		tk = tk.Prev
	}

	// Collect all tokens forward, filtering parser-only tokens.
	var tks token.Tokens
	for ; tk != nil; tk = tk.Next {
		// Avoid calling tks.Add, since it modifies the token's Next/Prev pointers,
		// which will race with any reads/writes.
		// Clone will also break equality checks.
		tks = append(tks, tk)
	}

	return NewSourceFromTokens(tks, opts...)
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

// Tokens reconstructs the full [token.Tokens] stream from all [line.Line]s.
// See [line.Lines.Tokens] for details on token recombination behavior.
func (s *Source) Tokens() token.Tokens {
	return s.lines.Tokens()
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

func (s *Source) parse() (*ast.File, error) {
	file, err := parser.Parse(s.Tokens(), parser.ParseComments, s.parserOpts...)
	if err == nil {
		return file, nil
	}

	var yamlErr yaml.Error
	if errors.As(err, &yamlErr) {
		return nil, NewError(
			yamlErr.GetMessage(),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// WrapError wraps an error with additional context for [Error] types.
// It applies any [ErrorOption]s provided and sets the source to this [Source].
// If the error isn't an [Error], it returns the original error unmodified.
func (s *Source) WrapError(err error) error {
	if err == nil {
		return nil
	}

	var yamlErr *Error
	if errors.As(err, &yamlErr) {
		yamlErr.SetOption(s.errorOpts...)
		yamlErr.SetOption(WithSource(s))

		return yamlErr
	}

	return err
}

// Len returns the number of lines.
func (s *Source) Len() int {
	return len(s.lines)
}

// IsEmpty reports whether there are no lines.
func (s *Source) IsEmpty() bool {
	return len(s.lines) == 0
}

// AllLines returns an iterator over lines within the given spans.
//
// If no spans are provided, all lines are iterated. Each iteration yields a
// [position.Position] and the [line.Line] at that position.
func (s *Source) AllLines(spans ...position.Span) iter.Seq2[position.Position, line.Line] {
	return func(yield func(position.Position, line.Line) bool) {
		s.overlayMu.RLock()
		defer s.overlayMu.RUnlock()

		// No spans = all lines (backwards compatible).
		if len(spans) == 0 {
			for i, ln := range s.lines {
				if !yield(position.New(i, 0), ln) {
					return
				}
			}

			return
		}

		// Iterate only lines within provided spans.
		for _, span := range spans {
			start := max(0, span.Start)
			end := min(len(s.lines), span.End)

			for i := start; i < end; i++ {
				if !yield(position.New(i, 0), s.lines[i]) {
					return
				}
			}
		}
	}
}

// AllRunes returns an iterator over runes within the given ranges.
// If no ranges are provided, all runes are iterated.
// Each iteration yields a [position.Position] and the rune at that position.
func (s *Source) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		s.overlayMu.RLock()
		defer s.overlayMu.RUnlock()

		// No ranges = all runes (backwards compatible).
		if len(ranges) == 0 {
			for i, ln := range s.lines {
				col := 0

				for _, tk := range ln.Tokens() {
					for _, r := range tk.Origin {
						if !yield(position.New(i, col), r) {
							return
						}

						col++
					}
				}
			}

			return
		}

		// Iterate only runes within provided ranges.
		for _, rng := range ranges {
			startLine := max(0, rng.Start.Line)
			endLine := min(len(s.lines)-1, rng.End.Line)

			for i := startLine; i <= endLine; i++ {
				col := 0

				for _, tk := range s.lines[i].Tokens() {
					for _, r := range tk.Origin {
						pos := position.New(i, col)
						if rng.Contains(pos) {
							if !yield(pos, r) {
								return
							}
						}

						col++
					}
				}
			}
		}
	}
}

// Line returns the [*line.Line] at the given index.
// Panics if idx is out of range.
func (s *Source) Line(idx int) *line.Line {
	return &s.lines[idx]
}

// Lines returns all [line.Lines] in the [Source].
// This returns the internal slice for efficiency; callers should not modify it.
func (s *Source) Lines() line.Lines {
	return s.lines
}

// Content returns the combined content of all [line.Line]s as a string.
// Lines are joined with newlines.
func (s *Source) Content() string {
	return s.lines.Content()
}

// String reconstructs all [line.Line]s as a string, including any annotations.
// This should generally only be used for debugging.
func (s *Source) String() string {
	return s.lines.String()
}

// Validate checks the integrity of the [Source].
// See [line.Lines.Validate] for details on validation checks.
func (s *Source) Validate() error {
	//nolint:wrapcheck // Pass through validation error directly.
	return s.lines.Validate()
}

// TokenAt returns the [*token.Token] at the given position.
// Returns nil if the position is out of bounds or no token exists there.
func (s *Source) TokenAt(pos position.Position) *token.Token {
	return s.lines.TokenAt(pos)
}

// TokenPositionRangesFromToken returns all position ranges for a given token.
// Returns nil if the token is nil or not found in the [Source].
func (s *Source) TokenPositionRangesFromToken(tk *token.Token) []position.Range {
	positions := s.lines.TokenPositions(tk)
	return s.TokenPositionRanges(positions...)
}

// TokenPositionRanges returns all token position ranges that are part of the
// same joined token group as the tokens at the given [position.Position]s.
//
// For non-joined lines, returns the range of the token at each given column.
// Duplicate ranges are removed.
//
// Returns nil if no tokens exist at any of the given positions.
func (s *Source) TokenPositionRanges(positions ...position.Position) []position.Range {
	allRanges := position.NewRanges()

	for _, pos := range positions {
		ranges := s.lines.TokenPositionRangesAt(pos)
		if ranges != nil {
			for _, r := range ranges.Values() {
				allRanges.Add(r)
			}
		}
	}

	return allRanges.UniqueValues()
}

// ContentPositionRangesFromToken returns all position ranges for content of the
// given token, excluding leading and trailing whitespace.
//
// Returns nil if the token is nil or not found in the [Source].
func (s *Source) ContentPositionRangesFromToken(tk *token.Token) []position.Range {
	positions := s.lines.TokenPositions(tk)
	return s.ContentPositionRanges(positions...)
}

// ContentPositionRanges returns all position ranges for content at the given
// positions, excluding leading and trailing whitespace.
//
// Duplicate ranges are removed.
//
// Returns nil if no content exists at any of the given positions.
func (s *Source) ContentPositionRanges(positions ...position.Position) []position.Range {
	allRanges := position.NewRanges()

	for _, pos := range positions {
		ranges := s.lines.ContentPositionRangesAt(pos)
		if ranges != nil {
			for _, r := range ranges.Values() {
				allRanges.Add(r)
			}
		}
	}

	return allRanges.UniqueValues()
}

// AddOverlay adds an overlay of the given kind to the specified ranges.
// Multi-line ranges are split into per-line overlays automatically.
func (s *Source) AddOverlay(kind style.Style, ranges ...position.Range) {
	s.overlayMu.Lock()
	defer s.overlayMu.Unlock()

	s.lines.AddOverlay(kind, ranges...)
}

// ClearOverlays removes all overlays from all lines.
func (s *Source) ClearOverlays() {
	s.overlayMu.Lock()
	defer s.overlayMu.Unlock()

	s.lines.ClearOverlays()
}
