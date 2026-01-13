package lexers

import (
	"errors"
	"io"
	"iter"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/scanner"
	"github.com/goccy/go-yaml/token"
)

// Tokenize wraps [lexer.Tokenize] for convenience.
func Tokenize(src string) token.Tokens {
	return lexer.Tokenize(src)
}

// TokenizeDocuments is like [lexer.Tokenize], but splits the YAML string into multiple
// token streams, one for each YAML document found (separated by '---' tokens).
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
