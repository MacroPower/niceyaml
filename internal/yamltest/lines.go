package yamltest

import (
	"errors"
	"fmt"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Sentinel errors for line validation.
var (
	// ErrLineNumberNotIncreasing indicates that a line number is not greater
	// than the number of the line before it.
	ErrLineNumberNotIncreasing = errors.New("line number not greater than previous")
	// ErrLineNumberMismatch indicates that the line of a token differs from
	// the number of the line that holds it.
	ErrLineNumberMismatch = errors.New("token line differs from line number")
	// ErrColumnNotIncreasing indicates that a column is not greater than the
	// column before it.
	ErrColumnNotIncreasing = errors.New("column not greater than previous")
	// ErrEmptyLineNumbered indicates that a line without tokens has a number.
	ErrEmptyLineNumbered = errors.New("line with no tokens has a number")
)

// ValidateLines checks the integrity of ls against these rules:
//   - Line numbers strictly increase.
//   - Every token on a line is non-nil and carries a Position.
//   - Every token on a line carries the line's [line.Line.Number] in its
//     Position.
//   - The columns of the tokens on a line strictly increase. The check
//     skips tokens with an empty Origin.
//
// A line without tokens must be a placeholder without a number, such as the
// blank row that a side-by-side diff inserts. ValidateLines rejects such a
// line when it has a number and otherwise leaves it out of the numbering
// check.
//
// It returns an error that wraps [ErrEmptyLineNumbered],
// [ErrLineNumberNotIncreasing], [ErrLineNumberMismatch], or
// [ErrColumnNotIncreasing] for the first check that fails. A nil token or a
// token without a Position yields an error that wraps a
// [*TokenValidationError], whose Reason is [ErrNilToken] or
// [ErrNilPosition].
func ValidateLines(ls line.Lines) error {
	prevLineNum := 0

	for i, l := range ls.All() {
		// A line with no tokens is a placeholder, such as the blank row a
		// side-by-side diff inserts, and carries no number. Every other
		// line must number above the one before it.
		if l.IsEmpty() {
			if n := l.Number(); n != 0 {
				return fmt.Errorf(
					"line at index %d: line with no tokens has number %d: %w",
					i,
					n,
					ErrEmptyLineNumbered,
				)
			}

			continue
		}

		// Check: line numbers strictly increasing.
		lineNum := l.Number()
		if lineNum <= prevLineNum {
			return fmt.Errorf(
				"line at index %d: line number %d not greater than previous %d: %w",
				i,
				lineNum,
				prevLineNum,
				ErrLineNumberNotIncreasing,
			)
		}

		prevLineNum = lineNum

		// Check: every token carries the line's number and columns are strictly increasing.
		prevCol := 0

		for j, tk := range l.Tokens() {
			if tk == nil {
				return fmt.Errorf(
					"line at index %d: %w",
					i,
					&TokenValidationError{Index: j, Which: whichGot, Reason: ErrNilToken},
				)
			}

			if tk.Position == nil {
				return fmt.Errorf(
					"line at index %d: %w",
					i,
					&TokenValidationError{Index: j, Which: whichGot, Reason: ErrNilPosition},
				)
			}

			// Check the token's line matches the line number.
			if tk.Position.Line != lineNum {
				return fmt.Errorf(
					"line at index %d, token %d: token line %d differs from line number %d: %w",
					i,
					j,
					tk.Position.Line,
					lineNum,
					ErrLineNumberMismatch,
				)
			}

			// Check columns strictly increasing.
			//
			// Skip check for zero-width tokens (empty Origin) as they don't
			// occupy column space. The lexer places such a token where the
			// next one starts, as it does the empty content of a block
			// scalar.
			if tk.Origin != "" {
				if tk.Position.Column <= prevCol {
					return fmt.Errorf(
						"line at index %d, token %d: column %d not greater than previous %d: %w",
						i,
						j,
						tk.Position.Column,
						prevCol,
						ErrColumnNotIncreasing,
					)
				}

				prevCol = tk.Position.Column
			}
		}
	}

	return nil
}

// LineNumbers returns the [line.Line.Number] of each line of seq, such as
// a [line.Lines] or a [*line.View], in order, or nil when seq does not
// hold a line.
func LineNumbers(seq line.Sequence) []int {
	var numbers []int

	for _, l := range seq.All() {
		numbers = append(numbers, l.Number())
	}

	return numbers
}

// ErrorAnnotation returns the [line.Annotation] that the binding of an
// error leaves below a line that it marks. A printer draws a caret under each
// column of cols with msg beside the carets, or the carets alone when msg
// is empty.
func ErrorAnnotation(cols position.Span, msg string) line.Annotation {
	return line.Annotation{Content: msg, Kind: kind.TextError, Placement: line.Below, Cols: cols}
}

// ErrorOverlays returns the [line.Overlays] that the binding of an error
// leaves on a line that it marks, one at each of spans, in order.
func ErrorOverlays(spans ...position.Span) line.Overlays {
	overlays := make(line.Overlays, 0, len(spans))
	for _, span := range spans {
		overlays = append(overlays, line.Overlay{Kind: kind.GenericError, Cols: span})
	}

	return overlays
}
