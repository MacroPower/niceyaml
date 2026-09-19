package tokens

import (
	"iter"
	"strings"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
)

// Tokenize returns the token stream for the given YAML source.
//
// It is the one place niceyaml calls the go-yaml lexer, so every token stream
// the module works with comes through here. The stream covers the whole
// file, except where the lexer itself drops text: a tab used as indentation
// swallows the characters after it into an invalid token, and a "\u"
// escape in a double-quoted scalar comes back decoded rather than as
// written. [SplitDocuments] cuts the stream into one stream per document.
func Tokenize(src string) token.Tokens {
	tks := lexer.Tokenize(src)
	if len(tks) == 0 {
		if src == "" {
			return tks
		}

		// The lexer emits nothing for a source of whitespace alone, so
		// give the stream one token holding the whole text, positioned
		// where the lexer places the first token of a file.
		return token.Tokens{{
			Type:          token.StringType,
			CharacterType: token.CharacterTypeMiscellaneous,
			Indicator:     token.NotIndicator,
			Value:         src,
			Origin:        src,
			Position:      &token.Position{Line: 1, Column: 1, Offset: 1},
		}}
	}

	// The lexer drops the source's final line ending, so a file that ends
	// in a blank line tokenizes like one that does not. Give the dropped
	// whitespace back to the last token so the stream ends where the file
	// does and the last line count matches the text. The rest is found
	// behind the last token's text rather than behind the joined origins,
	// which need not be a prefix of the source when the lexer dropped text
	// earlier in the file.
	last := tks[len(tks)-1]

	i := strings.LastIndex(src, last.Origin)
	if i < 0 {
		return tks
	}

	if rest := src[i+len(last.Origin):]; rest != "" && strings.TrimSpace(rest) == "" {
		last.Origin += rest
	}

	return tks
}

// TrimLineEnding returns s without its trailing line ending: "\n", "\r\n",
// or a bare "\r". The go-yaml lexer splits CRLF endings across tokens, so a
// token may end with the "\r" alone while the "\n" opens the next one.
func TrimLineEnding(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// ResetPositions clones tks and shifts the positions of the clones so that
// a stream cut from a longer one, such as the tokens of one document from
// [SplitDocuments], counts its lines from 1 as [Tokenize] does. Every token
// moves by the same number of lines and the same offset distance, and
// tokens on the first line also move by the same number of columns. Tokens
// that start at line 1 already come back as clones with the same positions.
// The shift is uniform, so a position the lexer placed oddly, such as block
// scalar content followed by a document header, which it places on the
// header's line, stays odd rather than moving to where a fresh tokenize of
// the cut text alone would put it.
//
// The text starts with the Origin of the first token with a non-nil position.
// That Origin can open with whitespace and line breaks, such as the line break
// that ends a preceding "..." line, and a fresh tokenize places the token after
// them. The token lands at line 1, column 1, offset 1, where the lexer places
// the first token of a fresh stream, only when its Origin opens with neither.
//
// The clones link to each other through Next and Prev and to nothing outside
// the result, so the stream stands alone. Tokens with nil positions are
// cloned but left with nil positions, and nil tokens are dropped.
func ResetPositions(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	// Find where the text starts from the first token with a position.
	var startLine, startCol, startOffset int

	for _, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		// The position points past the whitespace and line breaks that open
		// the Origin, and the text still holds them.
		lead := tk.Origin[:len(tk.Origin)-len(strings.TrimLeft(tk.Origin, " \t\r\n"))]
		lastLine := lead[strings.LastIndexByte(lead, '\n')+1:]

		startLine = tk.Position.Line - strings.Count(lead, "\n")
		startCol = tk.Position.Column - len(lastLine)
		startOffset = tk.Position.Offset - len(lead)

		break
	}

	result := make(token.Tokens, 0, len(tks))

	for _, tk := range tks {
		clone := tk.Clone()
		if clone == nil {
			continue
		}

		if clone.Position != nil {
			clone.Position.Line = clone.Position.Line - startLine + 1
			if clone.Position.Line == 1 {
				clone.Position.Column = clone.Position.Column - startCol + 1
			}

			clone.Position.Offset = clone.Position.Offset - startOffset + 1
		}

		result.Add(clone)
	}

	// Clone copies Next and Prev, and Add rewires only the links between
	// clones, so the first and last clone still point at the un-cloned
	// tokens of the neighboring documents. Sever those links so the
	// document stands alone.
	if len(result) > 0 {
		result[0].Prev = nil
		result[len(result)-1].Next = nil
	}

	return result
}

// SplitDocuments splits a token stream into multiple token streams, one for
// each YAML document.
//
// A document header token ('---') starts a new document and is included at
// the start of it. A document end token ('...') closes the current document
// and is included at the end of it, so content that follows without a header
// forms a new document. These are the boundaries the go-yaml parser uses for
// well-formed streams. The parser may produce fewer documents than this
// function yields, for example when consecutive headers collapse, so pair
// the two by token offset rather than by index.
//
// The returned slices each contain tokens for a single document, preserving
// original token order and positions. The tokens are the caller's, not
// copies, and they keep the Next and Prev links of the full stream, so a
// document's first token still links back to the previous document. Pass a
// document to [ResetPositions] for clones that count lines from 1. Nil
// tokens in the stream are skipped.
func SplitDocuments(tks token.Tokens) iter.Seq2[int, token.Tokens] {
	return func(yield func(int, token.Tokens) bool) {
		var (
			docIdx  int
			current token.Tokens
		)

		yieldDoc := func(doc token.Tokens) bool {
			return yield(docIdx, doc)
		}

		for _, tk := range tks {
			if tk == nil {
				continue
			}

			if tk.Type == token.DocumentHeaderType && len(current) > 0 {
				if !yieldDoc(current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}

			current = append(current, tk)

			if tk.Type == token.DocumentEndType {
				if !yieldDoc(current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}
		}

		if len(current) > 0 {
			if !yieldDoc(current) {
				return
			}
		}
	}
}
