package lexers

import (
	"iter"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/tokens"
)

// Tokenize returns the token stream for the given YAML source.
//
// It is the one place niceyaml calls the go-yaml lexer, so every token stream
// the module works with comes through here.
func Tokenize(src string) token.Tokens {
	return lexer.Tokenize(src)
}

// TokenizeDocumentsOption configures [TokenizeDocuments]. It is the same
// option type [tokens.SplitDocuments] accepts.
//
// Available options:
//   - [WithResetPositions]
type TokenizeDocumentsOption = tokens.SplitDocumentsOption

// WithResetPositions is a [TokenizeDocumentsOption] that resets token
// positions so they match a fresh [Tokenize] of each document's text. It is
// [tokens.WithResetPositions] under another name.
func WithResetPositions() TokenizeDocumentsOption {
	return tokens.WithResetPositions()
}

// TokenizeDocuments tokenizes src and yields one [token.Tokens] stream per
// YAML document, with the zero-based document index.
//
// It is [Tokenize] followed by [tokens.SplitDocuments], so the two agree on
// document boundaries and on token sharing. A document header ("---") starts a
// new document and a document end marker ("...") closes one. The yielded
// tokens keep the Next and Prev links of the full stream unless
// [WithResetPositions] asks for clones.
func TokenizeDocuments(src string, opts ...TokenizeDocumentsOption) iter.Seq2[int, token.Tokens] {
	return tokens.SplitDocuments(Tokenize(src), opts...)
}
