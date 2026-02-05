package lexers

import (
	"errors"
	"io"
	"iter"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/scanner"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/tokens"
)

// Tokenize returns a token stream for the given YAML source.
//
// This is a convenience wrapper around the go-yaml lexer.
func Tokenize(src string) token.Tokens {
	return lexer.Tokenize(src)
}

// TokenizeDocumentsOption configures [TokenizeDocuments].
//
// Available options:
//   - [WithResetPositions]
type TokenizeDocumentsOption func(*tokenizeDocumentsConfig)

type tokenizeDocumentsConfig struct {
	resetPositions bool
}

// WithResetPositions is a [TokenizeDocumentsOption] that resets token positions
// so each document starts from line 1, column 1.
//
// When enabled, tokens are cloned and their positions adjusted relative to the
// document's start. By default, positions are preserved from the original source.
func WithResetPositions() TokenizeDocumentsOption {
	return func(cfg *tokenizeDocumentsConfig) {
		cfg.resetPositions = true
	}
}

// TokenizeDocuments splits a YAML string into multiple [token.Tokens] streams,
// one for each YAML document found (separated by "---" markers).
//
// Unlike [Tokenize], which returns a flat token stream, this function yields
// document-aware streams via an iterator. The iterator yields (index, tokens)
// pairs, where index is the zero-based document number.
func TokenizeDocuments(src string, opts ...TokenizeDocumentsOption) iter.Seq2[int, token.Tokens] {
	cfg := &tokenizeDocumentsConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(yield func(int, token.Tokens) bool) {
		var (
			s       scanner.Scanner
			docIdx  int
			current token.Tokens
		)
		s.Init(src)

		yieldDoc := func(doc token.Tokens) bool {
			if cfg.resetPositions {
				doc = tokens.CloneWithResetPositions(doc)
			}

			return yield(docIdx, doc)
		}

		for {
			subTokens, err := s.Scan()
			if errors.Is(err, io.EOF) {
				break
			}

			for _, tk := range subTokens {
				if tk.Type == token.DocumentHeaderType && len(current) > 0 {
					if !yieldDoc(current) {
						return
					}

					current = token.Tokens{}
					docIdx++
				}

				current.Add(tk)
			}
		}

		if len(current) > 0 {
			if !yieldDoc(current) {
				return
			}
		}
	}
}
