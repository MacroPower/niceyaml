// Package reposition shifts the positions of a token stream in place, so
// a stream cut from a longer one counts its lines from 1.
//
// [go.jacobcolvin.com/niceyaml/tokens.ResetPositions] clones a stream and
// shifts the clones with this package. A caller that holds the only
// reference to a stream, such as the stream that
// [go.jacobcolvin.com/niceyaml/tokens.Tokenize] returns, shifts it without
// the clones.
package reposition

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

// Reset shifts the positions of the tokens of tks as
// [go.jacobcolvin.com/niceyaml/tokens.ResetPositions] shifts those of its
// clones. It returns the tokens as ResetPositions returns the clones, in
// order and without nil tokens, and it links them to each other through
// Next and Prev alone. It changes the tokens and the slice of tks in
// place, so pass it a stream that nothing else reads, such as the stream
// that [go.jacobcolvin.com/niceyaml/tokens.Tokenize] returns. The result
// shares the memory of tks.
func Reset(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	// Find where the text starts from the first token that holds content.
	// A stream without one starts where the lexer starts a fresh stream.
	startLine, startCol, startOffset := 1, 1, 1

	var (
		ws     strings.Builder
		anchor *token.Token
		lead   string
	)

	for _, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		// An empty token shares the position of the text after it, so the
		// whitespace that opens that text's Origin still lies ahead of it.
		// It anchors only when no token holds text.
		if tk.Origin == "" {
			if anchor == nil {
				anchor, lead = tk, ws.String()
			}

			continue
		}

		// The position points past the whitespace and line breaks that open
		// the Origin, and the text still holds them. An Origin holding
		// whitespace alone has no such split, so it anchors nothing, but its
		// whitespace still comes before the anchor's text.
		trimmed := strings.TrimLeft(tk.Origin, " \t\r\n")
		if trimmed == "" {
			ws.WriteString(tk.Origin)

			continue
		}

		// Join the whitespace before counting it, so a "\r" that ends one
		// Origin and a "\n" that opens the next count as one CRLF break.
		anchor, lead = tk, ws.String()+tk.Origin[:len(tk.Origin)-len(trimmed)]

		break
	}

	if anchor != nil {
		lastLine := lead[strings.LastIndexAny(lead, "\r\n")+1:]

		startLine = anchor.Position.Line - lineend.CountBreaks(lead)
		startCol = anchor.Position.Column - len(lastLine)
		startOffset = anchor.Position.Offset - len(lead)
	}

	// Each token lands at or before its index in tks, so the result can
	// take the slice of tks.
	result := tks[:0]

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		switch {
		case tk.Position == nil:
		case anchor == nil:
			tk.Position.Line, tk.Position.Column, tk.Position.Offset = 1, 1, 1
		default:
			tk.Position.Line = tk.Position.Line - startLine + 1
			if tk.Position.Line == 1 {
				tk.Position.Column = tk.Position.Column - startCol + 1
			}

			tk.Position.Offset = tk.Position.Offset - startOffset + 1
		}

		result.Add(tk)
	}

	clear(tks[len(result):])

	// The last token still links to a token after it when the caller cut
	// tks before the end of its stream, so no text of the stream follows
	// the tokens without text that end it.
	if len(result) > 0 && result[len(result)-1].Next != nil {
		endTrailing(result)
	}

	// Add rewires only the links between the tokens of the result, so the
	// first and last token can still point at tokens of the neighboring
	// documents. Sever those links so the document stands alone.
	if len(result) > 0 {
		result[0].Prev = nil
		result[len(result)-1].Next = nil
	}

	return result
}

// endTrailing moves the tokens of tks that have a position and follow the
// last token that holds text. It moves them to the end of the last line of
// the joined Origins, before its final line ending. That is where
// [go.jacobcolvin.com/niceyaml/tokens.Tokenize] places a token without
// text after the last text of a source. The positions of tks count from
// line 1, column 1, offset 1 at the start of the joined Origins. A stream
// without a token that holds text keeps its positions, which [Reset] has
// already moved to line 1, column 1, offset 1.
func endTrailing(tks token.Tokens) {
	last := -1

	for i, tk := range tks {
		if tk.Position != nil && strings.Trim(tk.Origin, " \t\r\n") != "" {
			last = i
		}
	}

	if last < 0 || last == len(tks)-1 {
		return
	}

	var sb strings.Builder

	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	text := lineend.Trim(sb.String())
	line, col := lineend.Advance(1, 1, text)
	offset := utf8.RuneCountInString(text) + 1

	for _, tk := range tks[last+1:] {
		if tk.Position != nil {
			tk.Position.Line, tk.Position.Column, tk.Position.Offset = line, col, offset
		}
	}
}
