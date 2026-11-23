package yamlscan

import (
	"io"

	"github.com/goccy/go-yaml/token"
)

// Tokenize split to token instances from string
func Tokenize(src string) token.Tokens {
	var s Scanner
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
		// niceyaml: a source of whitespace alone, which the scan does not
		// emit a token for, gets one token that holds its text.
		tokens = token.Tokens{{
			Type:          token.StringType,
			CharacterType: token.CharacterTypeMiscellaneous,
			Indicator:     token.NotIndicator,
			Value:         src,
			Origin:        src,
			Position:      &token.Position{Line: 1, Column: 1, Offset: 1},
		}}
	}
	return tokens
}
