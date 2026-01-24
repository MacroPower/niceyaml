package lexers

import (
	"errors"
	"io"
	"iter"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/scanner"
	"github.com/goccy/go-yaml/token"
)

// Tokenize returns a token stream for the given YAML source.
//
// This is a convenience wrapper around the go-yaml lexer.
func Tokenize(src string) token.Tokens {
	return lexer.Tokenize(src)
}

// TokenizeDocuments splits a YAML string into multiple [token.Tokens] streams,
// one for each YAML document found (separated by "---" markers).
//
// Unlike [Tokenize], which returns a flat token stream, this function yields
// document-aware streams via an iterator. The iterator yields (index, tokens)
// pairs, where index is the zero-based document number.
func TokenizeDocuments(src string) iter.Seq2[int, token.Tokens] {
	return func(yield func(int, token.Tokens) bool) {
		var (
			s       scanner.Scanner
			docIdx  int
			current token.Tokens
		)
		s.Init(src)

		for {
			subTokens, err := s.Scan()
			if errors.Is(err, io.EOF) {
				break
			}

			for _, tk := range subTokens {
				if tk.Type == token.DocumentHeaderType && len(current) > 0 {
					if !yield(docIdx, current) {
						return
					}

					current = token.Tokens{}
					docIdx++
				}

				current.Add(tk)
			}
		}

		if len(current) > 0 {
			if !yield(docIdx, current) {
				return
			}
		}
	}
}
