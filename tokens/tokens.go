package tokens

import (
	"iter"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
)

// Tokenize returns the token stream for the given YAML source.
//
// It is the one place niceyaml calls the go-yaml lexer, so every token stream
// the module works with comes through here. The stream covers the whole
// file, except where the lexer itself drops text: a tab used as indentation
// swallows the characters after it into an invalid token, and a "\x", "\u",
// or "\U" escape in a double-quoted scalar truncates the token's Origin at
// the escape, leaving the text before the escape and a closing quote, so the
// rest of the scalar never reaches the stream. [SplitDocuments] cuts the
// stream into one stream per document.
//
// Every token's Line, Column, and Offset name the rune where its text
// starts, counting lines, columns, and offsets from 1 and offsets in runes.
// The Origin may open with whitespace and line endings before that rune.
// The text of a token is its Origin without the whitespace around it, and
// for a token cut across lines, such as a block scalar, the position names
// the first line that holds text. A token whose Origin holds no text, such
// as the empty content of a block scalar, sits where the next text starts,
// so it shares the position of the token after it. The lexer itself places
// a token behind the trailing spaces it drops from the Origin, one rune
// short after a comment or a tag, and on the last line of a multi-line
// block scalar, and Tokenize moves each such token to where the source
// holds its text.
func Tokenize(src string) token.Tokens {
	tks := lexer.Tokenize(src)
	if len(tks) == 0 {
		if src == "" {
			return tks
		}

		// The lexer emits nothing for a source of whitespace alone, and
		// nothing for some text it rejects outright, such as a lone "!",
		// so give the stream one token holding the whole text, positioned
		// where the lexer places the first token of a file, and the file
		// stays visible whatever the lexer made of it.
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
	// does and the last line count matches the text. Find the rest behind
	// the last token's text rather than behind the joined origins, which
	// need not be a prefix of the source when the lexer dropped text
	// earlier in the file. The search leaves the token's own trailing
	// whitespace out as well, because the lexer rewrites it. The lexer
	// collapses a blank line of spaces to a bare line ending, which leaves
	// an Origin the source does not hold. A token whose text the source
	// does not hold either keeps the Origin it came with.
	last := tks[len(tks)-1]

	text := strings.TrimRight(last.Origin, " \t\r\n")

	if i := strings.LastIndex(src, text); i >= 0 {
		if rest := src[i+len(text):]; rest != "" && strings.TrimSpace(rest) == "" {
			last.Origin = text + rest
		}
	}

	repairPositions(src, tks)

	return tks
}

// repairPositions moves every token of tks to the rune of src where its
// text starts, so Line, Column, and Offset agree with the source whatever
// the lexer counted. It walks src once from left to right, since each
// token starts at or after the end of the text before it, and leaves
// IndentNum and IndentLevel as they are.
func repairPositions(src string, tks token.Tokens) {
	p := &positioner{src: []rune(src), line: 1, col: 1, reliable: true}

	for _, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		p.place(tk)
	}
}

// positioner finds the rune each token's text starts at.
//
// The cursor stands just past the last text placed, and the next token's
// text starts at the first rune after it that is not whitespace, because
// the stream covers the source. The lexer's Offset serves as a second
// candidate. The lexer counts the runes it consumes, so its Offset stands a
// fixed distance from the truth until it drops or repeats a rune, and that
// distance is delta. The offset candidate is what places the tokens after a
// double-quoted scalar whose Origin the lexer truncated at an escape, where
// the cursor stops short and the whitespace rule would land inside the
// scalar. The cursor is reliable again once a token is found by either
// candidate.
type positioner struct {
	src []rune

	cursor   int // The rune index just past the text placed so far.
	reliable bool

	// The rune index the line and column count runs up to.
	idx  int
	line int
	col  int

	delta int // The distance from the lexer's Offset to the rune found last.
}

// place moves tk to the rune where its text starts and advances the cursor
// past the text of every line of its Origin. A token without text sits
// where the next text starts and leaves the cursor where it is.
func (p *positioner) place(tk *token.Token) {
	placed := false

	for ln := range originLines(tk.Origin) {
		text := []rune(strings.Trim(ln, " \t\r\n"))
		if len(text) == 0 {
			continue
		}

		var at int

		if placed {
			// A later line of the token follows the text before it,
			// past the blank lines the lexer collapses into a bare
			// line ending.
			at = p.skipSpace()
			if !p.hasText(at, text) {
				p.reliable = false
			}
		} else {
			at = p.locate(tk, text)
			p.setPosition(tk, at)

			placed = true
		}

		p.cursor = at + len(text)
	}

	if !placed {
		p.setPosition(tk, p.skipSpace())
	}
}

// locate returns the rune index where text, the first text line of tk,
// starts. It prefers the first rune after the cursor that is not
// whitespace while the cursor is reliable, and the lexer's Offset shifted
// by delta otherwise, and takes whichever of the two holds the text when
// only one does. When neither does, the token is one the lexer rewrote,
// and it sits after the cursor, which is unreliable from there on.
func (p *positioner) locate(tk *token.Token, text []rune) int {
	b := p.skipSpace()
	foundB := p.hasText(b, text)

	a := tk.Position.Offset - 1 + p.delta
	foundA := tk.Position.Offset > 0 && a >= p.cursor && p.hasText(a, text)

	var at int

	switch {
	case foundA && foundB && !p.reliable:
		at = a
	case foundB:
		at = b
	case foundA:
		at = a
	default:
		p.reliable = false

		return b
	}

	p.reliable = true

	if tk.Position.Offset > 0 {
		p.delta = at - (tk.Position.Offset - 1)
	}

	return at
}

// setPosition writes the line, column, and offset of the rune at index at
// into tk, counting the runes between the last position written and it.
func (p *positioner) setPosition(tk *token.Token, at int) {
	for p.idx < at && p.idx < len(p.src) {
		r := p.src[p.idx]
		p.idx++

		if r == '\n' || (r == '\r' && (p.idx >= len(p.src) || p.src[p.idx] != '\n')) {
			p.line++
			p.col = 1
		} else {
			p.col++
		}
	}

	tk.Position.Line = p.line
	tk.Position.Column = p.col
	tk.Position.Offset = at + 1
}

// skipSpace returns the index of the first rune at or after the cursor that
// is not a space, tab, or line ending, or the length of the source when
// none follows.
func (p *positioner) skipSpace() int {
	i := p.cursor
	for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t' || p.src[i] == '\r' || p.src[i] == '\n') {
		i++
	}

	return i
}

// hasText reports whether the source holds text at rune index at.
func (p *positioner) hasText(at int, text []rune) bool {
	if at < 0 || at+len(text) > len(p.src) {
		return false
	}

	return slices.Equal(p.src[at:at+len(text)], text)
}

// originLines yields the lines of origin, each cut after its line ending:
// "\n", "\r\n", or a bare "\r", the three the lexer advances
// Position.Line on. An empty origin yields nothing.
func originLines(origin string) iter.Seq[string] {
	return func(yield func(string) bool) {
		start := 0

		for i := range len(origin) {
			switch origin[i] {
			case '\n':
			case '\r':
				if i+1 < len(origin) && origin[i+1] == '\n' {
					continue // The "\n" of a CRLF ends the line.
				}

			default:
				continue
			}

			if !yield(origin[start : i+1]) {
				return
			}

			start = i + 1
		}

		if start < len(origin) {
			yield(origin[start:])
		}
	}
}

// countLineBreaks returns the number of line breaks in s, counting "\r\n",
// "\n", and a bare "\r" as one each. The go-yaml lexer advances
// Position.Line on all three.
func countLineBreaks(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
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
// The clones keep the invariant of [Tokenize], so Line, Column, and Offset
// name the rune where the token's text starts, and the Origin may open with
// whitespace and line endings before it.
//
// The text starts with the Origin of the first token that has a non-nil
// position and holds something other than whitespace. That Origin can open
// with whitespace and line breaks, such as the line break that ends a
// preceding "..." line, and a fresh tokenize places the token after them. The
// token lands at line 1, column 1, offset 1, where the lexer places the first
// token of a fresh stream, only when its Origin opens with neither. A stream
// carrying whitespace alone, whose token [Tokenize] positions ahead of the
// whitespace rather than inside it, keeps the positions it has.
//
// The clones link to each other through Next and Prev and to nothing outside
// the result, so the stream stands alone. Tokens with nil positions come
// back as clones with nil positions, and ResetPositions drops nil tokens.
func ResetPositions(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	// Find where the text starts from the first token that holds content.
	// A stream without one starts where the lexer starts a fresh stream.
	startLine, startCol, startOffset := 1, 1, 1

	for _, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		// The position points past the whitespace and line breaks that open
		// the Origin, and the text still holds them. An Origin holding
		// whitespace alone has no such split, so it anchors nothing.
		trimmed := strings.TrimLeft(tk.Origin, " \t\r\n")
		if trimmed == "" && tk.Origin != "" {
			continue
		}

		lead := tk.Origin[:len(tk.Origin)-len(trimmed)]
		lastLine := lead[strings.LastIndexAny(lead, "\r\n")+1:]

		startLine = tk.Position.Line - countLineBreaks(lead)
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
// A document header token ('---') starts a new document and belongs to the
// start of it. A document end token ('...') closes the current document and
// belongs to the end of it, so content that follows without a header forms
// a new document. These are the boundaries the go-yaml parser uses for
// well-formed streams. The parser may produce fewer documents than this
// function yields, for example when consecutive headers collapse, so pair
// the two by token offset rather than by index.
//
// Each returned slice holds the tokens of one document in their original
// order and with their original positions. The tokens are the caller's, not
// copies, and they keep the Next and Prev links of the whole stream, so a
// document's first token still links back to the previous document. Pass a
// document to [ResetPositions] for clones that count lines from 1.
// SplitDocuments skips nil tokens in the stream.
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
