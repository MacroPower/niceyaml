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
// file; [SplitDocuments] cuts it into one stream per document.
func Tokenize(src string) token.Tokens {
	tks := lexer.Tokenize(src)
	if len(tks) == 0 {
		return tks
	}

	// The lexer drops the source's final line ending, so a file that ends
	// in a blank line tokenizes like one that does not. Give the dropped
	// whitespace back to the last token so the stream covers the whole
	// file and the last line count matches the text.
	var joined strings.Builder

	for _, tk := range tks {
		joined.WriteString(tk.Origin)
	}

	if rest, ok := strings.CutPrefix(src, joined.String()); ok && rest != "" && strings.TrimSpace(rest) == "" {
		tks[len(tks)-1].Origin += rest
	}

	return tks
}

// TrimLineEnding returns s without its trailing line ending: "\n", "\r\n",
// or a bare "\r". The go-yaml lexer splits CRLF endings across tokens, so a
// token may end with the "\r" alone while the "\n" opens the next one.
func TrimLineEnding(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// SplitDocumentsOption configures [SplitDocuments].
//
// Available options:
//   - [WithResetPositions]
type SplitDocumentsOption func(*splitDocumentsConfig)

type splitDocumentsConfig struct {
	resetPositions bool
}

// WithResetPositions is a [SplitDocumentsOption] that sets whether token
// positions are reset to match a fresh tokenize of each document's text,
// which starts from line 1, column 1. A first token whose Origin opens with
// a line break, such as the one after a "..." marker, starts below line 1,
// where a fresh tokenize places it.
//
// When enabled, tokens are cloned and their positions adjusted relative to
// the document's start. The default is false, and the tokens then keep the
// positions they have in the original source.
func WithResetPositions(reset bool) SplitDocumentsOption {
	return func(cfg *splitDocumentsConfig) {
		cfg.resetPositions = reset
	}
}

// cloneWithResetPositions clones tokens and shifts their positions to where a
// fresh tokenize of their text would put them.
//
// The text starts with the Origin of the first token with a non-nil position.
// That Origin can open with whitespace and line breaks, such as the line break
// that ends a preceding "..." line, and a fresh tokenize places the token after
// them. The token lands at line 1, column 1, offset 1, where the lexer places
// the first token of a fresh stream, only when its Origin opens with neither.
// Every token moves by the same number of lines and the same offset distance,
// and tokens on the first line also move by the same number of columns.
//
// Tokens with nil positions are cloned but left with nil positions.
func cloneWithResetPositions(tks token.Tokens) token.Tokens {
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
// document's first token still links back to the previous document. Pass
// [WithResetPositions] to receive clones instead. Nil tokens in the stream
// are skipped.
func SplitDocuments(tks token.Tokens, opts ...SplitDocumentsOption) iter.Seq2[int, token.Tokens] {
	cfg := &splitDocumentsConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(yield func(int, token.Tokens) bool) {
		var (
			docIdx  int
			current token.Tokens
		)

		yieldDoc := func(doc token.Tokens) bool {
			if cfg.resetPositions {
				doc = cloneWithResetPositions(doc)
			}

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
