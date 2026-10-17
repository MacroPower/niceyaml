package line

import (
	"cmp"
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
)

// Lines is an ordered collection of [*Line] values, the content that a
// [View] decorates and that the finder and diff packages read.
//
// Lines carries the tokens split per line. It does not know about YAML
// documents, parsing, or files. A Lines value can therefore describe
// content that is not a YAML document, such as a diff that interleaves
// lines from two revisions. The lines never change after creation, and
// code outside this package cannot add to, remove from, or reorder a Lines
// value. It is safe to share between views and goroutines, and a view over
// it shares the lines instead of copying them.
//
// Reach a line with [Lines.Line] or by ranging over [Lines.All], as
// with a [View]. The zero value is empty.
//
// Create instances with [NewLines], which cuts a token stream into one
// [Line] per source line, or [Collect], which gathers lines taken from
// other Lines values.
type Lines struct {
	idx   *lineIndex
	lines []*Line
}

// NewLines creates new [Lines] from [token.Tokens], one [Line] per source
// line. NewLines expects the tokens that [tokens.Tokenize] returns, or the
// clones that [tokens.ResetPositions] makes of them. The positions of such
// tokens name the rune where each token's text starts.
//
// NewLines cuts multiline tokens, such as block scalars and quoted multiline
// strings, into one part per line. Each part is a token whose Position
// describes its own line. The first part that holds the token's text keeps
// the token's Column and Offset, and every later part names the rune where
// it starts. Every part keeps a reference to the original token that
// NewLines cut it from. [Lines.Tokens] recombines the parts into the
// original tokens. NewLines returns the zero Lines when tks is empty.
func NewLines(tks token.Tokens) Lines {
	split := segment.Split(tks)
	if len(split) == 0 {
		return Lines{}
	}

	lines := make([]*Line, len(split))
	for i, l := range split {
		lines[i] = &Line{segments: l.Segments, number: l.Number}
	}

	return newLines(lines)
}

// Collect creates new [Lines] that hold ls in the order given, such as the
// lines of two revisions a diff interleaves. The result shares the lines
// with the Lines values they came from, so a [View] over it finds them by
// identity as it finds them in the originals. Collect panics when a line is
// nil.
func Collect(ls ...*Line) Lines {
	for i, l := range ls {
		if l == nil {
			panic(fmt.Sprintf("line: Collect: line %d is nil", i))
		}
	}

	return newLines(slices.Clone(ls))
}

// newLines creates new [Lines] that own ls, which the caller must not
// change afterward. It returns the zero Lines when ls is empty.
func newLines(ls []*Line) Lines {
	if len(ls) == 0 {
		return Lines{}
	}

	return Lines{lines: ls, idx: &lineIndex{}}
}

// lineIndex finds the indices that hold a [*Line] by identity, and the
// indices of the lines that hold a token. The first lookup of each kind
// builds its maps, so creating [Lines] does not build a map.
type lineIndex struct {
	// The first index that holds each line.
	first map[*Line]int
	// The indices, in ascending order, of the lines with a segment whose
	// source or part token sits at each position.
	byPos map[posKey][]int
	// The next index that holds the same line as each index, or -1 at its
	// last occurrence. It is nil when every line occurs once.
	next    []int
	once    sync.Once
	posOnce sync.Once
}

// posKey holds the fields of a [*token.Position] that
// [segment.Segment.Contains] compares, so a token can match a segment only
// when the keys of their positions are equal. A nil position has the zero
// key.
//
//nolint:unused // The fields tell the keys of the position map apart.
type posKey struct {
	line, col, off int
	set            bool
}

// keyOf returns the key of p.
func keyOf(p *token.Position) posKey {
	if p == nil {
		return posKey{}
	}

	return posKey{line: p.Line, col: p.Column, off: p.Offset, set: true}
}

// build indexes ls by identity.
func (x *lineIndex) build(ls []*Line) {
	x.first = make(map[*Line]int, len(ls))

	// The last index seen of each line that repeats.
	var last map[*Line]int

	for i, l := range ls {
		prev, seen := x.first[l]
		if !seen {
			x.first[l] = i

			continue
		}

		if x.next == nil {
			x.next = make([]int, len(ls))
			for j := range x.next {
				x.next[j] = -1
			}

			last = map[*Line]int{}
		}

		if j, ok := last[l]; ok {
			prev = j
		}

		x.next[prev] = i
		last[l] = i
	}
}

// buildPositions indexes ls by the positions of the source and part token
// of every segment.
func (x *lineIndex) buildPositions(ls []*Line) {
	x.byPos = make(map[posKey][]int, len(ls))

	for i, l := range ls {
		for _, seg := range l.segments {
			x.addPosition(seg.Source(), i)
			x.addPosition(seg.Part(), i)
		}
	}
}

// addPosition records index i under the position of tk, once per index.
// The indices of one line arrive together, so comparing with the last
// index recorded under the key skips repeats.
func (x *lineIndex) addPosition(tk *token.Token, i int) {
	if tk == nil {
		return
	}

	k := keyOf(tk.Position)

	idxs := x.byPos[k]
	if n := len(idxs); n > 0 && idxs[n-1] == i {
		return
	}

	x.byPos[k] = append(idxs, i)
}

// index returns the identity index of ls, or nil when ls is empty. The
// first call builds the index.
func (ls Lines) index() *lineIndex {
	if ls.idx == nil {
		return nil
	}

	ls.idx.once.Do(func() { ls.idx.build(ls.lines) })

	return ls.idx
}

// firstIndex returns the first index that holds l and true, or false when
// ls does not hold l.
func (ls Lines) firstIndex(l *Line) (int, bool) {
	x := ls.index()
	if x == nil {
		return 0, false
	}

	i, ok := x.first[l]

	return i, ok
}

// nextIndex returns the next index after i that holds the same line as i
// and true, or false when i holds its last occurrence.
func (ls Lines) nextIndex(i int) (int, bool) {
	x := ls.index()
	if x == nil || x.next == nil {
		return 0, false
	}

	j := x.next[i]
	if j < 0 {
		return 0, false
	}

	return j, true
}

// linesAt returns the indices, in ascending order, of the lines with a
// segment whose source or part token sits at p. The first call builds the
// position index. Every line where a token at p can match a segment is
// among them.
func (ls Lines) linesAt(p *token.Position) []int {
	if ls.idx == nil {
		return nil
	}

	ls.idx.posOnce.Do(func() { ls.idx.buildPositions(ls.lines) })

	return ls.idx.byPos[keyOf(p)]
}

// Line returns the [*Line] at index i. Line panics when i is outside the
// collection, as indexing a slice does.
func (ls Lines) Line(i int) *Line {
	return ls.lines[i]
}

// Len returns the number of lines.
func (ls Lines) Len() int {
	return len(ls.lines)
}

// IsEmpty reports whether the collection holds zero lines.
func (ls Lines) IsEmpty() bool {
	return len(ls.lines) == 0
}

// Width returns the maximum [Line.Width] across all lines.
func (ls Lines) Width() int {
	var maxWidth int

	for _, l := range ls.lines {
		if w := l.Width(); w > maxWidth {
			maxWidth = w
		}
	}

	return maxWidth
}

// All returns an iterator over every line, in content order. Each
// iteration yields the 0-indexed line index and the [*Line] at that index.
// [Lines.Within] yields the lines of some spans.
func (ls Lines) All() iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		for i := range ls.lines {
			if !yield(i, ls.lines[i]) {
				return
			}
		}
	}
}

// Within returns an iterator over the lines within any of the given spans.
// It yields them in content order and yields each line one time, whatever
// order the spans come in and however they overlap. Without spans, the
// iterator is empty, so the nil result of [position.ContextSpans] does not
// select any line. [Lines.All] yields every line.
//
// Each iteration yields the 0-indexed line index and the [*Line] at that
// index. A span that reaches outside the collection selects only the lines
// inside the collection.
func (ls Lines) Within(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		for _, s := range mergeSpans(spans, len(ls.lines)) {
			for i := s.Start; i < s.End; i++ {
				if !yield(i, ls.lines[i]) {
					return
				}
			}
		}
	}
}

// Runes returns an iterator over every rune of the lines, in content
// order. [Lines.RunesIn] yields the runes of some ranges.
//
// Each iteration yields a [position.Position] and the rune at that
// position. The iteration includes each line ending as a single '\n', as
// [Line.Runes] does. The newline occupies the column after the last
// visible rune whether the source used LF or CRLF, so its column matches
// [Line.Width].
func (ls Lines) Runes() iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		every := position.Spans{position.NewSpan(0, math.MaxInt)}

		for i := range ls.lines {
			if !ls.yieldRunes(i, every, yield) {
				return
			}
		}
	}
}

// RunesIn returns an iterator over the runes within any of the given
// ranges. It yields them in content order and yields each rune one time,
// whatever order the ranges come in and however they overlap. Without
// ranges, the iterator is empty, so the ranges of a search without matches
// do not select any rune. [Lines.Runes] yields every rune.
//
// Each iteration yields a [position.Position] and the rune at that
// position, line endings included, as [Lines.Runes] yields them.
func (ls Lines) RunesIn(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		// A range holds runes only on the lines from its start line through
		// its end line, so [Lines.Within] of those line spans visits each
		// line that any range touches once, in content order. Capping the
		// end line first keeps the span end from overflowing.
		spans := make([]position.Span, len(ranges))
		for j, rng := range ranges {
			spans[j] = position.NewSpan(rng.Start.Line, min(rng.End.Line, len(ls.lines)-1)+1)
		}

		// The lines come in ascending order, so a sweep over the ranges
		// sorted by start line keeps the ranges on the current line
		// active. Each line then walks its runes once against the merged
		// columns of those ranges, whatever the number of ranges.
		sorted := slices.Clone(ranges)
		slices.SortFunc(sorted, func(a, b position.Range) int {
			return cmp.Compare(a.Start.Line, b.Start.Line)
		})

		var (
			active []position.Range
			cols   []position.Span
			next   int
		)

		for i := range ls.Within(spans...) {
			for ; next < len(sorted) && sorted[next].Start.Line <= i; next++ {
				active = append(active, sorted[next])
			}

			active = slices.DeleteFunc(active, func(rng position.Range) bool {
				return rng.End.Line < i
			})

			cols = cols[:0]
			for _, rng := range active {
				cols = append(cols, lineCols(rng, i))
			}

			if !ls.yieldRunes(i, mergeSpans(cols, math.MaxInt), yield) {
				return
			}
		}
	}
}

// lineCols returns the columns of line i that r contains, as
// [position.Range.Contains] tests them. A range that continues past line i
// contains every column after its start, the line ending included.
func lineCols(r position.Range, i int) position.Span {
	start, end := 0, math.MaxInt

	if i == r.Start.Line {
		start = r.Start.Col
	}

	if i == r.End.Line {
		end = r.End.Col
	}

	return position.NewSpan(start, end)
}

// yieldRunes yields, as positions, the runes of the line at index lineIdx
// that lie within one of cols. The caller must sort cols and keep its
// spans disjoint. It returns false when yield stops the iteration.
func (ls Lines) yieldRunes(lineIdx int, cols position.Spans, yield func(position.Position, rune) bool) bool {
	if len(cols) == 0 {
		return true
	}

	k := 0

	for col, r := range ls.lines[lineIdx].Runes() {
		for k < len(cols) && cols[k].End <= col {
			k++
		}

		if k == len(cols) {
			return true
		}

		if col < cols[k].Start {
			continue
		}

		if !yield(position.New(lineIdx, col), r) {
			return false
		}
	}

	return true
}

// Tokens reconstructs the full [token.Tokens] stream from all lines.
//
// Each original token appears once, where it first occurs. A multiline
// token that [NewLines] split across lines therefore appears as the single
// token the lexer produced. The tokens of a line that the collection holds
// more than once also appear once. The slice is new, but the tokens are
// the originals. Treat them as read-only.
func (ls Lines) Tokens() token.Tokens {
	if len(ls.lines) == 0 {
		return nil
	}

	var (
		result = token.Tokens{}
		seen   = map[*token.Token]struct{}{}
	)

	for _, l := range ls.lines {
		for _, src := range l.SourceTokens() {
			if _, ok := seen[src]; ok {
				continue
			}

			seen[src] = struct{}{}

			result = append(result, src)
		}
	}

	return result
}

// SliceLines splits r into one [position.Range] per line of the [Lines]
// it covers, and each range holds the columns of r on that line. A range
// starts at the start column of r on the first line and at column 0 on
// every later one. It ends at the end column of r on the last line and at
// [Line.Width] on every earlier one.
// The result is what [View.AddOverlay] marks for r, so a caller that
// inspects or compares the per-line ranges sees the columns the lines
// hold.
//
// A range that ends at column 0 of a later line does not cover any column
// of that line, as [position.Range.LastLine] counts it. A range that ends
// before its start does not cover any line. SliceLines leaves out lines
// outside the collection, columns before 0, and columns past the width of
// a line. It returns a range only for a line on which r covers at least
// one column. SliceLines returns nil when r does not cover a column of any
// line.
func (ls Lines) SliceLines(r position.Range) position.Ranges {
	var result position.Ranges

	for i := max(0, r.Start.Line); i <= min(r.LastLine(), len(ls.lines)-1); i++ {
		start, end := 0, ls.lines[i].Width()

		if i == r.Start.Line {
			start = max(0, r.Start.Col)
		}

		if i == r.End.Line {
			end = min(end, r.End.Col)
		}

		if end <= start {
			continue
		}

		result = append(result, position.NewRange(position.New(i, start), position.New(i, end)))
	}

	return result
}

// TokenAt returns the original [*token.Token] that covers the given
// position.
//
// The token is the one the lexer produced, so a caller can pass it back
// to [Lines.TokenRanges] or [Lines.ContentRanges] to find every range it
// occupies. Treat it as read-only.
//
// The lexer bundles the indentation of a line into the token that ends on
// the line before. A position in the indentation therefore resolves to the
// token that follows it on the same line instead.
//
// TokenAt returns nil when the position is out of bounds or does not lie
// in any token.
func (ls Lines) TokenAt(pos position.Position) *token.Token {
	if pos.Line < 0 || pos.Line >= len(ls.lines) {
		return nil
	}

	return ls.lines[pos.Line].TokenAt(pos.Col)
}

// TokenRanges returns the ranges tk occupies, one per line where it holds
// runes besides a line ending. A line where tk holds only a line ending,
// such as a blank line that a block scalar keeps, does not contribute a
// range. The lexer bundles the indentation of a line into the token that
// ends on the line before. That token's range on the indented line
// therefore covers the indentation alone.
//
// The token can be a lexer token, such as one that [Lines.TokenAt] or
// [Lines.Tokens] returns. It can also be one of the per-line parts from
// [Line.Tokens], or a copy of either. A token taken from the AST that a
// parser built from the same stream is such a copy.
//
// When a line holds tk itself, only the lines that hold it match. A token
// from one revision of a diff therefore skips the other revision's token
// at the same position. Otherwise, tk matches every token with the same
// type, value, origin, and position. TokenRanges returns nil when tk is
// nil or when it does not find tk.
func (ls Lines) TokenRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, (*Line).TokenSpan)
}

// ContentRanges returns the ranges of tk's content, one per line it appears
// on, excluding leading and trailing spaces and tabs. A line where tk holds
// only spaces and tabs does not contribute a range.
//
// The token can be a lexer token, one of the per-line parts, or a copy of
// either, as for [Lines.TokenRanges]. ContentRanges returns nil when tk is
// nil or when it does not find tk.
func (ls Lines) ContentRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, (*Line).ContentSpan)
}

// ranges collects one range per line that holds tk. The span function
// picks the columns within each line, and a line where span is empty does
// not contribute a range. It visits only the lines that hold a token at
// tk's position, and only the lines that hold tk itself when any line
// does.
func (ls Lines) ranges(
	tk *token.Token, span func(*Line, *token.Token) (position.Span, bool),
) position.Ranges {
	if tk == nil {
		return nil
	}

	idxs := ls.linesAt(tk.Position)

	// Another line's token with tk's fields is a different token, such as
	// the other revision's in a diff, so it matches only a copy of tk.
	held := slices.ContainsFunc(idxs, func(i int) bool {
		return ls.lines[i].holds(tk)
	})

	var result position.Ranges

	for _, i := range idxs {
		if held && !ls.lines[i].holds(tk) {
			continue
		}

		sp, ok := span(ls.lines[i], tk)
		if !ok || sp.Len() <= 0 {
			continue
		}

		result = append(result, position.NewRange(
			position.New(i, sp.Start),
			position.New(i, sp.End),
		))
	}

	return result
}

// Content returns the combined content of all lines as a string.
// It joins the lines with newlines, so the result ends each line but the
// last with "\n" whatever line endings the source used. A program that
// needs the bytes of the source reads
// [go.jacobcolvin.com/niceyaml.Source.Text].
func (ls Lines) Content() string {
	if len(ls.lines) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range ls.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.Content())
	}

	return sb.String()
}

// String renders the lines as [View.String] renders a view over them
// without decoration: each line behind its number, one per row.
func (ls Lines) String() string {
	return NewView(ls).String()
}
