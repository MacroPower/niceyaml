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
// document end markers ("..."). It returns an iterator over the token
// stream of each YAML document. The tokens keep the positions they have in
// the whole stream, and [ResetPositions] clones a document's tokens with
// positions that count from line 1, as a fresh tokenize of its text would:
//
//	for idx, doc := range tokens.SplitDocuments(tokens.Tokenize(src)) {
//		standalone := tokens.ResetPositions(doc)
//	}
//
// The first document may or may not have a header. See [SplitDocuments] for
// the rest of the boundary rules.
package tokens
