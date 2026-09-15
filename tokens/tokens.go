package tokens

import (
	"iter"
	"strings"

	"github.com/goccy/go-yaml/token"
)

// TrimLineEnding returns s without its trailing line ending: "\n", "\r\n",
// or a bare "\r". The go-yaml lexer splits CRLF endings across tokens, so a
// token may end with the "\r" alone while the "\n" opens the next one.
func TrimLineEnding(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// ValueOffset returns the byte offset where Value starts within the first
// line of the [*token.Token]'s Origin, or 0 when the first line does not
// contain it.
func ValueOffset(tk *token.Token) int {
	firstLine, _, _ := strings.Cut(tk.Origin, "\n")
	if firstLine == "" {
		return 0
	}

	if idx := strings.Index(firstLine, tk.Value); idx >= 0 {
		return idx
	}

	return 0
}

// SplitDocumentsOption configures [SplitDocuments].
//
// Available options:
//   - [WithResetPositions]
type SplitDocumentsOption func(*splitDocumentsConfig)

type splitDocumentsConfig struct {
	resetPositions bool
}

// WithResetPositions is a [SplitDocumentsOption] that resets token positions
// so each document starts from line 1, column 1.
//
// When enabled, tokens are cloned and their positions adjusted relative to the
// document's start. By default, positions are preserved from the original source.
func WithResetPositions() SplitDocumentsOption {
	return func(cfg *splitDocumentsConfig) {
		cfg.resetPositions = true
	}
}

// CloneWithResetPositions clones tokens and adjusts positions relative to
// the document's starting position.
//
// The first token with a non-nil position determines the starting line, column,
// and offset. All subsequent token positions are adjusted relative to this start,
// so the first positioned token ends up at line 1, column 1, offset 1, which is
// where the lexer places the first token of a fresh stream.
//
// Tokens with nil positions are cloned but left with nil positions.
func CloneWithResetPositions(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	// Find starting position from first token with position.
	var startLine, startCol, startOffset int

	for _, tk := range tks {
		if tk != nil && tk.Position != nil {
			startLine = tk.Position.Line
			startCol = tk.Position.Column
			startOffset = tk.Position.Offset

			break
		}
	}

	result := make(token.Tokens, 0, len(tks))

	for _, tk := range tks {
		clone := tk.Clone()
		if clone.Position != nil {
			clone.Position.Line = clone.Position.Line - startLine + 1
			if clone.Position.Line == 1 {
				clone.Position.Column = clone.Position.Column - startCol + 1
			}

			clone.Position.Offset = clone.Position.Offset - startOffset + 1
		}

		result.Add(clone)
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
// [WithResetPositions] to receive clones instead.
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
				doc = CloneWithResetPositions(doc)
			}

			return yield(docIdx, doc)
		}

		for _, tk := range tks {
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
