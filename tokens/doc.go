// Package tokens provides utilities for working with go-yaml token streams:
// document splitting, syntax highlighting categories, and line ending handling.
//
// # Line Endings
//
// The go-yaml lexer keeps line endings in a token's Origin and may split a
// CRLF ending across two tokens. [TrimLineEnding] strips whichever form a
// token carries so callers can measure and compare content consistently.
//
// # Syntax Highlighting
//
// [TypeStyle] maps token types to [style.Style] values for syntax highlighting.
// It handles context-sensitive styling: a string followed by a colon is styled
// as a mapping key, not a plain string.
//
// # Multi-Document YAML
//
// [SplitDocuments] splits a token stream at document headers ("---") and
// document end markers ("..."), returning an iterator over separate token
// streams for each YAML document. Use [WithResetPositions] to reset token
// positions so each document starts from line 1, column 1.
package tokens
