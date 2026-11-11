package line

import (
	"cmp"
	"iter"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Line holds the tokens on one line of source. It is content alone. The
// flag, overlays, and annotations that rendering attaches to a line live
// on a [View], so views render one Line many ways without copying it.
//
// A Line never changes after [NewLines] creates it, so it is safe to share
// between views and goroutines. Every token that [Line.Tokens],
// [Line.Token], [Line.TokenAt], or [Line.SourceTokens] hands out belongs
// to the line. Treat them as read-only and call [token.Token.Clone] before
// modifying one.
//
// A Line searches its tokens by column, so [Line.TokenAt] and [Line.Width]
// do not walk the line. [Line.TokenSpan] and [Line.ContentSpan] read each
// token of a line of a few tokens, and such a line does not keep an index.
// A longer line indexes its tokens by offset on the first lookup of the
// columns of a token. TokenSpan and ContentSpan then read only the tokens
// at the offset of their token. On the one line of a minified document,
// each lookup takes about the time that it takes on a short line.
//
// TokenSpan and ContentSpan read the tokens that share an offset in the
// order of the line until one matches. Every token without a position
// counts at offset 0. A lookup on a long line of such tokens thus takes
// time in proportion to the number of tokens before the match.
//
// The zero value is an empty line without a number. A side-by-side diff
// uses it as the placeholder opposite an inserted or deleted line.
//
// Create instances with [NewLines], which cuts a token stream into one Line
// per source line.
type Line struct {
	segments segment.Segments

	// The offset of the source and of the part token of each segment, with
	// the index of the segment. Each pair occurs once, and the pairs sort
	// by offset and then by segment. A token without a position counts at
	// offset 0. The first lookup of a token builds the pairs, so creating a
	// Line does not build them. A line of [maxScan] segments or fewer never
	// builds them.
	byOffset []offsetSegment
	once     sync.Once

	// The 1-indexed line number a renderer displays.
	number int
}

// maxScan is the largest number of segments that a [Line] reads one by
// one to find a token. Most lines of a document with one value on each
// line hold fewer segments. A test of each segment of such a line takes
// less time than a search of a token index. The line also does not
// allocate an index.
const maxScan = 8

// inOrder names the first [maxScan] segments of a line in the order of
// the line. A line that does not index its tokens reads its segments
// through inOrder. The offsets in inOrder do not carry a meaning.
var inOrder = func() [maxScan]offsetSegment {
	var pairs [maxScan]offsetSegment

	for i := range pairs {
		pairs[i].seg = i
	}

	return pairs
}()

// offsetSegment is an offset in the token index of a [Line] and the index
// of a segment whose source or part token sits there.
type offsetSegment struct {
	off, seg int
}

// segmentsAt returns the segments that a token at p can match, in the
// order of the line. The callers read only the index of each segment.
//
// On a line of [maxScan] segments or fewer, they are every segment of the
// line. On a longer line, they are the segments whose source or part
// token sits at the offset of p. The first call on such a line builds its
// token index. A segment of a token at another position with the same
// offset can be among them. On a line of either length, the segments that
// match a token come in the same order, so the first match is the same.
func (l *Line) segmentsAt(p *token.Position) []offsetSegment {
	if len(l.segments) <= maxScan {
		return inOrder[:len(l.segments)]
	}

	l.once.Do(func() { l.byOffset = offsetSegments(l.segments) })

	off := offsetOf(p)

	// The segments at one offset can be every segment of the line, as on
	// a line of tokens without positions. A search thus finds each end of
	// them. The second search looks for a larger offset and not for off+1,
	// because off+1 overflows when off is the largest int.
	lo := sort.Search(len(l.byOffset), func(i int) bool { return l.byOffset[i].off >= off })
	at := l.byOffset[lo:]
	n := sort.Search(len(at), func(i int) bool { return at[i].off > off })

	return at[:n]
}

// offsetSegments returns the offset of the source and of the part token
// of each segment of segs with the index of the segment, sorted by offset
// and then by segment.
func offsetSegments(segs segment.Segments) []offsetSegment {
	n := 0

	for _, seg := range segs {
		_, count := segmentOffsets(seg)
		n += count
	}

	pairs := make([]offsetSegment, 0, n)

	for i, seg := range segs {
		offs, count := segmentOffsets(seg)
		for _, off := range offs[:count] {
			pairs = append(pairs, offsetSegment{off: off, seg: i})
		}
	}

	// The offsets of a stream from the tokenizer ascend along a line, so
	// the pairs come nearly in order. Each pair occurs once, so the order
	// is the same for any sort.
	slices.SortFunc(pairs, func(a, b offsetSegment) int {
		return cmp.Or(cmp.Compare(a.off, b.off), cmp.Compare(a.seg, b.seg))
	})

	return pairs
}

// segmentOffsets returns the offsets of the source and of the part token
// of seg, and the number of offsets it returns. It skips a nil token. A
// segment whose source and part token share an offset, such as the
// segment of a token on one line, has one offset.
func segmentOffsets(seg segment.Segment) ([2]int, int) {
	var (
		offs [2]int
		n    int
	)

	for _, tk := range [2]*token.Token{seg.Source(), seg.Part()} {
		if tk == nil {
			continue
		}

		off := offsetOf(tk.Position)
		if n > 0 && offs[0] == off {
			continue
		}

		offs[n] = off
		n++
	}

	return offs, n
}

// Number returns the 1-indexed line number of this [Line], or 0 for the
// zero value.
func (l *Line) Number() int {
	return l.number
}

// Content returns this [Line]'s content as a string.
//
// Content strips the line ending (LF, CRLF, or a bare CR) from each
// segment, so the result is a single line.
func (l *Line) Content() string {
	var sb strings.Builder

	for _, seg := range l.segments {
		sb.WriteString(tokens.TrimLineEnding(seg.Part().Origin))
	}

	return sb.String()
}

// Tokens returns the [token.Tokens] for this [Line] with line-adjusted
// positions. Each token links to its neighbors on the line through Next and
// Prev, and the chain stops at the line boundary.
//
// The slice is new, but the tokens belong to the line. Treat them as
// read-only.
func (l *Line) Tokens() token.Tokens {
	return l.segments.PartTokens()
}

// Token returns the [*token.Token] at the given index. The token belongs
// to the line, so treat it as read-only.
// Token panics if idx is out of range.
func (l *Line) Token(idx int) *token.Token {
	return l.segments[idx].Part()
}

// SourceTokens returns the original lexer tokens that have a part on this
// [Line], in column order, each once. A token that spans several lines
// appears in the result of every line it touches.
//
// The slice is new, but the tokens are the originals. Treat them as
// read-only.
func (l *Line) SourceTokens() token.Tokens {
	if len(l.segments) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(l.segments))

	var last *token.Token

	for _, seg := range l.segments {
		if src := seg.Source(); src != last {
			result = append(result, src)
			last = src
		}
	}

	return result
}

// TokenAt returns the original lexer token that covers the given 0-indexed
// column, or nil when the column does not lie in any token. A column in
// the indentation resolves to the token that follows it, as
// [Lines.TokenAt] describes. Treat the token as read-only.
func (l *Line) TokenAt(col int) *token.Token {
	return l.segments.SourceTokenAt(col)
}

// TokenSpan returns the columns tk occupies on this [Line]. The token can be
// a lexer token, one of the per-line parts from [Line.Tokens], or a copy of
// either. Any token on this line with tk's type, value, origin, and
// position matches, even one from the other revision of a diff, which
// [Lines.TokenRanges] skips. The second result is false when tk is nil or
// does not have a part on this line.
func (l *Line) TokenSpan(tk *token.Token) (position.Span, bool) {
	return l.span(tk, wholeSpan)
}

// ContentSpan returns the columns of tk's content on this [Line], excluding
// leading and trailing spaces and tabs. The span is empty when tk holds only
// spaces and tabs on this line. The token can be a lexer token or one of the
// per-line parts, as for [Line.TokenSpan]. The second result is false when
// tk is nil or does not have a part on this line.
func (l *Line) ContentSpan(tk *token.Token) (position.Span, bool) {
	return l.span(tk, segment.Segment.ContentSpan)
}

// wholeSpan returns the columns of seg within itself, from 0 to its width.
func wholeSpan(seg segment.Segment) position.Span {
	return position.NewSpan(0, seg.Width())
}

// span returns the columns that span picks within the first segment that
// holds tk or a copy of it, offset to the line.
//
// It reads the segments that tk can match in the order of the line and
// stops at the first that holds tk.
func (l *Line) span(tk *token.Token, span func(segment.Segment) position.Span) (position.Span, bool) {
	if tk == nil {
		return position.Span{}, false
	}

	for _, e := range l.segmentsAt(tk.Position) {
		if seg := l.segments[e.seg]; seg.Contains(tk) {
			return spanOnLine(seg, span), true
		}
	}

	return position.Span{}, false
}

// find looks tk up on this line as [Line.span] does, and its first two
// results are the results of span. The third result reports whether a
// segment has tk itself, not a copy, as its source or part token.
//
// It reads the segments that tk can match in the order of the line and
// stops at the first that has tk itself. The caller passes a tk that is
// not nil.
func (l *Line) find(
	tk *token.Token, span func(segment.Segment) position.Span,
) (position.Span, bool, bool) {
	var (
		sp    position.Span
		found bool
	)

	for _, e := range l.segmentsAt(tk.Position) {
		seg := l.segments[e.seg]

		if !found && seg.Contains(tk) {
			sp, found = spanOnLine(seg, span), true
		}

		// A segment that has tk itself holds tk, so the columns are known
		// by now.
		if seg.Source() == tk || seg.Part() == tk {
			return sp, true, true
		}
	}

	return sp, found, false
}

// spanOnLine returns the columns that span picks within seg, offset to
// the line of seg.
func spanOnLine(seg segment.Segment, span func(segment.Segment) position.Span) position.Span {
	col, in := seg.Start(), span(seg)

	return position.NewSpan(col+in.Start, col+in.End)
}

// IsEmpty reports whether this [Line] holds zero tokens.
func (l *Line) IsEmpty() bool {
	return len(l.segments) == 0
}

// Width returns the number of runes in this line's content, excluding the
// line ending. Positions on the line count columns in the same unit. Width
// does not measure the display width. A wide rune, such as a Chinese,
// Japanese, or Korean ideograph, takes one column but two terminal cells.
func (l *Line) Width() int {
	return l.segments.Width()
}

// Runes returns an iterator over the runes on this [Line]. Each iteration
// yields the 0-indexed column and the rune at that column.
//
// The iteration includes the line ending as a single '\n' at column
// [Line.Width], the column after the last visible rune. It yields '\n'
// whether the source used LF, CRLF, or a bare CR. A line yields at most
// one newline. The lexer sometimes repeats a line ending at the start of
// the next token. That repeat sits on the same line as the ending it
// copies.
func (l *Line) Runes() iter.Seq2[int, rune] {
	return func(yield func(int, rune) bool) {
		col := 0

		for _, seg := range l.segments {
			for _, r := range tokens.TrimLineEnding(seg.Part().Origin) {
				if !yield(col, r) {
					return
				}

				col++
			}
		}

		// The splitter joins a token that holds no text and sits past the
		// end of the source onto the last line, after the segment that
		// ends it. The ending is therefore on the last segment that holds
		// any of the source.
		for _, seg := range slices.Backward(l.segments) {
			origin := seg.Part().Origin
			if origin == "" {
				continue
			}

			if hasLineEnding(origin) {
				yield(col, '\n')
			}

			return
		}
	}
}

// hasLineEnding reports whether origin ends with a line ending, as
// [tokens.TrimLineEnding] recognizes them.
func hasLineEnding(origin string) bool {
	return tokens.TrimLineEnding(origin) != origin
}

// String returns the line number and content, as "   1 | key: value". The
// result matches the row [View.String] renders for an undecorated line in
// a view whose largest line number is less than 10000. A line without a
// number, such as a zero Line, gets a blank gutter.
func (l *Line) String() string {
	return contentRow(l.Number(), FlagDefault, l.Content(), 4)
}
