package yamlscan

import (
	"io"
	"strings"

	"github.com/goccy/go-yaml/token"
)

// Tokenize split to token instances from string
func Tokenize(src string, opts ...Option) token.Tokens {
	var s Scanner
	// niceyaml: apply the options of the scanner.
	for _, opt := range opts {
		opt(&s)
	}
	s.Init(src)
	var tokens token.Tokens
	for {
		subTokens, err := s.Scan()
		if err == io.EOF {
			break
		}
		tokens.Add(subTokens...)
	}
	switch {
	case len(tokens) > 0:
		// niceyaml: the last token takes the text that ends the input,
		// because the last scan held no token to take it. Then place the
		// restored whitespace and count the positions from the joined
		// origins.
		tokens[len(tokens)-1].Origin += s.rest
		settleWhitespace(tokens, s.drops)
		setPositions(tokens)
	case src != "":
		// niceyaml: a source without a token, such as one of whitespace
		// alone, gets one token that holds its text.
		tokens = token.Tokens{{
			Type:          token.StringType,
			CharacterType: token.CharacterTypeMiscellaneous,
			Indicator:     token.NotIndicator,
			Value:         src,
			Origin:        src,
			Position:      &token.Position{Line: 1, Column: 1, Offset: 1},
		}}
		if strings.Trim(src, " \t\r\n") != "" {
			setPositions(tokens)
		}
	}
	return tokens
}
