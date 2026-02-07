// Package lexers provides document-aware YAML tokenization.
//
// The underlying [lexer] produces a flat token stream for an entire YAML file,
// regardless of how many documents it contains.
//
// This package adds [TokenizeDocuments] to split multi-document YAML into
// separate token streams, enabling per-document processing without manual
// boundary detection.
//
// # Usage
//
// For single-document YAML, use [Tokenize] (a thin wrapper around the
// underlying lexer):
//
//	tokens := lexers.Tokenize("key: value")
//
// For multi-document YAML separated by "---" markers, use [TokenizeDocuments]:
//
//	yaml := "doc1: a\n---\ndoc2: b\n---\ndoc3: c"
//	for idx, tokens := range lexers.TokenizeDocuments(yaml) {
//	    // Process each document's tokens independently
//	}
//
// The iterator supports early termination if you only need specific documents.
//
// To split an existing [token.Tokens] stream rather than raw text, see
// [tokens.SplitDocuments].
//
// # Document Boundaries
//
// [TokenizeDocuments] splits on [token.DocumentHeaderType] tokens ("---").
//
// The first document may or may not have a header; subsequent documents always
// start with one.
//
// Document end markers ("...") are included in their document's token stream
// but do not trigger splits.
package lexers
