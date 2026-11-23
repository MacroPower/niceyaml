// Package tokens reads the token streams of YAML files. It also provides
// utilities for working with those streams: position resets and line
// ending handling.
//
// # Tokenizing
//
// [Tokenize] returns the token stream for a YAML file. It reads the tokens
// with niceyaml's copy of the go-yaml scanner. The tokens that the go-yaml
// parser reads have the types and values of the go-yaml lexer, with the
// exception that [Tokenize] lists. The Origins of all tokens join to the
// text of the file:
//
//	tks := tokens.Tokenize("key: value")
//
// # Parser Input
//
// The go-yaml parser rejects some valid documents in a stream from
// [Tokenize], and in the tokens of a
// [go.jacobcolvin.com/niceyaml.Source]. Hand the parser the stream that
// [ForParser] returns:
//
//	f, err := parser.Parse(tokens.ForParser(src.Tokens()), parser.ParseComments)
//
// The parser then builds the tree that go-yaml builds from the text. For
// the tree of [go.jacobcolvin.com/niceyaml.Source.File], take the File of
// a second Source, as Source.File describes.
//
// # Line Endings
//
// The go-yaml lexer keeps line endings in a token's Origin. A line ends
// with "\n", "\r\n", or a bare "\r". [TrimLineEnding] strips whichever
// form a token carries so callers can measure and compare content
// consistently.
//
// # Multi-Document YAML
//
// This package does not split a stream into documents, because the
// bounds of a document depend on the parse. The root package builds the
// documents from the parse. It moves the comments and directives above a
// "---" header into the document that the header opens. A cut at the
// markers alone gives different documents.
// [go.jacobcolvin.com/niceyaml.Source.AllDocuments] returns each document,
// including the documents the parser rejects. The
// [go.jacobcolvin.com/niceyaml.Node.Tokens] of a document hold its tokens
// with the positions they have in the whole stream. [ResetPositions]
// clones them with positions that count from line 1, as a fresh tokenize
// of their text does:
//
//	src := niceyaml.NewSourceFromString(text)
//	for _, doc := range src.AllDocuments() {
//		standalone := tokens.ResetPositions(doc.Tokens())
//	}
package tokens
