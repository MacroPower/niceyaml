// Package ansi provides utilities for handling ANSI.
//
// # Escaping Control Characters
//
// When displaying raw content that may contain ANSI escape sequences or other
// control characters, terminals interpret these bytes rather than showing them.
//
// The [Escape] function replaces control characters with visible Unicode
// representations, making them safe to display without affecting terminal state.
//
//	escaped := ansi.Escape("\x1b[31mRed\x1b[0m") // "␛[31mRed␛[0m"
package ansi
