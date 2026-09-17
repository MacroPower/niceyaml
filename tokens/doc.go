// Package tokens is the module's entry point to the go-yaml lexer and
// provides utilities for working with its token streams: document splitting
// and line ending handling.
//
// # Tokenizing
//
// [Tokenize] returns the token stream for a YAML file. It is the one place
// niceyaml calls the go-yaml lexer, so a future change of lexer touches this
// package alone:
//
//	tks := tokens.Tokenize("key: value")
//
// # Line Endings
//
// The go-yaml lexer keeps line endings in a token's Origin and may split a
// CRLF ending across two tokens. [TrimLineEnding] strips whichever form a
// token carries so callers can measure and compare content consistently.
//
// # Multi-Document YAML
//
// [SplitDocuments] splits a token stream at document headers ("---") and
// document end markers ("..."), returning an iterator over separate token
// streams for each YAML document. Use [WithResetPositions] to receive clones
// whose positions match a fresh tokenize of each document's text:
//
//	for idx, doc := range tokens.SplitDocuments(tokens.Tokenize(src)) {
//		// Process each document's tokens independently.
//	}
//
// A document header ("---") starts a new document and belongs to the start
// of it. A document end marker ("...") closes the current document and
// belongs to the end of it, so content that follows without a header forms
// a new document. The first document may or may not have a header.
package tokens
