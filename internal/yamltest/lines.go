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
)

// ValidateLines checks the integrity of ls.
//
// It ensures that:
//   - Line numbers are strictly increasing
//   - Every token on a given line has an identical line number in its Position
//   - Every token on a given line has columns that are strictly increasing
//
// Returns an error wrapping [ErrLineNumberNotIncreasing],
// [ErrLineNumberMismatch], or [ErrColumnNotIncreasing] for the first check
// that fails.
func ValidateLines(ls line.Lines) error {
	prevLineNum := 0

	for i, l := range ls {
		// Check: line numbers strictly increasing.
		lineNum := l.Number()
		if lineNum != 0 && lineNum <= prevLineNum {
			return fmt.Errorf(
				"line at index %d: line number %d not greater than previous %d: %w",
				i,
				lineNum,
				prevLineNum,
				ErrLineNumberNotIncreasing,
			)
		}

		if lineNum != 0 {
			prevLineNum = lineNum
		}

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
			// scalar content).
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
