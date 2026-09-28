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
// the module works with comes through here. The lexer drops some text from
// the Origins, and Tokenize gives it back from the source. It restores the
// letter and hex digits of a "\x", "\u", or "\U" escape in a double-quoted
// scalar, also in the invalid token the lexer makes of a scalar that no
// quote closes, as when an escape reads the closing quote as a hex digit.
// It restores the spaces and tabs that end a line or fill a blank line,
// the space in front of a ":" after a quoted or alias key, and the space
// between a "-" and a "?". It restores the line breaks and indentation in
// front of some tokens, such as a "?" or ":" indicator that follows a flow
// collection, a quoted scalar, or a comment. When the lexer gives up on a
// "\u" or "\U" escape, such as one the source ends too soon for, it reads
// the escape's backslash into two tokens, and Tokenize leaves it in the
// second alone. [SplitDocuments] cuts the stream into one stream per
// document.
//
// Tokenize drops a UTF-8 byte order mark where YAML allows one: at the
// start of a line before the content of a document, and in front of a
// "---" or "..." marker. The lexer would read the mark as text and put it
// in the first key. Every position names a rune of the text without
// those marks.
//
// The joined Origins match that text, except in a few places Tokenize
// leaves as the lexer made them. The lexer drops some text outright, such
// as a lone "!" that ends the file. A tab used as indentation makes the
// lexer read an invalid token that can swallow the characters after it,
// such as a ":" indicator, and the text around such a token keeps the
// lexer's shape. The lexer also ends one token with a line ending and
// opens the next with it again, as after a tag that ends its line and
// after the invalid token it makes of text that follows a block scalar
// header. Tokenize keeps the repeat, and a blank line between the two
// tokens loses its spaces and tabs.
//
// Every token's Line, Column, and Offset name the rune where its text
// starts, counting lines, columns, and offsets from 1 and offsets in runes.
// The Origin may open with whitespace and line endings before that rune.
// The text of a token is its Origin without the whitespace around it, and
// for a token cut across lines, such as a block scalar, the position names
// the first line that holds text. A token whose Origin holds no text, such
// as the empty content of a block scalar, sits where the next text starts,
// so it shares the position of the token after it. A source of whitespace
// alone comes back as one token at line 1, column 1. The lexer itself
// places a token behind the trailing spaces it drops, one rune short after
// a comment or a tag, and on the last line of a multi-line block scalar.
// Tokenize moves each such token to where the source holds its text, and
// the Origins hold the spaces the lexer dropped.
func Tokenize(src string) token.Tokens {
	src = dropByteOrderMarks(src)

	tks := lexer.Tokenize(src)
	if len(tks) == 0 {
		if src == "" {
			return tks
		}

		// The lexer emits nothing for a source of whitespace alone, and
		// nothing for some text it rejects outright, such as a lone "!",
		// so give the stream one token holding the whole text, and the
		// file stays visible whatever the lexer made of it. The token
		// sits where its text starts, like any other token, and at 1:1:1
		// when the source holds whitespace alone. [IsPlaceholder] tells
		// that token apart from one the lexer made.
		tks = token.Tokens{{
			Type:          token.StringType,
			CharacterType: token.CharacterTypeMiscellaneous,
			Indicator:     token.NotIndicator,
			Value:         src,
			Origin:        src,
			Position:      &token.Position{Line: 1, Column: 1, Offset: 1},
		}}

		if strings.Trim(src, " \t\r\n") != "" {
			repairPositions([]rune(src), tks)
		}

		return tks
	}

	// The lexer drops the source's final line ending and rewrites the
	// whitespace ahead of it, so a file that ends in blank lines tokenizes
	// like one that does not. Give the whitespace after the last text of
	// the source to the last token in place of the whitespace the token
	// ends with, so the stream ends where the file does and the line count
	// matches the text. A last token of whitespace alone, such as the empty
	// content of a block scalar or the lexer's invalid tab token, gets only
	// the part the tokens before it do not already hold. A token that ends
	// in more line breaks than the source does sits in front of text the
	// lexer dropped, such as a lone "!" that ends the file, and it keeps
	// the Origin it came with.
	last := tks[len(tks)-1]

	text := strings.TrimRight(last.Origin, " \t\r\n")
	tail := src[len(strings.TrimRight(src, " \t\r\n")):]

	var held string

	if text == "" {
		var before strings.Builder

		for _, tk := range tks[:len(tks)-1] {
			before.WriteString(tk.Origin)
		}

		joined := before.String()
		held = joined[len(strings.TrimRight(joined, " \t\r\n")):]
	}

	rest, ok := strings.CutPrefix(tail, held)
	if ok && lineend.CountBreaks(rest) >= lineend.CountBreaks(last.Origin[len(text):]) {
		last.Origin = text + rest
	}

	runes := []rune(src)

	restoreWhitespace(runes, tks, repairPositions(runes, tks))

	return tks
}

// byteOrderMark is the UTF-8 byte order mark.
const byteOrderMark = "\ufeff"

// dropByteOrderMarks returns src without the byte order marks that open
// a line before the content of a document, or that stand in front of a
// document marker. A document starts at the start of src and after a
// marker line that holds nothing but the marker and a comment. Blank and
// comment lines keep the document before its content. A line ends where
// the lexer ends one, at "\n", "\r\n", or a bare "\r".
func dropByteOrderMarks(src string) string {
	var sb strings.Builder

	sb.Grow(len(src))

	prefix := true

	for line := range lineend.Lines(src) {
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
// token's text hold as many lines as the source does. It returns the runes
// of src each token's text covers, one [span] for each token of tks.
func repairPositions(src []rune, tks token.Tokens) []span {
	p := &positioner{
		src:      src,
		end:      utf8.RuneCountInString(TrimLineEnding(string(src))),
		line:     1,
		col:      1,
		reliable: true,
	}

	spans := make([]span, len(tks))

	for i, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		spans[i] = p.place(tk)
	}

	return spans
}

// span holds the runes of the source that the text of a token covers,
// from its first text rune to its last.
type span struct {
	start int // The rune index where the text starts.
	end   int // The rune index just past where the text ends.

	// Whether the source holds each text line of the Origin, in order,
	// between start and end. It is false for a token without text and
	// for one the positioner placed where the source does not hold its
	// text.
	ok bool
}

// restoreWhitespace gives the Origins of tks the whitespace of src that
// the lexer drops. The lexer drops the spaces and tabs that end a line,
// the spaces and tabs of a blank line, the space in front of a ":" after
// a quoted or alias key, and the space between a "-" and a "?". It leaves
// every position as it is.
//
// Each token that holds text takes the source's runes from its first text
// rune to its last when the two differ in whitespace alone. Between two
// tokens that hold text, the Origins take the whitespace of the source
// when the whitespace they hold there lacks some of its runes and adds
// none. The line breaks and the lines they close go to the end of the
// earlier Origin, where the lexer puts the line ending that closes a line
// of text. The indentation of the line the later token starts on goes to
// the start of the later one. A ":" whose Origin opens with a line break
// keeps the line breaks in front of it, because the earlier Origin is a
// key, and the parser rejects a key whose Origin ends with a line break.
// That key takes only the spaces and tabs that end its own line. The first
// token that holds text opens with all the whitespace ahead of its text,
// blank lines included. The Origins stay as they are on both sides of a
// token of whitespace alone and of a token whose [span] is not ok. They
// also stay as they are where the lexer repeats a line ending, such as
// after a tag, because the repeat adds a rune the source lacks. A blank
// line there loses its spaces and tabs. The repair of the final line
// ending in [Tokenize] handles the whitespace after the last token that
// holds text.
func restoreWhitespace(src []rune, tks token.Tokens, spans []span) {
	var (
		prev     *token.Token
		prevSpan span
		blocked  bool // Whether a token of whitespace alone sits after prev.
	)

	for i, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		if strings.Trim(tk.Origin, " \t\r\n") == "" {
			blocked = blocked || tk.Origin != ""

			continue
		}

		sp := spans[i]
		if sp.ok {
			restoreText(src, tk, sp)

			if !blocked && (prev == nil || prevSpan.ok) {
				restoreBetween(src, prev, prevSpan, tk, sp)
			}
		}

		prev, prevSpan, blocked = tk, sp, false
	}
}

// restoreText gives tk the runes of src its text covers, in place of the
// text of its Origin, when the source adds whitespace alone, such as the
// spaces of a blank line inside a multi-line plain scalar.
func restoreText(src []rune, tk *token.Token, sp span) {
	lead := len(tk.Origin) - len(strings.TrimLeft(tk.Origin, " \t\r\n"))
	trail := len(strings.TrimRight(tk.Origin, " \t\r\n"))

	text := tk.Origin[lead:trail]

	want := string(src[sp.start:sp.end])
	if want == text || !isSubsequence(text, want) || withoutWhitespace(text) != withoutWhitespace(want) {
		return
	}

	tk.Origin = tk.Origin[:lead] + want + tk.Origin[trail:]
}

// restoreBetween gives prev and cur the whitespace of src between the
// text of the two, when the whitespace the Origins hold between them is
// part of it. The line breaks and the lines they close go to the end of
// the Origin of prev, and the rest to the start of the Origin of cur. When
// cur is a ":" whose Origin opens with a line break and prev holds none
// after its text, prev is a key, so prev takes only the spaces and tabs
// that end its line, and cur takes the rest. A nil prev stands for the
// start of the source, and cur then opens with all the whitespace ahead
// of its text.
//
// A gap that holds anything but whitespace holds text the lexer dropped,
// and a gap the Origins hold more whitespace for than the source has
// holds a line ending the lexer repeats, such as the one after a tag.
// Both keep the Origins as they are.
func restoreBetween(src []rune, prev *token.Token, prevSpan span, cur *token.Token, curSpan span) {
	from := 0
	if prev != nil {
		from = prevSpan.end
	}

	if from > curSpan.start {
		return
	}

	gap := string(src[from:curSpan.start])
	if strings.Trim(gap, " \t\r\n") != "" {
		return
	}

	var trail string

	if prev != nil {
		trail = prev.Origin[len(strings.TrimRight(prev.Origin, " \t\r\n")):]
	}

	text := strings.TrimLeft(cur.Origin, " \t\r\n")
	lead := cur.Origin[:len(cur.Origin)-len(text)]

	if trail+lead == gap || !isSubsequence(trail+lead, gap) {
		return
	}

	if prev == nil {
		cur.Origin = gap + text

		return
	}

	cut := strings.LastIndexAny(gap, "\r\n") + 1
	if cur.Type == token.MappingValueType && strings.ContainsAny(lead, "\r\n") && !strings.ContainsAny(trail, "\r\n") {
		// The parser rejects a key whose Origin holds a line break, so the
		// breaks in front of a ":" stay with it, and the key keeps only the
		// spaces and tabs that end its own line.
		cut = strings.IndexAny(gap, "\r\n")
	}

	prev.Origin = strings.TrimRight(prev.Origin, " \t\r\n") + gap[:cut]
	cur.Origin = gap[cut:] + text
}

// isSubsequence reports whether s holds the bytes of sub in order, with
// any bytes between them.
func isSubsequence(sub, s string) bool {
	i := 0

	for j := 0; j < len(s) && i < len(sub); j++ {
		if s[j] == sub[i] {
			i++
		}
	}

	return i == len(sub)
}

// withoutWhitespace returns s without its spaces, tabs, and line endings.
func withoutWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}

		return r
	}, s)
}

// positioner finds the rune each token's text starts at.
//
// The cursor stands just past the last text placed, and the next token's
// text starts at the first rune after it that is not whitespace, because
// the stream covers the source. The lexer's Offset gives a second
// candidate. The lexer counts the runes it consumes, so its Offset stands a
// fixed distance from the truth until it drops or repeats a rune, and that
// distance is delta. The offset candidate places the tokens after one the
// lexer rewrote, where the cursor is unreliable. The cursor is reliable
// again once either candidate finds a token.
//
// The lexer shortens the Origin of a double-quoted scalar at a "\x", "\u",
// or "\U" escape, so the positioner finds such a token by its text through
// the first backslash. The cursor then moves past the scalar's closing
// quote in the source rather than past the shortened text, where the next
// token would land inside the scalar. A scalar that no quote closes runs
// to the end of the source, and the cursor moves past its last text.
//
// When the lexer gives up on a "\u" or "\U" escape, it ends the invalid
// token of the scalar with the escape's backslash and opens the next
// token with the backslash again. The invalid token gives up the
// backslash, and the cursor stops in front of it, so the next token lands
// on it.
type positioner struct {
	tail string // The whitespace the stream holds after the text placed last.

	src []rune
	end int // The rune index where the last line of src ends, before its final line ending.

	cursor   int // The rune index just past the text placed so far, at most the length of src.
	reliable bool

	// The rune index the line and column count runs up to.
	idx  int
	line int
	col  int

	delta int // The distance from the lexer's Offset to the rune found last.
}

// place moves tk to the rune where its text starts and advances the cursor
// past the text of every line of its Origin, never past the end of the
// source. A token without text sits where the next text starts, or at the
// end of the last line when no text follows, and leaves the cursor where
// it is. A token with text sits at the end of the last line too when the
// source holds whitespace alone past the cursor. When the text follows the
// cursor past whitespace alone, place gives tk the line breaks the lexer
// dropped from that whitespace. A double-quoted scalar found in the source
// moves the cursor past its closing quote instead, or past the last text of
// the source when no quote closes it, and takes its Origin from the source
// when the lexer shortened it. The invalid token of a scalar the lexer cut
// at the backslash of an escape gives that backslash up to the token after
// it. It returns the runes of the source the text of tk covers.
func (p *positioner) place(tk *token.Token) span {
	var (
		placed, found bool
		start         int
	)

	// Whether the source holds every text line of the Origin where place
	// put it.
	matched := true

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
				p.reliable, matched = false, false
			}
		} else {
			// Only the whitespace between a reliable cursor and the text
			// shows the line breaks the lexer dropped. After a token the
			// lexer rewrote, the text sits where the lexer's Offset points
			// instead.
			reliable := p.reliable
			next := p.skipSpace()

			// When the source holds whitespace alone past the cursor, the
			// token sits at the end of the last line, as one without text
			// does.
			at, found = p.locate(tk, text)
			if at >= len(p.src) {
				at = p.end
			}

			if reliable && at == next {
				p.restoreGap(tk, at)
			}

			matched = found && p.hasText(at, text)

			p.setPosition(tk, at)

			placed, start = true, at
		}

		p.cursor = min(at+len(text), len(p.src))
	}

	if !placed {
		// The lexer places the empty content of a block scalar that keeps
		// its trailing lines on the line after the header, a line the
		// source does not have when the header ends the file. When no
		// text follows, the token sits at the end of the last line
		// instead.
		at := p.skipSpace()
		if at >= len(p.src) {
			at = p.end
		}

		p.setPosition(tk, at)

		p.tail += tk.Origin

		return span{}
	}

	// A double-quoted scalar takes its text from the source, so it matches
	// whatever the lexer made of it.
	if found && doubleQuoted(tk) && p.src[start] == '"' && p.restoreQuoted(tk, start) {
		matched = true
	}

	if matched && cutAtEscape(tk) {
		p.dropBackslash(tk, start)
	}

	p.tail = tk.Origin[len(strings.TrimRight(tk.Origin, " \t\r\n")):]

	return span{start: start, end: p.cursor, ok: matched}
}

// doubleQuoted reports whether tk holds a double-quoted scalar: a token the
// lexer read through its closing quote, or the invalid token it makes of a
// scalar no quote closes. The lexer reads such a scalar to the end of the
// source, so that invalid token ends the stream. An invalid token that
// opens with a quote and has tokens after it holds the part of a scalar
// the lexer read before some other fault, such as an unknown escape, and
// the tokens after it hold the rest.
func doubleQuoted(tk *token.Token) bool {
	switch tk.Type {
	case token.DoubleQuoteType:
		return true
	case token.InvalidType:
		return tk.Next == nil && strings.HasPrefix(strings.TrimLeft(tk.Origin, " \t\r\n"), `"`)
	default:
		return false
	}
}

// cutAtEscape reports whether tk is the invalid token the lexer makes of a
// double-quoted scalar when it gives up on a "\u" or "\U" escape, such as
// one the source ends too soon for. The lexer ends that token with the
// escape's backslash and reads on from the backslash, so the token after
// tk opens with it too.
func cutAtEscape(tk *token.Token) bool {
	if tk.Type != token.InvalidType || tk.Next == nil {
		return false
	}

	text := strings.Trim(tk.Origin, " \t\r\n")
	next := strings.TrimLeft(tk.Next.Origin, " \t\r\n")

	return len(text) > 1 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `\`) && strings.HasPrefix(next, `\`)
}

// dropBackslash takes the backslash that ends the text of tk off its
// Origin, so the token after tk holds it alone. The text of tk starts at
// rune index start, and the cursor stands just past the backslash. The
// whitespace in front of the backslash then ends the Origin, and the
// cursor moves back to the end of the text left.
func (p *positioner) dropBackslash(tk *token.Token, start int) {
	trail := len(strings.TrimRight(tk.Origin, " \t\r\n"))
	tk.Origin = tk.Origin[:trail-1] + tk.Origin[trail:]

	p.cursor--
	for p.cursor > start && strings.ContainsRune(" \t\r\n", p.src[p.cursor-1]) {
		p.cursor--
	}
}

// restoreQuoted moves the cursor past the end of the double-quoted scalar
// tk, whose opening quote sits at rune index start. The scalar ends with
// its closing quote. The invalid token the lexer makes of a scalar no
// quote closes runs to the end of the source, and it ends with the last
// text of the source. The lexer drops the code of a "\x", "\u", or "\U"
// escape from the Origin. When the source from the opening quote to the
// end of the scalar differs from the text of the Origin, the Origin takes
// the source's runes in place of its text and keeps the whitespace around
// it. It reports whether it found the end, and a DoubleQuoteType token
// that no quote closes leaves the cursor and the Origin as they are.
func (p *positioner) restoreQuoted(tk *token.Token, start int) bool {
	end, ok := p.closingQuote(start)
	if !ok {
		if tk.Type != token.InvalidType {
			return false
		}

		end = utf8.RuneCountInString(strings.TrimRight(string(p.src), " \t\r\n"))
	}

	p.cursor, p.reliable = end, true

	text := strings.Trim(tk.Origin, " \t\r\n")
	if quoted := string(p.src[start:end]); quoted != text {
		lead := len(tk.Origin) - len(strings.TrimLeft(tk.Origin, " \t\r\n"))
		trail := len(strings.TrimRight(tk.Origin, " \t\r\n"))

		tk.Origin = tk.Origin[:lead] + quoted + tk.Origin[trail:]
	}

	return true
}

// closingQuote returns the rune index just past the quote that closes the
// double-quoted scalar opening at rune index start, and whether one does.
// It steps over each escape as the lexer reads it, so a quote inside an
// escape does not close the scalar.
func (p *positioner) closingQuote(start int) (int, bool) {
	for i := start + 1; i < len(p.src); i++ {
		switch p.src[i] {
		case '"':
			return i + 1, true
		case '\\':
			i += p.escapeWidth(i)
		}
	}

	return 0, false
}

// escapeWidth returns how many runes after the backslash at rune index i
// the lexer reads as part of the escape. A "\x" escape takes the letter
// and two hex digits when the source holds them, "\u" takes the letter and
// four, "\U" the letter and eight, and any other escape takes the one rune
// after the backslash.
func (p *positioner) escapeWidth(i int) int {
	switch {
	case i+1 >= len(p.src):
		return 0
	case p.src[i+1] == 'x' && i+3 < len(p.src):
		return 3
	case p.src[i+1] == 'u':
		return 5
	case p.src[i+1] == 'U':
		return 9
	default:
		return 1
	}
}

// restoreGap gives tk the line breaks and indentation the lexer dropped
// between the text placed last and the text of tk, which starts at rune
// index at. The lexer drops them in front of a "?" or ":" indicator that
// follows a flow collection, a quoted scalar, or a comment, and after a
// double-quoted scalar that holds a tab it drops one line of a run of
// blank lines. The line breaks the stream lacks go at the start of the
// Origin as bare line endings, the way the lexer keeps the blank lines it
// does not drop. Bare line endings keep tabs out of the Origin. The parser
// trims spaces and line breaks from the start of a key's Origin and
// rejects the key when a line break is left, so a tab in front of a line
// break would make it reject the key. [restoreWhitespace] later moves the
// line breaks to the end of the Origin before, together with the spaces
// and tabs of the source, when the two Origins hold nothing else of the
// gap. In front of a ":" they stay where they are, since the Origin
// before belongs to a key. When the lexer dropped every line break in
// front of tk, the indentation of the line tk starts on goes with them.
// An Origin stays as it is when the stream already holds every line break
// of the source.
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
// starts, and reports whether the source holds text there. It asks
// [positioner.pick] for the whole text first. A double-quoted scalar
// whose Origin the lexer shortened at an escape holds its text verbatim
// only through the first backslash, so pick tries that prefix next.
// Failing both, the text sits at the first place on the line of the next
// text that holds it, past text the lexer dropped in front of the token,
// such as the ":" after a tab used as indentation. The search stops at the
// end of that line, so a token the lexer rewrote does not jump to text
// further on. When the line does not hold the text, the token sits where
// the next text starts, and the cursor is unreliable from there on.
func (p *positioner) locate(tk *token.Token, text []rune) (int, bool) {
	if at, ok := p.pick(tk, text); ok {
		return at, true
	}

	if doubleQuoted(tk) {
		if i := slices.Index(text, '\\'); i >= 0 {
			if at, ok := p.pick(tk, text[:i+1]); ok {
				return at, true
			}
		}
	}

	b := p.skipSpace()

	for at := b; at < len(p.src) && p.src[at] != '\n' && p.src[at] != '\r'; at++ {
		if p.hasText(at, text) {
			p.anchor(tk, at)

			return at, true
		}
	}

	p.reliable = false

	return b, false
}

// pick returns the rune index where text, the first text line of tk or
// the start of it, starts, and reports whether one of the two candidates
// holds it. It prefers the first rune after the cursor that is not
// whitespace while the cursor is reliable, and the lexer's Offset shifted
// by delta otherwise, and takes whichever of the two holds the text when
// only one does. When neither does, pick leaves the positioner as it is.
func (p *positioner) pick(tk *token.Token, text []rune) (int, bool) {
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
		return 0, false
	}

	p.anchor(tk, at)

	return at, true
}

// anchor marks the cursor reliable once the text of tk sits at rune index
// at, and takes delta from the lexer's Offset for it.
func (p *positioner) anchor(tk *token.Token, at int) {
	p.reliable = true

	if tk.Position.Offset > 0 {
		p.delta = at - (tk.Position.Offset - 1)
	}
}

// setPosition writes the line, column, and offset of the rune at index at
// into tk, counting the runes between the last position written and it.
// When at sits before the last position written, the count starts again
// from the start of the source. Index at never names the "\n" of a CRLF,
// so the runes counted never split one.
func (p *positioner) setPosition(tk *token.Token, at int) {
	end := min(at, len(p.src))
	if end < p.idx {
		p.idx, p.line, p.col = 0, 1, 1
	}

	if p.idx < end {
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
// The first token that has a non-nil position and holds something other
// than whitespace anchors the shift, and a fresh tokenize places it after
// all the whitespace and line breaks ahead of its text. Earlier tokens can
// hold some of that whitespace alone, such as the invalid token the lexer
// emits for a tab that indents the line after "...". The anchor's own
// Origin can open with the rest, such as the line break that ends a
// preceding "..." line. The anchor lands at line 1, column 1, offset 1,
// where the lexer places the first token of a fresh stream, only when no
// whitespace comes before its text. A stream carrying whitespace alone,
// whose token [Tokenize] positions ahead of the whitespace rather than
// inside it, keeps the positions it has.
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

	var ws strings.Builder

	for _, tk := range tks {
		if tk == nil || tk.Position == nil {
			continue
		}

		// The position points past the whitespace and line breaks that open
		// the Origin, and the text still holds them. An Origin holding
		// whitespace alone has no such split, so it anchors nothing, but its
		// whitespace still comes before the anchor's text.
		trimmed := strings.TrimLeft(tk.Origin, " \t\r\n")
		if trimmed == "" && tk.Origin != "" {
			ws.WriteString(tk.Origin)

			continue
		}

		// Join the whitespace before counting it, so a "\r" that ends one
		// Origin and a "\n" that opens the next count as one CRLF break.
		lead := ws.String() + tk.Origin[:len(tk.Origin)-len(trimmed)]
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
// function yields, for example when a "..." marker opens the stream, so
// pair the two by token offset rather than by index.
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
