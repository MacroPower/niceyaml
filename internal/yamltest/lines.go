package yamltest

import (
	"errors"
	"fmt"

	"go.jacobcolvin.com/niceyaml/line"
)

// Sentinel errors for line validation.
var (
	// ErrLineNumberNotIncreasing indicates a line number is not greater than
	// the previous.
	ErrLineNumberNotIncreasing = errors.New("line number not greater than previous")
	// ErrLineNumberMismatch indicates a token's line differs from the number
	// of the line holding it.
	ErrLineNumberMismatch = errors.New("token line differs from line number")
	// ErrColumnNotIncreasing indicates a column is not greater than the previous.
	ErrColumnNotIncreasing = errors.New("column not greater than previous")
	// ErrEmptyLineNumbered indicates a line with no tokens has a number.
	ErrEmptyLineNumbered = errors.New("line with no tokens has a number")
)

// ValidateLines checks the integrity of ls.
//
// It checks that:
//   - Line numbers are strictly increasing
//   - Every token on a given line is non-nil and carries a Position
//   - Every token on a given line carries the line's [line.Line.Number] in
//     its Position
//   - Every token on a given line has columns that are strictly increasing,
//     ignoring tokens with an empty Origin
//
// A line with no tokens must be a numberless placeholder, such as the blank
// row a side-by-side diff inserts. ValidateLines rejects such a line when it
// has a number and otherwise leaves it out of the numbering check.
//
// Returns an error wrapping [ErrEmptyLineNumbered],
// [ErrLineNumberNotIncreasing], [ErrLineNumberMismatch], or
// [ErrColumnNotIncreasing] for the first check that fails. A nil token or a
// token without a Position yields an error wrapping a
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
