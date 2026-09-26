package line

import (
	"iter"
	"slices"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Segment is a run of the content of one line that renders in one style:
// the columns it covers, the text on them, the [kind.Kind] of the token
// the text belongs to, and the overlays of the view that cover it.
//
// Receive instances from [View.Segments].
type Segment struct {
	// Text is the content of the line on Cols, without the line ending.
	Text string
	// Kind is the kind of the token the text belongs to, as [Line.Kind]
	// reads it, or [kind.Text] for the whitespace a token carries before
	// or after its text.
	Kind kind.Kind
	// Overlays are the overlays of the view that cover Cols, in the order
	// the view took them in, so the last one added is the outermost. The
	// slice is the segment's own and holds copies of the view's overlays,
	// so changing it leaves the view and the other segments as they were.
	Overlays Overlays
	// Cols are the columns of the line the segment covers.
	Cols position.Span
}

// Segments returns an iterator over the segments of line i, in column
// order, covering its content once: one per run of text that one kind
// and one set of overlays style. Each token on the line is one segment
// for its text, in the kind [Line.Kind] gives it, with the spaces and
// tabs it carries before and after that text, such as the indentation
// before a value or the gap before a comment, as segments of their own
// in [kind.Text]. A segment splits where an overlay of the view starts
// or ends inside it, and [Segment.Overlays] holds the overlays that cover
// each piece. Adjacent segments that style the same are separate, so a
// renderer that joins them decides that itself.
//
// A renderer of any kind reads the segments in place of the tokens and
// the overlays, so it styles the line as the printer does without
// reading either:
//
//	for i := range view.All() {
//		for seg := range view.Segments(i) {
//			class := string(seg.Kind)
//			if n := len(seg.Overlays); n > 0 {
//				class = string(seg.Overlays[n-1].Kind)
//			}
//
//			fmt.Fprintf(w, `<span class=%q>%s</span>`, class, html.EscapeString(seg.Text))
//		}
//
//		fmt.Fprintln(w)
//	}
//
// An empty line has no segments. Panics if i is outside the content.
func (v *View) Segments(i int) iter.Seq[Segment] {
	ln := v.lines.lines[i]
	overlays := v.Overlays(i)

	return func(yield func(Segment) bool) {
		runs := newOverlayRuns(overlays)
		col := 0

		for j, seg := range ln.segments {
			rest := tokens.TrimLineEnding(seg.Part().Origin)
			sp := seg.ContentSpan()

			// The separator is the whitespace the token carries before its
			// text, whether that text is a plain scalar, a quoted string,
			// an anchor, a comment, or the continuation of a multiline
			// scalar. The trailer is the whitespace it carries after that
			// text, which the lexer hands to the token preceding a comment.
			// Neither is part of the text, so both are Text. The content
			// span of the segment covers the text between them. A token of
			// nothing but whitespace is all separator.
			pieces := [...]struct {
				kind kind.Kind
				end  int
			}{
				{kind.Text, col + sp.Start},
				{ln.Kind(j), col + sp.End},
				{kind.Text, col + seg.Width()},
			}

			for _, piece := range pieces {
				for col < piece.end {
					end, covering := runs.next(col, piece.end)
					n := runeBytes(rest, end-col)

					s := Segment{
						Cols:     position.NewSpan(col, end),
						Text:     rest[:n],
						Kind:     piece.kind,
						Overlays: covering,
					}

					rest = rest[n:]
					col = end

					if !yield(s) {
						return
					}
				}
			}
		}
	}
}

// overlayRuns cuts the columns of one line into runs where its overlays
// start or end, so every column of a run has the same overlays over it.
// Each call to [overlayRuns.next] asks for columns at or after the ones
// the call before asked for, so it moves through the edges once, from
// left to right.
//
// Create instances with [newOverlayRuns].
type overlayRuns struct {
	// Edges are the columns where an overlay starts or ends, ascending and
	// without repeats.
	edges []int
	// Covers holds the overlays over each run, in the order the view took
	// them in. Covers[k] is the run after the first k edges, so covers[0]
	// is the run before the first edge. It is nil when there are no
	// overlays.
	covers []Overlays
	// K is the run holding the col the last call to next asked for.
	k int
}

// newOverlayRuns creates a new [overlayRuns] over the given overlays. An
// overlay that covers no columns, one added with its end at or before its
// start, still cuts the line at both edges.
func newOverlayRuns(overlays Overlays) overlayRuns {
	if len(overlays) == 0 {
		return overlayRuns{}
	}

	edges := make([]int, 0, 2*len(overlays))
	for _, o := range overlays {
		edges = append(edges, o.Cols.Start, o.Cols.End)
	}

	slices.Sort(edges)

	edges = slices.Compact(edges)

	covers := make([]Overlays, len(edges)+1)

	for _, o := range overlays {
		if o.Cols.Start >= o.Cols.End {
			continue
		}

		first, _ := slices.BinarySearch(edges, o.Cols.Start)
		last, _ := slices.BinarySearch(edges, o.Cols.End)

		// Start is edges[first] and End is edges[last], so the overlay
		// covers the runs from first+1 through last.
		for k := first + 1; k <= last; k++ {
			covers[k] = append(covers[k], o)
		}
	}

	return overlayRuns{edges: edges, covers: covers}
}

// next returns where the run holding col ends, at the next edge after col
// or at end if that comes first, and a new slice of the overlays over it,
// or nil for none.
func (r *overlayRuns) next(col, end int) (int, Overlays) {
	for r.k < len(r.edges) && r.edges[r.k] <= col {
		r.k++
	}

	if r.k < len(r.edges) && r.edges[r.k] < end {
		end = r.edges[r.k]
	}

	if r.covers == nil {
		return end, nil
	}

	return end, slices.Clone(r.covers[r.k])
}

// runeBytes returns the length in bytes of the first n runes of s, or of
// all of s if it holds fewer.
func runeBytes(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}

		n--
	}

	return len(s)
}
