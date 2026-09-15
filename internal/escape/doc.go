// Package escape makes control characters visible in rendered text.
//
// # Escaping Control Characters
//
// When displaying raw content that may contain ANSI escape sequences or other
// control characters, terminals interpret these bytes rather than showing them.
//
// The [Control] function replaces control characters with visible Unicode
// representations, making them safe to display without affecting terminal state.
//
//	escaped := escape.Control("\x1b[31mRed\x1b[0m") // "␛[31mRed␛[0m"
package escape
