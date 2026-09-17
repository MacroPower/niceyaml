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
	// ErrLineNumberMismatch indicates a token's line number differs from expected.
	ErrLineNumberMismatch = errors.New("token line number differs from expected")
	// ErrColumnNotIncreasing indicates a column is not greater than the previous.
	ErrColumnNotIncreasing = errors.New("column not greater than previous")
	// ErrNilLine indicates a nil line in the collection.
	ErrNilLine = errors.New("line is nil")
)

// ValidateLines checks the integrity of ls.
//
// It ensures that:
//   - Line numbers are strictly increasing
//   - Every token on a given line has an identical line number in its Position
//   - Every token on a given line has columns that are strictly increasing
//
// A line with no tokens is a placeholder and its number is not checked.
//
// Returns an error wrapping [ErrNilLine], [ErrLineNumberNotIncreasing],
// [ErrLineNumberMismatch], or [ErrColumnNotIncreasing] for the first check
// that fails.
func ValidateLines(ls line.Lines) error {
	prevLineNum := 0

	for i, l := range ls {
		if l == nil {
			return fmt.Errorf("line at index %d: %w", i, ErrNilLine)
		}

		// A line with no tokens is a placeholder, such as the blank row a
		// side-by-side diff inserts, and carries no number to check. Every
		// other line must number above the one before it.
		if len(l.Tokens()) == 0 {
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

		// Check: all tokens have identical line number and columns are strictly increasing.
		var (
			expectedLineNum = -1
			prevCol         = 0
		)

		for j, tk := range l.Tokens() {
			if tk == nil || tk.Position == nil {
				continue
			}

			// Check token line number consistency.
			if expectedLineNum == -1 {
				expectedLineNum = tk.Position.Line
			} else if tk.Position.Line != expectedLineNum {
				return fmt.Errorf(
					"line at index %d, token %d: line number %d differs from expected %d: %w",
					i,
					j,
					tk.Position.Line,
					expectedLineNum,
					ErrLineNumberMismatch,
				)
			}

			// Check columns strictly increasing.
			//
			// Skip check for zero-width tokens (empty Origin) as they don't occupy
			// column space.
			//
			// The lexer can produce tokens at the same position (e.g., empty block
			// scalar content). It also gives multi-line block scalar content
			// that other content follows Column 0, a marker rather than a
			// column, so such a token neither fails the check nor moves the
			// baseline.
			if tk.Origin != "" && tk.Position.Column != 0 {
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
