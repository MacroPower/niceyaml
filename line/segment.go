package line

import (
	"iter"

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
	// they were added, so the last one added is the outermost. The slice
	// is the segment's own and holds copies of the view's overlays, so
	// changing it leaves the view and the other segments as they were.
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
		col := 0

		for j, seg := range ln.segments {
			origin := tokens.TrimLineEnding(seg.Part().Origin)
			sp := seg.ContentSpan()
			start := col

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
				if !yieldPiece(yield, origin, start, position.NewSpan(col, piece.end), piece.kind, overlays) {
					return
				}

				col = piece.end
			}
		}
	}
}

// yieldPiece yields the segments of one piece of a token: cols of the
// line, which origin covers from the column start, in kind k, split where
// an overlay starts or ends inside them. A piece that covers no columns
// yields nothing. Reports whether the iteration goes on.
func yieldPiece(
	yield func(Segment) bool,
	origin string,
	start int,
	cols position.Span,
	k kind.Kind,
	overlays Overlays,
) bool {
	if cols.Len() == 0 {
		return true
	}

	runes := []rune(origin)

	for _, span := range splitAtOverlays(cols, overlays) {
		var covering Overlays

		for _, o := range overlays {
			if o.Cols.Contains(span.Start) {
				covering = append(covering, o)
			}
		}

		seg := Segment{
			Cols:     span,
			Text:     string(runes[span.Start-start : span.End-start]),
			Kind:     k,
			Overlays: covering,
		}

		if !yield(seg) {
			return false
		}
	}

	return true
}

// splitAtOverlays cuts cols where an overlay starts or ends strictly
// inside them and returns the pieces in column order.
func splitAtOverlays(cols position.Span, overlays Overlays) []position.Span {
	edges := []int{cols.Start}

	for _, o := range overlays {
		for _, edge := range [...]int{o.Cols.Start, o.Cols.End} {
			if edge > cols.Start && edge < cols.End {
				edges = insertEdge(edges, edge)
			}
		}
	}

	edges = append(edges, cols.End)

	spans := make([]position.Span, 0, len(edges)-1)
	for i := range len(edges) - 1 {
		spans = append(spans, position.NewSpan(edges[i], edges[i+1]))
	}

	return spans
}

// insertEdge inserts edge into the ascending edges, once.
func insertEdge(edges []int, edge int) []int {
	for i, e := range edges {
		switch {
		case e == edge:
			return edges

		case e > edge:
			edges = append(edges, 0)
			copy(edges[i+1:], edges[i:])

			edges[i] = edge

			return edges
		}
	}

	return append(edges, edge)
}
