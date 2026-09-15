// Package lexers is the module's entry point to the go-yaml lexer.
//
// The underlying [lexer] produces a flat token stream for an entire YAML file,
// regardless of how many documents it contains. Every token stream niceyaml
// works with comes from [Tokenize], so a future change of lexer
// touches this package alone.
//
// # Usage
//
// For single-document YAML, use [Tokenize]:
//
//	tokens := lexers.Tokenize("key: value")
//
// For multi-document YAML, use [TokenizeDocuments]:
//
//	yaml := "doc1: a\n---\ndoc2: b\n---\ndoc3: c"
//	for idx, tokens := range lexers.TokenizeDocuments(yaml) {
//	    // Process each document's tokens independently
//	}
//
// The iterator supports early termination if you only need specific documents.
//
// To split an existing [token.Tokens] stream rather than raw text, use
// [tokens.SplitDocuments], which [TokenizeDocuments] builds on, so both
// place document boundaries the same way.
//
// # Document Boundaries
//
// A document header ("---") starts a new document and belongs to the
// start of it. A document end marker ("...") closes the current document and
// belongs to the end of it, so content that follows without a header
// forms a new document. The first document may or may not have a header.
package lexers
