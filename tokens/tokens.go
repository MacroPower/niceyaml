package tokens

import (
	"iter"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

// Tokenize returns the token stream for the given YAML source.
//
// It is the one place niceyaml calls the go-yaml lexer, so every token stream
// the module works with comes through here. The stream covers the whole
// file, except where the lexer itself drops text: a tab used as indentation
// swallows the characters after it into an invalid token, and a "\x", "\u",
// or "\U" escape in a double-quoted scalar truncates the token's Origin at
// the escape, leaving the text before the escape and a closing quote, so the
// rest of the scalar never reaches the stream. The lexer also drops the line
// breaks and indentation in front of some tokens, such as the blank lines
// before a "?" or ":" indicator that follows a flow collection, a quoted
// scalar, or a comment. Tokenize gives them back at the start of that
// token's Origin, with each blank line as a bare line ending, the way the
// lexer keeps the blank lines it does not drop. [SplitDocuments] cuts the
// stream into one stream per document.
//
// Tokenize drops a UTF-8 byte order mark where YAML allows one: at the
// start of a line before the content of a document, and in front of a
// "---" or "..." marker. The lexer would read the mark as text and put it
// in the first key. Every position names a rune of the text without
// those marks.
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
	src = dropByteOrderMarks(src)

	tks := lexer.Tokenize(src)
	if len(tks) == 0 {
		if src == "" {
			return tks
		}

		// The lexer emits nothing for a source of whitespace alone, and
		// nothing for some text it rejects outright, such as a lone "!",
		// so give the stream one token holding the whole text, positioned
		// where the lexer places the first token of a file, and the file
		// stays visible whatever the lexer made of it. [IsPlaceholder]
		// tells that token apart from one the lexer made.
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
	repairPastEnd(src, tks)

	return tks
}

// byteOrderMark is the UTF-8 byte order mark.
const byteOrderMark = "\ufeff"

// dropByteOrderMarks returns src without the byte order marks that open
// a line before the content of a document, or that stand in front of a
// document marker. A document starts at the start of src and after a
// marker line that holds nothing but the marker and a comment. Blank and
// comment lines keep the document before its content.
func dropByteOrderMarks(src string) string {
	var sb strings.Builder

	sb.Grow(len(src))

	prefix := true

	for line := range strings.SplitAfterSeq(src, "\n") {
		if rest, ok := strings.CutPrefix(line, byteOrderMark); ok && (prefix || isDocumentMarker(rest)) {
			line = rest
		}

		sb.WriteString(line)

		switch {
		case isDocumentMarker(line):
			prefix = isBlankOrComment(line[len("---"):])
		case isBlankOrComment(line):
		default:
			prefix = false
		}
	}

	return sb.String()
}

// isDocumentMarker reports whether line opens with a "---" or "..."
// marker followed by a space, a tab, or the end of the line.
func isDocumentMarker(line string) bool {
	if !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "...") {
		return false
	}

	rest := line[len("---"):]

	return rest == "" || strings.ContainsAny(rest[:1], " \t\r\n")
}

// isBlankOrComment reports whether line holds only whitespace, or a
// comment after it.
func isBlankOrComment(line string) bool {
	text := strings.TrimLeft(line, " \t")

	return strings.TrimRight(text, "\r\n") == "" || strings.HasPrefix(text, "#")
}

// repairPastEnd moves every token of tks that holds no text and sits past
// the end of src to the end of the last line that holds a rune. The lexer
// places the empty content of a block scalar that keeps its trailing
// lines on the line after the header, which is a line the source does
// not have when the header ends the file.
func repairPastEnd(src string, tks token.Tokens) {
	runes := utf8.RuneCountInString(src)

	var line, col, offset int

	for _, tk := range tks {
		if tk == nil || tk.Position == nil || strings.Trim(tk.Origin, " \t\r\n") != "" || tk.Position.Offset <= runes {
			continue
		}

		if line == 0 {
			line, col, offset = sourceEnd(src)
		}

		tk.Position.Line, tk.Position.Column, tk.Position.Offset = line, col, offset
	}
}

// sourceEnd returns the line of the last rune of src that is no part of
// its final line ending, the column just past that rune, and the rune
// offset of that place, counting all three from 1. An empty source ends
// at 1:1 with offset 1.
func sourceEnd(src string) (int, int, int) {
	src = TrimLineEnding(src)
	line, col := advance(1, 1, src)

	return line, col, utf8.RuneCountInString(src) + 1
}

// advance returns the line and column reached by moving from line and col
// across s. A line break moves to column 1 of the next line, and any
// other rune moves one column to the right.
func advance(line, col int, s string) (int, int) {
	for ln := range lineend.Lines(s) {
		text := strings.TrimRight(ln, "\r\n")
		if len(text) < len(ln) {
			line, col = line+1, 1

			continue
		}

		col += utf8.RuneCountInString(text)
	}

	return line, col
}

// IsPlaceholder reports whether tk is the token [Tokenize] made for text
// the lexer emits nothing for, such as a source of whitespace alone. The
// parser reads that token as a plain scalar holding the text, while the
// go-yaml Unmarshal reads the text as no value at all, so a decoder that
// wants to agree with it treats the token as no content. Tokenize makes the
// placeholder as the only token of its stream, so a token linked to a
// neighbor, such as the content of a block scalar, is never one.
func IsPlaceholder(tk *token.Token) bool {
	if tk == nil || tk.Type != token.StringType || tk.Origin != tk.Value || tk.Prev != nil || tk.Next != nil {
		return false
	}

	return len(lexer.Tokenize(tk.Origin)) == 0
}

// repairPositions moves every token of tks to the rune of src where its
// text starts, so Line, Column, and Offset agree with the source whatever
// the lexer counted. It walks src once from left to right, since each
// token starts at or after the end of the text before it, and leaves
// IndentNum and IndentLevel as they are. It gives each token the line
// breaks the lexer dropped in front of it, so the Origins ahead of a
// token's text hold as many lines as the source does.
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
	tail string // The whitespace the stream holds after the text placed last.

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
// where the next text starts and leaves the cursor where it is. When the
// text follows the cursor past whitespace alone, place gives tk the line
// breaks the lexer dropped from that whitespace.
func (p *positioner) place(tk *token.Token) {
	placed := false

	for ln := range lineend.Lines(tk.Origin) {
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
			// Only the whitespace between a reliable cursor and the text
			// shows the line breaks the lexer dropped. After a token the
			// lexer truncated or rewrote, the text sits where the lexer's
			// Offset points instead.
			reliable := p.reliable
			next := p.skipSpace()

			at = p.locate(tk, text)
			if reliable && at == next {
				p.restoreGap(tk, at)
			}

			p.setPosition(tk, at)

			placed = true
		}

		p.cursor = at + len(text)
	}

	if !placed {
		p.setPosition(tk, p.skipSpace())

		p.tail += tk.Origin

		return
	}

	p.tail = tk.Origin[len(strings.TrimRight(tk.Origin, " \t\r\n")):]
}

// restoreGap gives tk the line breaks and indentation the lexer dropped
// between the text placed last and the text of tk, which starts at rune
// index at. The lexer drops them in front of a "?" or ":" indicator that
// follows a flow collection, a quoted scalar, or a comment, and after a
// double-quoted scalar that holds a tab it drops one line of a run of
// blank lines. The line breaks the stream lacks go at the start of the
// Origin as bare line endings, the way the lexer keeps the blank lines it
// does not drop. Bare line endings keep tabs out of the Origin, and the
// parser rejects a key whose Origin holds a tab in front of a line break.
// When the lexer dropped every line break in front of tk, the indentation
// of the line tk starts on goes with them. An Origin stays as it is when
// the stream already holds every line break of the source.
func (p *positioner) restoreGap(tk *token.Token, at int) {
	gap := string(p.src[p.cursor:at])
	lead := tk.Origin[:len(tk.Origin)-len(strings.TrimLeft(tk.Origin, " \t\r\n"))]

	rest, ok := cutWhitespace(gap, p.tail)
	if !ok {
		// The lexer rewrote a line ending it kept, such as a CRLF it
		// turned into "\n" in an invalid token, so the gap matches the
		// stream by the count of line breaks alone.
		_, rest = cutLineBreaks(gap, lineend.CountBreaks(p.tail))
	}

	// The lexer keeps the last line breaks of the gap in the whitespace
	// the Origin opens with, so the first ones of rest are the ones it
	// dropped.
	missing := lineend.CountBreaks(rest) - lineend.CountBreaks(lead)
	if missing <= 0 {
		return
	}

	breaks, rest := cutLineBreaks(rest, missing)

	// When the lexer dropped every line break, rest is the indentation of
	// the line tk starts on, and the Origin opens with the part of it the
	// lexer kept.
	if indent, ok := strings.CutSuffix(rest, lead); ok && !strings.ContainsAny(lead, "\r\n") {
		breaks += indent
	}

	tk.Origin = breaks + tk.Origin
}

// cutLineBreaks returns the first n line breaks of s joined together,
// dropping the spaces and tabs between them, and the rest of s after the
// last of them. A CRLF counts as one line break.
func cutLineBreaks(s string, n int) (string, string) {
	var sb strings.Builder

	i := 0

	for ln := range lineend.Lines(s) {
		if n == 0 {
			break
		}

		sb.WriteString(ln[len(strings.TrimRight(ln, "\r\n")):])

		i += len(ln)
		n--
	}

	return sb.String(), s[i:]
}

// cutWhitespace returns gap without the whitespace ws the stream holds
// for its start, and reports whether gap opens with ws. The lexer drops
// the spaces and tabs that end a line of text and collapses a blank line
// of spaces into a bare line ending, so ws may lack some spaces and tabs
// of gap, but never a line break. A "\r" that ends ws leaves the "\n"
// of a CRLF in gap, where the lexer cut the CRLF between two tokens.
func cutWhitespace(gap, ws string) (string, bool) {
	i := 0

	for j := 0; j < len(ws); {
		switch {
		case i < len(gap) && gap[i] == ws[j]:
			i++
			j++

		case i < len(gap) && (gap[i] == ' ' || gap[i] == '\t'):
			i++
		default:
			return "", false
		}
	}

	return gap[i:], true
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
// Index at names the end of the source or a rune that is no line ending,
// so the runes counted never split a CRLF.
func (p *positioner) setPosition(tk *token.Token, at int) {
	if end := min(at, len(p.src)); p.idx < end {
		p.line, p.col = advance(p.line, p.col, string(p.src[p.idx:end]))
		p.idx = end
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

		startLine = tk.Position.Line - lineend.CountBreaks(lead)
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

		for _, tk := range tks {
			if tk == nil {
				continue
			}

			if tk.Type == token.DocumentHeaderType && len(current) > 0 {
				if !yield(docIdx, current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}

			current = append(current, tk)

			if tk.Type == token.DocumentEndType {
				if !yield(docIdx, current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}
		}

		if len(current) > 0 {
			yield(docIdx, current)
		}
	}
}
