package niceyaml

import (
	"errors"
	"iter"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
)

// Source represents a collection of [token.Tokens] organized into [line.Lines]
// with associated metadata.
type Source struct {
	Name  string
	lines line.Lines
}

// SourceOption configures [Source] creation.
type SourceOption func(*Source)

// WithName sets the name for the [Source].
func WithName(name string) SourceOption {
	return func(t *Source) {
		t.Name = name
	}
}

// NewSourceFromString calls [lexer.Tokenize] to create new [Source] from a YAML string.
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := lexer.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromToken creates new [Source] from a seed [*token.Token].
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
		// which will race with any reads/writes. Clone will also break equality checks.
		tks = append(tks, tk)
	}

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromTokens creates new [Source] from [token.Tokens].
// See [line.NewLines] for details on token splitting behavior.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
	for _, opt := range opts {
		opt(t)
	}

	t.lines = line.NewLines(tks)

	return t
}

// Tokens reconstructs the full [token.Tokens] stream from all [Line]s.
// See [line.Lines.Tokens] for details on token recombination behavior.
func (s *Source) Tokens() token.Tokens {
	return s.lines.Tokens()
}

// Parse parses the Source tokens into an [ast.File].
// Any YAML parsing errors are converted to [Error] with source annotations.
func (s *Source) Parse(opts ...parser.Option) (*ast.File, error) {
	file, err := parser.Parse(s.Tokens(), parser.ParseComments, opts...)
	if err == nil {
		return file, nil
	}

	var yamlErr yaml.Error
	if errors.As(err, &yamlErr) {
		return nil, NewError(
			errors.New(yamlErr.GetMessage()),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// Count returns the number of lines.
func (s *Source) Count() int {
	return len(s.lines)
}

// IsEmpty returns true if there are no lines.
func (s *Source) IsEmpty() bool {
	return len(s.lines) == 0
}

// Lines returns an iterator over all lines.
// Each iteration yields a [position.Position] and the [line.Line] at that position.
func (s *Source) Lines() iter.Seq2[position.Position, line.Line] {
	return func(yield func(position.Position, line.Line) bool) {
		for i, line := range s.lines {
			if !yield(position.New(i, 0), line) {
				return
			}
		}
	}
}

// Runes returns an iterator over all runes.
// Each iteration yields a [position.Position] and the rune at that position.
func (s *Source) Runes() iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
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
	}
}

// Line returns the [line.Line] at the given index. Panics if idx is out of range.
func (s *Source) Line(idx int) line.Line {
	return s.lines[idx]
}

// Annotate sets a [line.Annotation] on the [line.Line] at the given index.
// Panics if idx is out of range.
func (s *Source) Annotate(idx int, ann line.Annotation) {
	s.lines[idx].Annotation = ann
}

// SetFlag sets a [line.Flag] on the [line.Line] at the given index.
// Panics if idx is out of range.
func (s *Source) SetFlag(idx int, flag line.Flag) {
	s.lines[idx].Flag = flag
}

// Content returns the combined content of all [Line]s as a string.
// [Line]s are joined with newlines.
func (s *Source) Content() string {
	return s.lines.Content()
}

// String reconstructs all [Line]s as a string, including any annotations.
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

// TokenPositionRangesFromToken returns all position ranges for a given token.
// Returns nil if the token is nil or not found in the Source.
func (s *Source) TokenPositionRangesFromToken(tk *token.Token) []position.Range {
	positions := s.lines.TokenPositions(tk)
	return s.TokenPositionRanges(positions...)
}

// TokenPositionRanges returns all token position ranges that are part of
// the same joined token group as the tokens at the given [position.Position]s.
// For non-joined lines, returns the range of the token at each given column.
// Duplicate ranges are removed.
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
